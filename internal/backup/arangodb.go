package backup

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"
)

func ArangoDBBackupAction(cltx *cli.Context) error {
	config := extractConfig(cltx)

	if err := setResticPassword(config.ResticPassword); err != nil {
		return cli.Exit(err.Error(), 2)
	}

	if err := ensureRepositoryExists(config.Repository); err != nil {
		return cli.Exit(err.Error(), 2)
	}

	if err := runArangoDump(config); err != nil {
		return cli.Exit(err.Error(), 2)
	}

	if err := backupToRestic(config.Repository, config.Output); err != nil {
		return cli.Exit(err.Error(), 2)
	}

	return nil
}

type arangoDBConfig struct {
	User           string
	Password       string
	Server         string
	Output         string
	Repository     string
	ResticPassword string
}

func extractConfig(cltx *cli.Context) arangoDBConfig {
	return arangoDBConfig{
		User:           cltx.String("user"),
		Password:       cltx.String("password"),
		Server:         cltx.String("server"),
		Output:         cltx.String("output"),
		Repository:     cltx.String("repository"),
		ResticPassword: cltx.String("restic-password"),
	}
}

func setResticPassword(password string) error {
	if _, ok := os.LookupEnv("RESTIC_PASSWORD"); !ok && len(password) > 0 {
		return os.Setenv("RESTIC_PASSWORD", password)
	}
	return nil
}

func ensureRepositoryExists(repository string) error {
	checkCmd := exec.Command("restic", "-r", repository, "snapshots")
	if err := checkCmd.Run(); err != nil {
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
			return err
		}
		slog.Info("Repository initialized successfully")
	} else {
		slog.Info("Repository already exists")
	}
	return nil
}

func runArangoDump(config arangoDBConfig) error {
	if err := validateConfig(config); err != nil {
		return err
	}

	args := buildArangoDumpArgs(config)
	cmd := exec.Command("arangodump", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error(
			"Failed to run arangodump",
			"error",
			err,
			"output",
			string(output),
		)
		return err
	}
	slog.Info("ArangoDB backup completed successfully")
	return nil
}

func validateConfig(config arangoDBConfig) error {
	if config.User == "" || config.Password == "" || config.Server == "" ||
		config.Output == "" {
		return fmt.Errorf("invalid configuration: all fields must be non-empty")
	}
	return nil
}

func buildArangoDumpArgs(config arangoDBConfig) []string {
	return []string{
		"--all-databases",
		"--server.username", config.User,
		"--server.password", config.Password,
		"--server.endpoint", config.Server,
		"--output-directory", config.Output,
	}
}

func backupToRestic(repository, output string) error {
	cmd := exec.Command("restic", "-r", repository, "backup", output)
	backupOutput, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error(
			"Failed to backup to restic repository",
			"error",
			err,
			"output",
			string(backupOutput),
		)
		return err
	}
	slog.Info("Backup successfully uploaded to restic repository")
	return nil
}
