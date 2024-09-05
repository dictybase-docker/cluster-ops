package main

import (
	"log/slog"
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	// Initialize slog
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	app := &cli.App{
		Name:  "gcp-role-analyzer",
		Usage: "Analyze GCP roles and permissions for a service account",
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
		Action: analyzeRoles,
	}

	if err := app.Run(os.Args); err != nil {
		slog.Error("Error running application", "error", err)
		os.Exit(1)
	}
}
