package main

import (
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/gcp"
	"github.com/urfave/cli/v2"
)

var logger *slog.Logger

func init() {
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func main() {
	app := &cli.App{
		Name:     "gcp-tools",
		Usage:    "A collection of tools for GCP",
		Commands: getCommands(),
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error("Error running application", slog.Any("error", err))
		os.Exit(1)
	}
}

func getCommands() []*cli.Command {
	return []*cli.Command{
		analyzeRolesCommand(),
		findOrCreateKopsBucketCommand(),
		createKeyringAndKeyCommand(),
	}
}

func createKeyringAndKeyCommand() *cli.Command {
	return &cli.Command{
		Name:   "create-keyring-and-key",
		Usage:  "Create a keyring and key in Google Cloud KMS",
		Action: gcp.CreateKeyringAndKey,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "project-id",
				Aliases:  []string{"p"},
				Usage:    "Google Cloud project ID",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "keyring-name",
				Aliases:  []string{"k"},
				Usage:    "Name of the keyring to create",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "key-name",
				Aliases:  []string{"n"},
				Usage:    "Name of the key to create",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "location",
				Aliases: []string{"l"},
				Usage:   "Location for the keyring and key",
				Value:   "us-central1",
			},
			&cli.StringFlag{
				Name:    "credentials",
				Aliases: []string{"c"},
				Usage:   "Path to the Google Cloud credentials file (can also be set via GOOGLE_APPLICATION_CREDENTIALS env var)",
				EnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
			},
		},
	}
}

func analyzeRolesCommand() *cli.Command {
	return &cli.Command{
		Name:   "analyze-roles",
		Usage:  "Analyze GCP roles and permissions for a service account",
		Action: gcp.RunAnalyzeRoles,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "project-id",
				Aliases:  []string{"p"},
				Usage:    "GCP Project ID",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "service-account",
				Aliases:  []string{"s"},
				Usage:    "Service Account email",
				Required: true,
			},
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output file name",
				Value:   "role_analysis_output.txt",
			},
			&cli.StringFlag{
				Name:     "credentials",
				Aliases:  []string{"c"},
				Usage:    "Path to the GCP credentials file",
				Required: true,
			},
		},
	}
}

func findOrCreateKopsBucketCommand() *cli.Command {
	return &cli.Command{
		Name:   "find-or-create-kops-bucket",
		Usage:  "Find or Create a bucket for kops state storage on Google Cloud",
		Action: gcp.CreateKopsStateBucket,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "project",
				Aliases:  []string{"p"},
				Usage:    "Google Cloud project ID",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "bucket",
				Aliases:  []string{"b"},
				Usage:    "Name of the bucket to create",
				Required: true,
			},
			&cli.IntFlag{
				Name:    "max-versions",
				Aliases: []string{"m"},
				Usage:   "Maximum number of versions to keep for each object",
				Value:   6,
			},
			&cli.StringFlag{
				Name:  "region",
				Value: "US",
				Usage: "Region name for the bucket",
			},
		},
	}
}
