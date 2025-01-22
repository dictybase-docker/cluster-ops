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
  slog.Info("Creating Service Account...")
  
	service, err := iam.NewService(ctx)
	if err != nil {
		return fmt.Errorf("iam.NewService: %w", err)
	}

	request := &iam.CreateServiceAccountRequest{
		AccountId: cliContext.String("sa-name"),
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: cliContext.String("sa-description"),
		},
	}

  projectName := fmt.Sprintf("projects/%s", cliContext.String("project"))
	_, err = service.Projects.ServiceAccounts.Create(projectName, request).Do()
	if err != nil {
		return fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}

  slog.Info("Service Account successfully created")
	return nil
}
