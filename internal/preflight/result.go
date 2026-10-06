package preflight

import (
	"fmt"
	"io"
	"os"

	IO "github.com/IBM/fp-go/v2/io"
)

// Status represents a check result status.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusInfo Status = "info"
)

// Result represents a single preflight check result.
type Result struct {
	Name   string
	Detail string
	Fix    string // Only for StatusFail
	Status Status
}

// Run executes all checks and returns their results.
// Never short-circuits — always runs every check.
func Run(checks []IO.IO[Result]) []Result {
	results := make([]Result, len(checks))
	for i, check := range checks {
		results[i] = check()
	}
	return results
}

// Print writes results to w in the same format as scripts/lib/check-helpers.sh.
// Honors CHECK_COLOR env var: "0" disables ANSI colors.
func Print(w io.Writer, results []Result) {
	useColor := os.Getenv("CHECK_COLOR") != "0"
	for _, r := range results {
		prefix := formatPrefix(r.Status, useColor)
		if r.Fix != "" {
			fmt.Fprintf(w, "%s%s %s — %s\n", prefix, r.Name, r.Detail, r.Fix)
		} else {
			fmt.Fprintf(w, "%s%s %s\n", prefix, r.Name, r.Detail)
		}
	}
}

// Failed reports whether any result has StatusFail.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

func formatPrefix(status Status, useColor bool) string {
	switch status {
	case StatusPass:
		if useColor {
			return "\033[32mPASS\033[0m  "
		}
		return "PASS  "
	case StatusFail:
		if useColor {
			return "\033[31mFAIL\033[0m  "
		}
		return "FAIL  "
	case StatusWarn:
		if useColor {
			return "\033[33mWARN\033[0m  "
		}
		return "WARN  "
	case StatusInfo:
		if useColor {
			return "\033[34mINFO\033[0m  "
		}
		return "INFO  "
	default:
		return "UNKN  "
	}
}
