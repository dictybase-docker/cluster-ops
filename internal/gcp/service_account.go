package gcp

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v2"
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

	// Create Service Account
	slog.Info(fmt.Sprintf("Creating service account %s in %s...", saName, client.projectId))
	sa, err := client.createServiceAccount(saName, saDisplayName, saDescription)
	if err != nil {
		slog.Error("Error creating service account", "error", err)
		return err
	}

	// Read roles from file
	roles, err := readRolesFromFile(rolesFilePath)
	if err != nil {
		slog.Error("Error reading roles from file", "error", err)
		return err
	}

	// Assign roles to service account
	slog.Info(fmt.Sprintf("Assigning roles to %s ", sa.Name))
	err = client.addRolesToServiceAccount(sa.Name, roles)
	if err != nil {
		slog.Error("Error assigning roles to service account", "error", err)
		return err
	}

	// Create Service Account Key
	if credentialOutputPath != "" {
		slog.Info(fmt.Sprintf("Creating service account key for %s", sa.Name))
		err = client.createServiceAccountKey(sa.Email, credentialOutputPath)
		if err != nil {
			slog.Error("Error creating service account key", "error", err)
			return err
		}
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
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectName, saName)
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
	// Create Service Account Key
	if credentialOutputPath != "" {
		slog.Info(fmt.Sprintf("Creating service account key for %s", saName))
		err = client.createServiceAccountKey(saName, credentialOutputPath)
		if err != nil {
			slog.Error("Error creating service account key", "error", err)
			return err
		}
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

func (c *IAMClient) addRolesToServiceAccount(saName string, roles []string) error {
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", c.projectId, saName)

	// Get IAM Policy
	iamPolicy, err := c.service.Projects.ServiceAccounts.GetIamPolicy(resourceName).Do()
	if err != nil {
		slog.Error("Error fetching IAM policy", "error", err)
		return fmt.Errorf("service.Projects.ServiceAccounts.GetIamPolicy: %w", err)
	}
	// Edit IAM Policy
	// Initialize new bindings with existing bindings.
	newBindings := iamPolicy.Bindings
	// Check desired roles slice for collision with existing bindings.
	for _, role := range roles {
		found := false
		// Iterate through existing bindings.
		for _, binding := range newBindings {
			// If the role is already in the existing bindings, do nothing.
			if binding.Role == role {
				found = true
				break
			}
		}
		// If the role is not in the existing bindings, create a binding and append it.
		if !found {
			newBindings = append(newBindings, &iam.Binding{
				Role: role,
				Members: []string{
					fmt.Sprintf(
						"serviceAccount:%s@%s.iam.gserviceaccount.com",
						saName,
						c.projectId,
					),
				},
			})
		}
	}
	// Set IAM Policy
	iamPolicy.Bindings = newBindings
	request := iam.SetIamPolicyRequest{
		Policy: iamPolicy,
	}
	_, err = c.service.Projects.ServiceAccounts.SetIamPolicy(resourceName, &request).Do()
	if err != nil {
		slog.Error("Error setting IAM policy", "error", err)
		return fmt.Errorf("service.Projects.ServiceAccounts.SetIamPolicy: %w", err)
	}

	return nil
}

func (c *IAMClient) createServiceAccountKey(saEmail, outputPath string) error {
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", c.projectId, saEmail)
	request := &iam.CreateServiceAccountKeyRequest{}
	key, err := c.service.Projects.ServiceAccounts.Keys.Create(resourceName, request).Do()
	if err != nil {
		slog.Error("Error creating service account key", "error", err)
		return fmt.Errorf("service.Projects.ServiceAccounts.Keys.Create: %w", err)
	}

	// Write key to file
	file, err := os.Create(outputPath)
	if err != nil {
		slog.Error("Error creating file", "error", err)
		return fmt.Errorf("os.Create: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(key.PrivateKeyData)
	if err != nil {
		slog.Error("Error writing to file", "error", err)
		return fmt.Errorf("file.WriteString: %w", err)
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
