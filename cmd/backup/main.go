package main

import (
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/backup"
	"github.com/urfave/cli/v2"
)

func main() {
	// Initialize the logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	app := &cli.App{
		Name:  "backup",
		Usage: "Backup tools for ArangoDB and Redis databases",
		Commands: []*cli.Command{
			{
				Name:  "arangodb-backup",
				Usage: "Backup ArangoDB database",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "user",
						Aliases:  []string{"u"},
						Usage:    "ArangoDB username",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "password",
						Aliases:  []string{"p"},
						Usage:    "ArangoDB password",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "server",
						Aliases:  []string{"s"},
						Usage:    "ArangoDB server address",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "output",
						Aliases:  []string{"o"},
						Usage:    "Output folder for backup",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "repository",
						Aliases:  []string{"r"},
						Usage:    "GCS location of restic repository",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "restic-password",
						Usage:   "Restic repository password (reads from RESTIC_PASSWORD env var if not provided)",
						EnvVars: []string{"RESTIC_PASSWORD"},
					},
				},
				Action: backup.ArangoDBBackupAction,
			},
			{
				Name:  "redis-backup",
				Usage: "Backup Redis database",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "host",
						Usage:    "Redis host address",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "repository",
						Aliases:  []string{"r"},
						Usage:    "GCS location of restic repository",
						Required: true,
					},
					&cli.StringFlag{
						Name:    "restic-password",
						Usage:   "Restic repository password (reads from RESTIC_PASSWORD env var if not provided)",
						EnvVars: []string{"RESTIC_PASSWORD"},
					},
				},
				Action: backup.RedisBackupAction,
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		slog.Error("Error running application", "error", err)
		os.Exit(1)
	}
}
