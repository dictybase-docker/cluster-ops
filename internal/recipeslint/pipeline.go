package recipeslint

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
)

// Config is the boundary input: the directory holding the justfile to lint.
type Config struct {
	Root string
}

// work carries the pipeline intermediates on a state struct.
type work struct {
	Cfg Config
}

// readDump runs `just --dump --dump-format json` against the target justfile.
// Only the raw call lives in TryCatchError; the failure is wrapped with the
// target path via MapLeft.
func readDump(w work) IOE.IOEither[error, []byte] {
	justfile := filepath.Join(w.Cfg.Root, "justfile")
	return F.Pipe1(
		IOE.TryCatchError(func() ([]byte, error) {
			return exec.Command(
				"just", "--justfile", justfile, "--dump", "--dump-format", "json",
			).Output()
		}),
		IOE.MapLeft[[]byte](func(err error) error {
			return fmt.Errorf("just --dump failed for %s: %w", justfile, err)
		}),
	)
}

// parseAndCheck unmarshals the dump and walks every recipe body.
func parseAndCheck(data []byte) IOE.IOEither[error, []Finding] {
	return F.Pipe1(
		IOE.TryCatchError(func() (dump, error) {
			var d dump
			if err := json.Unmarshal(data, &d); err != nil {
				return dump{}, fmt.Errorf("parsing just dump: %w", err)
			}
			return d, nil
		}),
		IOE.Map[error](Walk),
	)
}

// report prints findings and fails the pipeline when fail-rule findings
// exist. Warn findings never flip the outcome.
func report(findings []Finding) E.Either[error, struct{}] {
	var fails, warns []Finding
	for _, f := range findings {
		if f.warn() {
			warns = append(warns, f)
			continue
		}
		fails = append(fails, f)
	}
	for _, f := range fails {
		detail := f.Detail
		if len(detail) > 120 {
			detail = detail[:120]
		}
		fmt.Printf("FAIL %s:%d  %s: %s\n", f.Recipe, f.Line, f.Rule, detail)
	}
	for _, f := range warns {
		fmt.Printf("WARN %s  %s: %s\n", f.Recipe, f.Rule, f.Detail)
	}
	if len(fails) > 0 {
		return E.Left[struct{}](fmt.Errorf("lint-recipes: %d finding(s)", len(fails)))
	}
	if len(warns) == 0 {
		fmt.Println("lint-recipes: no recipe findings")
	}
	return E.Right[error](struct{}{})
}

// Run lints the justfile in cfg.Root, prints findings, and returns an error
// when any fail-rule finding exists. The full pipeline stays inline here —
// no one-step handoff to a named continuation.
func Run(cfg Config) error {
	outcome := F.Pipe3(
		work{Cfg: cfg},
		readDump,
		IOE.Chain(parseAndCheck),
		IOE.ChainEitherK(report),
	)
	return E.ToError(outcome())
}
