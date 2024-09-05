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
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error running application %s", err)
		os.Exit(1)
	}
}
