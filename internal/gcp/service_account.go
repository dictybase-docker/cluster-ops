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
    if err != nil {
        slog.Error("Error creating client", "error", err)
        return err
    }

    // Create Service Account
    slog.Info(fmt.Sprintf("Creating service account %s in %s...", saName, projectName))
    sa, err := createServiceAccount(newService, saName, projectName, saDisplayName, saDescription)
    if err != nil {
        slog.Error("Error creating service account", "error", err)
        return err
    }

    // Assign Roles
    err = assignRoles(newService, sa.Email, projectName, rolesFilePath)
    if err != nil {
        slog.Error("Error assigning roles", "error", err)
        return err
    }

    // Output credentials
    err = createServiceAccountKey(newService, sa.Email, projectName, credentialOutputPath)
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
    resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectName, saName)
    _, err = service.Projects.ServiceAccounts.Get(resourceName).Do()
    if err != nil {
        slog.Error("Could not find requested service account", "error", err)
        return fmt.Errorf("Projects.ServiceAccounts.Get: %w", err)
    }
    return nil
}

func createServiceAccount(service *iam.Service, name string, project string, displayName string, description string) (*iam.ServiceAccount, error) {
    request := &iam.CreateServiceAccountRequest{
        AccountId: name,
        ServiceAccount: &iam.ServiceAccount{
            DisplayName: displayName,
            Description: description,
        },
    }

    serviceAccount, err := service.Projects.ServiceAccounts.Create(fmt.Sprintf("projects/%s", project), request).Do()
    if err != nil {
        return nil, fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
    }

    slog.Info("Service Account successfully created")
    return serviceAccount, nil
}

func assignRoles(service *iam.Service, serviceAccountEmail, projectName, rolesFilePath string) error {
    // Code to assign roles to the service account
    // You can read the roles from the rolesFilePath and use the iam.Service.Projects.ServiceAccounts.SetIamPolicy method
    return nil
}

func createServiceAccountKey(service *iam.Service, serviceAccountEmail, projectName, credentialOutputPath string) error {
    // Code to create a service account key and write it to the credentialOutputPath
    // You can use the iam.Service.Projects.ServiceAccounts.Keys.Create method
    return nil
}
