package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/urfave/cli/v2"
)

func initLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func newCustodianConfig(
	cliCtx *cli.Context,
	logger *slog.Logger,
) custodian.CustodianConfig {
	return custodian.CustodianConfig{
		KubeconfigPath: cliCtx.String("kubeconfig"),
		Namespace:      cliCtx.String("namespace"),
		Logger:         logger,
	}
}

func extractLogCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
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
			config := newCustodianConfig(cliCtx, logger)
			config.Label = cliCtx.String("label")
			cus, err := custodian.NewCustodian(config)
			if err != nil {
				return cli.Exit(err.Error(), 2)
			}
			if err := cus.SearchAndExtractLogs(cliCtx); err != nil {
				return cli.Exit(err.Error(), 2)
			}
			return nil
		},
	}
}

func excludeFromBackupCommand(logger *slog.Logger) *cli.Command {
	return &cli.Command{
		Name:  "exclude-from-backup",
		Usage: "Add 'velero.io/exclude-from-backup=true' label",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "namespace",
				Aliases: []string{"n"},
				Usage:   "Kubernetes namespace to search in",
				Value:   "dev",
			},
		},
		Action: func(cliCtx *cli.Context) error {
			config := newCustodianConfig(cliCtx, logger)
			cus, err := custodian.NewCustodian(config)
			if err != nil {
				return cli.Exit(err.Error(), 2)
			}
			if err := cus.ExcludeFromBackup(); err != nil {
				return cli.Exit(err.Error(), 2)
			}
			return nil
		},
	}
}

func main() {
	logger := initLogger()

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
			extractLogCommand(logger),
			excludeFromBackupCommand(logger),
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
}
