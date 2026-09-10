package gcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	R "github.com/IBM/fp-go/v2/retry"
	"github.com/urfave/cli/v2"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
)

type IAMClient struct {
	service   *iam.Service
	projectId string
}

// createServiceAccount creates a service account.
func CreateServiceAccount(cliContext *cli.Context) error {
	saName := cliContext.String("name")
	saDisplayName := cliContext.String("display-name")
	saDescription := cliContext.String("description")
	projectName := cliContext.String("project")
	rolesFilePath := cliContext.String("roles-file")
	credentialOutputPath := cliContext.String("output-file")

	// Create IAM Service
	ctx := context.Background()
	svc, err := iam.NewService(ctx)
	if err != nil {
		slog.Error("Error creating service account", "error", err)
		return err
	}
	client := &IAMClient{
		service:   svc,
		projectId: projectName,
	}
	// Check if Service Account exists.
	sa := client.getServiceAccount(projectName, saName)
	if sa == nil {
		// Create Service Account if it does not exist.
		slog.Info(
			fmt.Sprintf(
				"Creating service account %s in %s...",
				saName,
				client.projectId,
			),
		)
		sa, err = client.createServiceAccount(
			saName,
			saDisplayName,
			saDescription,
		)
		if err != nil {
			slog.Error("Error creating service account", "error", err)
			return err
		}
	}

	// Read roles from file.
	roles, err := readRolesFromFile(rolesFilePath)
	if err != nil {
		slog.Error("Error reading roles from file", "error", err)
		return err
	}

	// Assign roles to service account
	slog.Info(fmt.Sprintf("Assigning roles to %s ", sa.Name))
	err = client.addRolesToServiceAccount(saName, roles)
	if err != nil {
		slog.Error("Error assigning roles to service account", "error", err)
		return err
	}

	// Create Service Account Key
	slog.Info(fmt.Sprintf("Creating service account key for %s", sa.Name))
	err = client.createServiceAccountKey(saName, credentialOutputPath)
	if err != nil {
		slog.Error("Error creating service account key", "error", err)
		return err
	}

	return nil
}

func VerifyServiceAccount(cliContext *cli.Context) error {
	ctx := context.Background()
	saName := cliContext.String("name")
	projectName := fmt.Sprintf("projects/%s", cliContext.String("project"))

	service, err := iam.NewService(ctx)
	if err != nil {
		slog.Error("Error creating client", "error", err)
		return fmt.Errorf("iam.NewService: %w", err)
	}
	resourceName := fmt.Sprintf(
		"projects/%s/serviceAccounts/%s",
		projectName,
		saName,
	)
	_, err = service.Projects.ServiceAccounts.Get(resourceName).Do()
	if err != nil {
		slog.Error("Could not find requested service account", "error", err)
		return fmt.Errorf("Projects.ServiceAccounts.Get: %w", err)
	}
	return nil
}

func CreateServiceAccountKey(cliContext *cli.Context) error {
	ctx := context.Background()
	saName := cliContext.String("name")
	projectName := cliContext.String("project")
	credentialOutputPath := cliContext.String("output-file")

	svc, err := iam.NewService(ctx)
	client := &IAMClient{
		service:   svc,
		projectId: projectName,
	}
	if err != nil {
		slog.Error("Error creating client", "error", err)
		return fmt.Errorf("iam.NewService: %w", err)
	}
	// Create Service Account Key
	slog.Info(fmt.Sprintf("Creating service account key for %s", saName))
	err = client.createServiceAccountKey(saName, credentialOutputPath)
	if err != nil {
		slog.Error("Error creating service account key", "error", err)
		return err
	}

	return nil
}

func readRolesFromFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var roles []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		roles = append(roles, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return roles, nil
}

const (
	// iamPropagationRetries bounds how long the role-binding step waits for
	// a freshly created service account to become visible to the resource
	// manager API — IAM changes propagate asynchronously.
	iamPropagationRetries = 5
	// iamPropagationBaseDelay is the first exponential backoff step between
	// binding attempts.
	iamPropagationBaseDelay = 2 * time.Second
)

// iamPropagationPolicy retries the binding with exponential backoff while
// LimitRetries caps the attempt count.
var iamPropagationPolicy = R.Monoid.Concat(
	R.LimitRetries(iamPropagationRetries),
	R.ExponentialBackoff(iamPropagationBaseDelay),
)

func (c *IAMClient) addRolesToServiceAccount(
	saName string,
	roles []string,
) error {
	ctx := context.Background()
	resourceManagerService, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return fmt.Errorf("cloudresourcemanager.NewService: %w", err)
	}
	action := bindRolesAction{
		client: c,
		rm:     resourceManagerService,
		saName: saName,
		roles:  roles,
	}
	result := IOE.Retrying(iamPropagationPolicy, action.Run, retryOnSAMissing)()
	return E.ToError(result)
}

// bindRolesAction is a re-runnable description of one role-binding attempt:
// fetch the project IAM policy, add the member to the listed roles, set the
// policy back. Re-fetching on every attempt keeps the ETag fresh.
type bindRolesAction struct {
	client *IAMClient
	rm     *cloudresourcemanager.Service
	saName string
	roles  []string
}

