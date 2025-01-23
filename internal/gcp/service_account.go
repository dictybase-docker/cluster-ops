package gcp
import (
	"context"
	"fmt"
  "log/slog"

	"github.com/urfave/cli/v2"
	iam "google.golang.org/api/iam/v1"
)

// createServiceAccount creates a service account.
func CreateServiceAccount(cliContext *cli.Context) error {
	ctx := context.Background()
  saName := cliContext.String("name")
  saDisplayName := cliContext.String("display-name")
  saDescription := cliContext.String("description")
  projectName := cliContext.String("project")

  slog.Info(fmt.Sprintf("Creating service account %s in %s...", saName, projectName))
  
	service, err := iam.NewService(ctx)
	if err != nil {
    slog.Error("Error creating client", "error", err)
		return fmt.Errorf("iam.NewService: %w", err)
	}

	request := &iam.CreateServiceAccountRequest{
		AccountId: saName,
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: saDisplayName,
      Description: saDescription,
		},
	}

	_, err = service.Projects.ServiceAccounts.Create(fmt.Sprintf("projects/%s", projectName), request).Do()
	if err != nil {
    slog.Error("Error creating service account", "error", err)
		return fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}

  slog.Info("Service Account successfully created")
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
