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
  projectName := fmt.Sprintf("projects/%s", cliContext.String("project"))

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

	_, err = service.Projects.ServiceAccounts.Create(projectName, request).Do()
	if err != nil {
    slog.Error("Error creating service account", "error", err)
		return fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}

  slog.Info("Service Account successfully created")
	return nil
}