// Run executes one binding attempt; IOE.Retrying re-invokes it on
// propagation delay.
func (a bindRolesAction) Run(status R.RetryStatus) IOE.IOEither[error, *cloudresourcemanager.Policy] {
	if status.IterNumber > 0 {
		slog.Info(
			"service account not visible to IAM yet, retrying role binding",
			"attempt", status.IterNumber,
		)
	}
	return F.Pipe2(
		IOE.TryCatchError(a.fetchPolicy),
		IOE.Map[error](a.bindRoles),
		IOE.Chain(a.setPolicy),
	)
}

// fetchPolicy reads the current project IAM policy.
func (a bindRolesAction) fetchPolicy() (*cloudresourcemanager.Policy, error) {
	policy, err := a.rm.Projects.GetIamPolicy(
		a.client.projectId,
		&cloudresourcemanager.GetIamPolicyRequest{},
	).Do()
	if err != nil {
		return nil, fmt.Errorf(
			"resourceManagerService.Projects.GetIamPolicy: %w",
			err,
		)
	}
	return policy, nil
}

// bindRoles adds the service-account member to every listed role binding.
func (a bindRolesAction) bindRoles(
	policy *cloudresourcemanager.Policy,
) *cloudresourcemanager.Policy {
	member := fmt.Sprintf(
		"serviceAccount:%s@%s.iam.gserviceaccount.com",
		a.saName,
		a.client.projectId,
	)
	return a.client.updatePolicyBindings(policy, a.roles, member)
}

// setPolicy writes the updated policy back to the project.
func (a bindRolesAction) setPolicy(
	policy *cloudresourcemanager.Policy,
) IOE.IOEither[error, *cloudresourcemanager.Policy] {
	return IOE.TryCatchError(func() (*cloudresourcemanager.Policy, error) {
		return a.rm.Projects.SetIamPolicy(
			a.client.projectId,
			&cloudresourcemanager.SetIamPolicyRequest{Policy: policy},
		).Do()
	})
}

// isSAMissing reports whether the resource manager API rejected the
// binding because the service account is not visible yet — a propagation
// delay 400 that only affects freshly created service accounts. Any other
// failure must not be retried.
func isSAMissing(err error) bool {
	var gErr *googleapi.Error
	return errors.As(err, &gErr) &&
		gErr.Code == http.StatusBadRequest &&
		strings.Contains(gErr.Message, "Service account") &&
		strings.Contains(gErr.Message, "does not exist")
}

// retryOnSAMissing retries a binding attempt only on propagation delay;
// any other failure surfaces immediately.
var retryOnSAMissing = E.Fold(isSAMissing, F.Constant1[*cloudresourcemanager.Policy](false))

// updatePolicyBindings adds the service account member to the specified roles
func (c *IAMClient) updatePolicyBindings(
	policy *cloudresourcemanager.Policy,
	roles []string,
	member string,
) *cloudresourcemanager.Policy {
	for _, role := range roles {
		found := false
		// Iterate through existing bindings
		for _, binding := range policy.Bindings {
			if binding.Role == role {
				// Check if member already exists
				if !c.memberExistsInBinding(binding, member) {
					binding.Members = append(binding.Members, member)
				}
				found = true
				break
			}
		}

		// If role not found, create new binding
		if !found {
			policy.Bindings = append(
				policy.Bindings,
				&cloudresourcemanager.Binding{
					Role:    role,
					Members: []string{member},
				},
			)
		}
	}
	return policy
}

// memberExistsInBinding checks if a member already exists in a binding
func (c *IAMClient) memberExistsInBinding(
	binding *cloudresourcemanager.Binding,
	member string,
) bool {
	return slices.Contains(binding.Members, member)
}

func (c *IAMClient) createServiceAccountKey(sa, outputPath string) error {
	resourceName := fmt.Sprintf(
		"projects/%s/serviceAccounts/%s@%s.iam.gserviceAccount.com",
		c.projectId,
		sa, c.projectId,
	)
	request := &iam.CreateServiceAccountKeyRequest{
		PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE",
	}
	key, err := c.
		service.
		Projects.
		ServiceAccounts.
		Keys.
		Create(resourceName, request).
		Do()
	if err != nil {
		slog.Error("Error creating service account key", "error", err)
		return fmt.Errorf(
			"service.Projects.ServiceAccounts.Keys.Create: %w",
			err,
		)
	}
	// Decode the base64-encoded private key
	decodedKey, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		return fmt.Errorf("failed to decode private key: %v", err)
	}
	if err := os.WriteFile(outputPath, decodedKey, 0600); err != nil {
		slog.Error("Error writing to file", "error", err)
		return fmt.Errorf("error writing to file: %w", err)
	}
	slog.Info(fmt.Sprintf("Service account key written to %s", outputPath))
	return nil
}

func (c *IAMClient) createServiceAccount(
	name string,
	displayName string,
	description string,
) (*iam.ServiceAccount, error) {
	request := &iam.CreateServiceAccountRequest{
		AccountId: name,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: displayName,
			Description: description,
		},
	}

	serviceAccount, err := c.service.Projects.ServiceAccounts.Create(fmt.Sprintf("projects/%s", c.projectId), request).
		Do()
	if err != nil {
		return nil, fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}

	slog.Info("Service Account successfully created")
	return serviceAccount, nil
}

func (c *IAMClient) getServiceAccount(
	projectName string,
	saName string,
) *iam.ServiceAccount {
	resourceName := fmt.Sprintf(
		"projects/%s/serviceAccounts/%s@%s.iam.gserviceaccount.com",
		projectName,
		saName,
		projectName,
	)
	sa, _ := c.service.Projects.ServiceAccounts.Get(resourceName).Do()

	return sa
}
