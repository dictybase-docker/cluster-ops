package main

import (
	"log/slog"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/backup"
	cli "github.com/urfave/cli/v2"
)

func getArangoDBBackupCommand() *cli.Command {
	return &cli.Command{
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
				Name:    "server",
				Aliases: []string{"s"},
				Usage:   "ArangoDB server address",
				EnvVars: []string{"ARANGODB_SERVICE_HOST"},
				Value:   "arangodb",
			},
			&cli.IntFlag{
				Name:    "port",
				Usage:   "ArangoDB port",
				EnvVars: []string{"ARANGODB_SERVICE_PORT"},
				Value:   8529,
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
		Action: func(cCtx *cli.Context) error {
			return backup.ArangoDBBackupAction(cCtx, cCtx.Int("port"))
		},
	}
}

func getRedisBackupCommand() *cli.Command {
	return &cli.Command{
		Name:  "redis-backup",
		Usage: "Backup Redis database",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "host",
				Usage:    "Redis host address",
				Required: true,
			},
			&cli.IntFlag{
				Name:    "port",
				Usage:   "Redis port",
				EnvVars: []string{"REDIS_SERVICE_PORT"},
				Value:   6379,
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
	}
}

func setupApp() *cli.App {
	return &cli.App{
		Name:  "backup",
		Usage: "Backup tools for ArangoDB and Redis databases",
		Commands: []*cli.Command{
			getArangoDBBackupCommand(),
			getRedisBackupCommand(),
		},
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	app := setupApp()

	if err := app.Run(os.Args); err != nil {
		slog.Error("Error running application", "error", err)
		os.Exit(1)
	}
}
