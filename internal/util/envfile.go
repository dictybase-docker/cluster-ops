package util

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
)

// Change records a single environment variable update for reporting.
type Change struct {
	Key   string // variable name
	Old   string // previous line, empty when the variable was absent
	New   string // new line (KEY=value)
	Added bool   // true when the variable was appended rather than replaced
}

// envWriteInput carries the state between the pure transform and the atomic
// write effect.
type envWriteInput struct {
	path    string
	content string
	changes []Change
}

// UpdateEnvFile rewrites the owned variables in updates inside envFile.
//
// Behaviour:
//   - Replaces an existing `KEY=…` line, or a legacy `export KEY=…` line.
//   - Appends any owned variable that is not already present.
//   - Preserves all unrelated lines and comments verbatim.
//   - Rejects values containing newlines.
//   - Writes atomically via a temp file + rename.
//
// Returns the applied changes (old → new). The error type carries the
// failing step; on success the file on disk matches the returned content.
func UpdateEnvFile(envFile string, updates map[string]string) ([]Change, error) {
	for k, v := range updates {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("env var %s: value must not contain newlines", k)
		}
	}

	effect := F.Pipe2(
		readEnvFileLines(envFile),
		IOE.Map[error](transformEnv(envFile, updates)),
		IOE.Chain(writeEnvAtomic),
	)
	return E.UnwrapError(effect())
}

// readEnvFileLines reads the env file into a slice of lines, preserving
// ordering and blank lines.
func readEnvFileLines(envFile string) IOE.IOEither[error, []string] {
	return IOE.TryCatchError(func() ([]string, error) {
		content, err := os.ReadFile(envFile)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("env file not found: %s", envFile)
			}
			return nil, fmt.Errorf("read env file: %w", err)
		}
		trimmed := strings.TrimRight(string(content), "\n")
		if trimmed == "" {
			return []string{}, nil
		}
		return strings.Split(trimmed, "\n"), nil
	})
}

// transformEnv is a pure function that applies the updates to the lines and
// produces the new content plus the change report. envFile is closed over so
// the write effect knows the target path.
func transformEnv(envFile string, updates map[string]string) func([]string) envWriteInput {
	return func(lines []string) envWriteInput {
		keys := make([]string, 0, len(updates))
		for k := range updates {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		matched := make(map[string]bool, len(updates))
		out := make([]string, 0, len(lines)+len(updates))
		changes := make([]Change, 0, len(updates))

		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			replaced := false
			for _, k := range keys {
				if matched[k] {
					continue
				}
				if strings.HasPrefix(trimmed, k+"=") || strings.HasPrefix(trimmed, "export "+k+"=") {
					newLine := k + "=" + updates[k]
					out = append(out, newLine)
					changes = append(changes, Change{Key: k, Old: trimmed, New: newLine})
					matched[k] = true
					replaced = true
					break
				}
			}
			if !replaced {
				out = append(out, line)
			}
		}

		for _, k := range keys {
			if !matched[k] {
				newLine := k + "=" + updates[k]
				out = append(out, newLine)
				changes = append(changes, Change{Key: k, New: newLine, Added: true})
			}
		}

		return envWriteInput{
			path:    envFile,
			content: strings.Join(out, "\n") + "\n",
			changes: changes,
		}
	}
}

// writeEnvAtomic writes the content to the target path via a temp file and
// rename, so a crash never leaves a half-written env file.
func writeEnvAtomic(in envWriteInput) IOE.IOEither[error, []Change] {
	return IOE.TryCatchError(func() ([]Change, error) {
		dir := filepath.Dir(in.path)
		tmp, err := os.CreateTemp(dir, ".env-sync-*")
		if err != nil {
			return nil, fmt.Errorf("create temp file: %w", err)
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName) // no-op after a successful rename

		writeErr := writeTemp(tmp, in.content)
		closeErr := tmp.Close()
		if writeErr != nil {
			return nil, writeErr
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close temp file: %w", closeErr)
		}
		if err := os.Rename(tmpName, in.path); err != nil {
			return nil, fmt.Errorf("replace env file: %w", err)
		}
		return in.changes, nil
	})
}

// writeTemp writes the content to the open temp file with fixed permissions.
func writeTemp(tmp *os.File, content string) error {
	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	return nil
}
