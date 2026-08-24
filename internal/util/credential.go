package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
)

const gacPrefix = "GOOGLE_APPLICATION_CREDENTIALS="
const gacExportPrefix = "export GOOGLE_APPLICATION_CREDENTIALS="

// SetCredential updates or inserts the GOOGLE_APPLICATION_CREDENTIALS
// line in a per-cluster env file. Validates the key file exists before
// writing. Returns nil on success, error on failure.
func SetCredential(envFile, keyPath string) error {
	return F.Pipe4(
		resolveKeyPath(keyPath),
		IOE.Chain(checkKeyFile),
		IOE.Chain(readAndUpdateEnv(envFile)),
		IOE.Chain(writeEnvFile(envFile)),
		func(effect IOE.IOEither[error, string]) error {
			return E.Fold(
				F.Identity[error],
				func(string) error { return nil },
			)(effect())
		},
	)
}

// resolveKeyPath resolves the key path to an absolute path.
func resolveKeyPath(keyPath string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		abs, err := filepath.Abs(keyPath)
		if err != nil {
			return "", fmt.Errorf("resolve key path: %w", err)
		}
		return abs, nil
	})
}

// checkKeyFile validates that the key file exists and is readable.
func checkKeyFile(absKey string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		_, err := os.Stat(absKey)
		switch {
		case os.IsNotExist(err):
			return "", fmt.Errorf("key file not found: %s", absKey)
		case err != nil:
			return "", fmt.Errorf("cannot read key file: %w", err)
		default:
			return absKey, nil
		}
	})
}

// readAndUpdateEnv reads the env file and replaces or appends the credential line.
func readAndUpdateEnv(envFile string) func(string) IOE.IOEither[error, string] {
	return func(absKey string) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			content, err := os.ReadFile(envFile)
			switch {
			case os.IsNotExist(err):
				return "", fmt.Errorf("env file not found: %s", envFile)
			case err != nil:
				return "", fmt.Errorf("read env file: %w", err)
			}

			newLine := gacPrefix + absKey
			trimmed := strings.TrimRight(string(content), "\n")
			var lines []string
			if trimmed != "" {
				lines = strings.Split(trimmed, "\n")
			}

			replaced := false
			for i, line := range lines {
				field := strings.TrimSpace(line)
				if strings.HasPrefix(field, gacPrefix) || strings.HasPrefix(field, gacExportPrefix) {
					lines[i] = newLine
					replaced = true
					break
				}
			}
			if !replaced {
				lines = append(lines, newLine)
			}
			return strings.Join(lines, "\n") + "\n", nil
		})
	}
}

// writeEnvFile writes the updated content back to the env file.
func writeEnvFile(envFile string) func(string) IOE.IOEither[error, string] {
	return func(output string) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			if err := os.WriteFile(envFile, []byte(output), 0o600); err != nil {
				return "", fmt.Errorf("write env file: %w", err)
			}
			return output, nil
		})
	}
}
