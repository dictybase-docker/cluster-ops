package gcp

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v2"
	iam "google.golang.org/api/iam/v1"
)

// createServiceAccount creates a service account.
func CreateServiceAccount(cliContext *cli.Context) (*iam.ServiceAccount, error) {
	ctx := context.Background()
  
	service, err := iam.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("iam.NewService: %w", err)
	}

	request := &iam.CreateServiceAccountRequest{
		AccountId: cliContext.String("sa-name"),
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: cliContext.String("sa-description"),
		},
	}
  projectName := fmt.Sprintf("projects/%s", cliContext.String("project"))

	account, err := service.Projects.ServiceAccounts.Create(projectName, request).Do()
	if err != nil {
		return nil, fmt.Errorf("Projects.ServiceAccounts.Create: %w", err)
	}
	return account, nil
}
