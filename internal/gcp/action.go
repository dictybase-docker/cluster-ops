package gcp

import (
	"context"
	"log/slog"

	"github.com/urfave/cli/v2"
)

func RunAnalyzeRoles(cltx *cli.Context) error {
	projectID := cltx.String("project-id")
	serviceAccount := cltx.String("service-account")
	outputFile := cltx.String("output")
	credentialsFile := cltx.String("credentials")

	ctx := context.Background()

	iamService, rmService, err := initializeServices(ctx, credentialsFile)
	if err != nil {
		return err
	}

	result, err := performAnalysis(
		iamService,
		rmService,
		projectID,
		serviceAccount,
	)
	if err != nil {
		return err
	}

	if err := writeResultsToFile(outputFile, result); err != nil {
		return err
	}

	slog.Info("Analysis complete", "output_file", outputFile)

	return nil
}
