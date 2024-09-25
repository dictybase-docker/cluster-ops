package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/gcp"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "gcp-tools",
		Usage: "A collection of tools for GCP",
		Commands: []*cli.Command{
			{
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
			},
			{
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
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error running application %s", err)
		os.Exit(1)
	}
}
