package main

import (
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/util"
	"github.com/urfave/cli/v2"
)

var logger *slog.Logger

func init() {
	logger = slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func main() {
	app := &cli.App{
		Name:     "cluster-ops-utils",
		Usage:    "miscellaneous tools for the cluster-ops project",
		Commands: getCommands(),
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error("Error running application", slog.Any("error", err))
		os.Exit(1)
	}
}

func getCommands() []*cli.Command {
	return []*cli.Command{
		setEnvironmentalVariableCommand(),
	}
}

func setEnvironmentalVariableCommand() *cli.Command {
	return &cli.Command{
		Name:   "set-env-var",
		Usage:  "set an enviromental variable in your .envrc file and load it",
		Action: util.SetEnvironmentalVariable,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "name",
				Aliases:  []string{"n"},
				Usage:    "Name of Enviromental Variable",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "value",
				Aliases:  []string{"v"},
				Usage:    "Google Cloud project ID",
				Required: true,
			},
		},
	}
}
