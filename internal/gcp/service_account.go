package gcp

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/urfave/cli/v2"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
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
	client := &IAMClient{
		service:   svc,
		projectId: projectName,
	}
	if err != nil {
		slog.Error("Error creating service account", "error", err)
		return err
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

func (c *IAMClient) addRolesToServiceAccount(
	saName string,
	roles []string,
) error {
	// Create Resource Manager service for project-level IAM operations
	ctx := context.Background()
	resourceManagerService, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return fmt.Errorf("cloudresourcemanager.NewService: %w", err)
	}
	// Get IAM Policy for the project
	policy, err := c.getProjectIAMPolicy(resourceManagerService)
	if err != nil {
		return err
	}

	// Create the service account member string
	member := fmt.Sprintf(
		"serviceAccount:%s@%s.iam.gserviceaccount.com",
		saName,
		c.projectId,
	)

	// Update policy with new role bindings
	policy = c.updatePolicyBindings(policy, roles, member)

	// Set the updated IAM policy
	err = c.setProjectIAMPolicy(resourceManagerService, policy)
	if err != nil {
		return err
	}

	return nil
}

// getProjectIAMPolicy retrieves the current IAM policy for the project
func (c *IAMClient) getProjectIAMPolicy(
	rmService *cloudresourcemanager.Service,
) (*cloudresourcemanager.Policy, error) {
	policy, err := rmService.Projects.GetIamPolicy(c.projectId,
		&cloudresourcemanager.GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, fmt.Errorf(
			"resourceManagerService.Projects.GetIamPolicy: %w",
			err,
		)
	}
	return policy, nil
}

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

// setProjectIAMPolicy applies the updated policy to the project
func (c *IAMClient) setProjectIAMPolicy(
	rmService *cloudresourcemanager.Service,
	policy *cloudresourcemanager.Policy,
) error {
	setIamPolicyRequest := &cloudresourcemanager.SetIamPolicyRequest{
		Policy: policy,
	}

	_, err := rmService.Projects.SetIamPolicy(c.projectId, setIamPolicyRequest).
		Do()
	if err != nil {
		slog.Error("Error setting IAM policy", "error", err)
		return fmt.Errorf(
			"resourceManagerService.Projects.SetIamPolicy: %w",
			err,
		)
	}
	return nil
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
