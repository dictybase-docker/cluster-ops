package backup

import (
	"log/slog"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"
)

func ArangoDBBackupAction(cltx *cli.Context) error {
	// Get the parameters from the CLI context
	user := cltx.String("user")
	password := cltx.String("password")
	server := cltx.String("server")
	output := cltx.String("output")
	repository := cltx.String("repository")
	resticPassword := cltx.String("restic-password")

	// Set RESTIC_PASSWORD environment variable
	if _, ok := os.LookupEnv("RESTIC_PASSWORD"); !ok {
		if len(resticPassword) > 0 {
			os.Setenv("RESTIC_PASSWORD", resticPassword)
		}
	}

	// Check if the repository exists
	checkCmd := exec.Command("restic", "-r", repository, "snapshots")
	err := checkCmd.Run()

	if err != nil {
		// Repository doesn't exist, so initialize it
		initCmd := exec.Command("restic", "-r", repository, "init")
		initOutput, err := initCmd.CombinedOutput()
		if err != nil {
			slog.Error(
				"Failed to initialize repository",
				"error",
				err,
				"output",
				string(initOutput),
			)
			return cli.Exit(err.Error(), 2)
		}
		slog.Info("Repository initialized successfully")
	} else {
		slog.Info("Repository already exists")
	}

	// Run arangodump command
	arangodumpCmd := exec.Command(
		"arangodump",
		"--all-databases",
		"--server.username", user,
		"--server.password", password,
		"--server.endpoint", server,
		"--output-directory", output,
	)

	arangodumpOutput, err := arangodumpCmd.CombinedOutput()
	if err != nil {
		slog.Error(
			"Failed to run arangodump",
			"error",
			err,
			"output",
			string(arangodumpOutput),
		)
		return cli.Exit(err.Error(), 2)
	}
	slog.Info("ArangoDB backup completed successfully")

	// Backup the output using restic
	resticBackupCmd := exec.Command(
		"restic",
		"-r", repository,
		"backup",
		output,
	)

	resticBackupOutput, err := resticBackupCmd.CombinedOutput()
	if err != nil {
		slog.Error(
			"Failed to backup to restic repository",
			"error",
			err,
			"output",
			string(resticBackupOutput),
		)
		return cli.Exit(err.Error(), 2)
	}
	slog.Info("Backup successfully uploaded to restic repository")

	return nil
}
