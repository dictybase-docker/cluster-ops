package gcp
import (
	"context"
	"fmt"
  "log/slog"

	"github.com/urfave/cli/v2"
	iam "google.golang.org/api/iam/v1"
)

type IAMClient struct {
  service *iam.Service
  projectId string
}

// createServiceAccount creates a service account.
func CreateServiceAccount(cliContext *cli.Context) error {
  saName := cliContext.String("name")
  saDisplayName := cliContext.String("display-name")
  saDescription := cliContext.String("description")
  projectName := cliContext.String("project")
  rolesFilePath := cliContext.String("roles-path")
  credentialOutputPath := cliContext.String("output-path")
  
  // Create Service 
  ctx := context.Background()
  newService, err := iam.NewService(ctx)
  client := &IAMClient{
    service: newService,
    projectId: projectName,
  }

  // Create Service Account
  slog.Info(fmt.Sprintf("Creating service account %s in %s...", saName, client.projectId))
  sa, err := client.createServiceAccount(saName, projectName, saDisplayName, saDescription)
	if err != nil {
    slog.Error("Error creating service account", "error", err)
		return err
	}

  // Read roles from file

  slog.Info(fmt.Sprintf("Assign roles to service account %s in %s...", saName, client.projectId))
  // err = client.addRolesToServiceAccount(sa.Name) 

  // Output credentials

  return nil
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
	newBindings := iamPolicy.Bindings
	for _, role := range roles {
		found := false
		for _, binding := range newBindings {
			if binding.Role == role {
				found = true
				break
			}
		}
		if found {
			newBindings = append(newBindings, &iam.Binding{
				Role: role,
				Members: []string{
					fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", saName, c.projectId),
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

func (c *IAMClient) createServiceAccount(name string, project string, displayName string, description string) (*iam.ServiceAccount, error) {
	request := &iam.CreateServiceAccountRequest{
		AccountId: name,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: displayName,
      Description: description,
		},
	}

  serviceAccount, err := c.service.Projects.ServiceAccounts.Create(fmt.Sprintf("projects/%s", project), request).Do()
	if err != nil {
		return nil, fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}

  slog.Info("Service Account successfully created")
	return serviceAccount, nil
}
