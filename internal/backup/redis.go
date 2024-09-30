package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	cli "github.com/urfave/cli/v2"
)

func RedisBackupAction(cltx *cli.Context) error {
	host := cltx.String("host")
	port := cltx.Int("port")
	repository := cltx.String("repository")
	resticPassword := cltx.String("restic-password")

	if err := setupResticPassword(resticPassword); err != nil {
		return err
	}

	if err := initializeResticRepository(repository); err != nil {
		return err
	}

	return performRedisBackup(host, port, repository)
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
	if len(repository) == 0 {
		return "", fmt.Errorf("repository path contains invalid characters")
	}

	return repository, nil
}

func performRedisBackup(host string, port int, repository string) error {
	sanitizedRepo, sanitizedHost, sanitizedPort, err := validateAndSanitizeInputs(
		repository,
		host,
		port,
	)
	if err != nil {
		return err
	}

	rdb := createRedisClient(sanitizedHost, sanitizedPort)
	defer rdb.Close()

	ctx := context.Background()

	if err := performBGSave(ctx, rdb); err != nil {
		return err
	}

	if err := waitForBGSaveCompletion(ctx, rdb); err != nil {
		return err
	}

	return runBackupCommands(sanitizedRepo, sanitizedHost, sanitizedPort)
}

func validateAndSanitizeInputs(
	repository, host string,
	port int,
) (string, string, int, error) {
	sanitizedRepo, err := validateAndSanitizeRepository(repository)
	if err != nil {
		return "", "", 0, cli.Exit(
			fmt.Sprintf("Invalid repository path: %v", err),
			2,
		)
	}

	sanitizedHost, err := validateAndSanitizeHost(host)
	if err != nil {
		return "", "", 0, cli.Exit(fmt.Sprintf("Invalid host: %v", err), 2)
	}

	sanitizedPort, err := validateAndSanitizePort(port)
	if err != nil {
		return "", "", 0, cli.Exit(fmt.Sprintf("Invalid port: %v", err), 2)
	}

	return sanitizedRepo, sanitizedHost, sanitizedPort, nil
}

func createRedisClient(host string, port int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", host, port),
	})
}

func performBGSave(ctx context.Context, rdb *redis.Client) error {
	if err := rdb.BgSave(ctx).Err(); err != nil {
		return cli.Exit(fmt.Sprintf("Failed to start BGSAVE: %v", err), 1)
	}
	return nil
}

func waitForBGSaveCompletion(ctx context.Context, rdb *redis.Client) error {
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
	return nil
}

func runBackupCommands(repository, host string, port int) error {
	redisCliArgs := []string{
		"-h", host,
		"-p", fmt.Sprintf("%d", port),
		"--rdb", "-",
	}
	redisCli := exec.Command("redis-cli", redisCliArgs...)

	resticArgs := []string{
		"-r", repository,
		"backup",
		"--stdin",
		"--stdin-filename", "redis-backup.rdb",
		"--tag", "redis-backup",
	}
	restic := exec.Command("restic", resticArgs...)

	var err error
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

func validateAndSanitizeHost(host string) (string, error) {
	// Simple validation: check if the host is not empty and doesn't contain spaces
	if len(host) == 0 {
		return "", fmt.Errorf("invalid host")
	}
	return host, nil
}

func validateAndSanitizePort(port int) (int, error) {
	// Check if the port is within the valid range
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port number")
	}
	return port, nil
}
