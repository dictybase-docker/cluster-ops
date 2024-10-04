package main

import (
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/urfave/cli/v2"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	app := &cli.App{
		Name:  "custodian",
		Usage: "Kubernetes cluster management tool",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "kubeconfig",
				Aliases: []string{"k"},
				Usage:   "Path to the kubeconfig file",
				EnvVars: []string{"KUBECONFIG"},
				Value:   "",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "extract-log",
				Usage: "Search for Kubernetes jobs and extract logs",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "label",
						Aliases:  []string{"l"},
						Usage:    "Kubernetes label to search for",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "namespace",
						Aliases: []string{"n"},
						Usage:   "Kubernetes namespace to search in",
						Value:   "dev",
					},
				},
				Action: func(cliCtx *cli.Context) error {
					config := custodian.CustodianConfig{
						KubeconfigPath: cliCtx.String("kubeconfig"),
						Namespace:      cliCtx.String("namespace"),
						Label:          cliCtx.String("label"),
						Logger:         logger,
					}
					cus, err := custodian.NewCustodian(config)
					if err != nil {
						return err
					}
					return cus.SearchAndExtractLogs(cliCtx)
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logger.Error("Application error", "error", err)
		os.Exit(1)
	}
}
