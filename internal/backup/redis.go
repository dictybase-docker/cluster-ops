package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/urfave/cli/v2"
)

func RedisBackupAction(cltx *cli.Context) error {
	host := cltx.String("host")
	repository := cltx.String("repository")
	resticPassword := cltx.String("restic-password")

	if err := setupResticPassword(resticPassword); err != nil {
		return err
	}

	if err := initializeResticRepository(repository); err != nil {
		return err
	}

	return performRedisBackup(host, repository)
}

func setupResticPassword(resticPassword string) error {
	if _, ok := os.LookupEnv("RESTIC_PASSWORD"); !ok &&
		len(resticPassword) > 0 {
		if err := os.Setenv("RESTIC_PASSWORD", resticPassword); err != nil {
			slog.Error(
				"Failed to set RESTIC_PASSWORD environment variable",
				"error",
				err,
			)
			return err
		}
	}
	return nil
}

func initializeResticRepository(repository string) error {
	cmd := exec.Command("restic", "-r", repository, "snapshots")
	if err := cmd.Run(); err != nil {
		cmd = exec.Command("restic", "-r", repository, "init")
		if err := cmd.Run(); err != nil {
			return cli.Exit(
				fmt.Sprintf("failed to initialize repository %s", err.Error()),
				2,
			)
		}
		slog.Info("Repository initialized successfully")
	} else {
		slog.Info("Repository already exists")
	}
	return nil
}

func validateAndSanitizeRepository(repository string) (string, error) {
	// Check if the repository path contains only allowed characters
	validPath := regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)
	if !validPath.MatchString(repository) {
		return "", fmt.Errorf("repository path contains invalid characters")
	}

	return repository, nil
}

func performRedisBackup(host, repository string) error {
	sanitizedRepo, err := validateAndSanitizeRepository(repository)
	if err != nil {
		return cli.Exit(fmt.Sprintf("Invalid repository path: %v", err), 2)
	}

	// Create a Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: host,
	})
	defer rdb.Close()

	ctx := context.Background()

	// Start BGSAVE
	if err := rdb.BgSave(ctx).Err(); err != nil {
		return cli.Exit(fmt.Sprintf("Failed to start BGSAVE: %v", err), 1)
	}

	// Wait for BGSAVE to complete
	for {
		info, err := rdb.Info(ctx, "persistence").Result()
		if err != nil {
			return cli.Exit(fmt.Sprintf("Failed to get Redis info: %v", err), 1)
		}
		if !strings.Contains(info, "rdb_bgsave_in_progress:1") {
			break
		}
		time.Sleep(time.Second)
	}

	// Run redis-cli --rdb and pipe to restic
	redisCli := exec.Command("redis-cli", "-h", host, "--rdb", "-")
	restic := exec.Command(
		"restic",
		"-r",
		sanitizedRepo,
		"backup",
		"--stdin",
		"--stdin-filename",
		"redis-backup.rdb",
		"--tag",
		"redis-backup",
	)

	restic.Stdin, err = redisCli.StdoutPipe()
	if err != nil {
		return cli.Exit(fmt.Sprintf("Failed to create pipe: %v", err), 1)
	}

	restic.Stdout = os.Stdout
	restic.Stderr = os.Stderr

	if err := restic.Start(); err != nil {
		return cli.Exit(fmt.Sprintf("Failed to start restic: %v", err), 1)
	}

	if err := redisCli.Run(); err != nil {
		return cli.Exit(fmt.Sprintf("Failed to run redis-cli: %v", err), 1)
	}

	if err := restic.Wait(); err != nil {
		return cli.Exit(
			fmt.Sprintf("Failed to complete restic backup: %v", err),
			1,
		)
	}

	slog.Info("Redis backup completed successfully")
	return nil
}
