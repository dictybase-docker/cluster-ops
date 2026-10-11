package pulumiauto

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
)

// Redactor masks secret config values in text. Masking is exact-value based
// (values come from SecretValues) — never keyword lists, never message
// matching. Empty values are ignored.
type Redactor struct{ secrets []string }

// Redact replaces every occurrence of each secret value with ***.
func (r Redactor) Redact(s string) string {
	for _, secret := range r.secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
}

// UpOptions governs one Update run. Logger nil = silent. Secrets feed the
// redactor. DryRun never reaches the update seam.
type UpOptions struct {
	DryRun  bool
	Logger  *slog.Logger
	Secrets []string
}

// UpOptions builds the run options from the mode.
func (m DeployMode) UpOptions(logger *slog.Logger, secrets []string) UpOptions {
	return UpOptions{DryRun: m.DryRun, Logger: logger, Secrets: secrets}
}

// Up runs the update with secret-safe event logging. It owns the event
// channel: create, drain in a collector goroutine until close, call Up, then
// close and wait — no leak, no deadlock. Raw engine output never reaches
// logs (the adapter discards ProgressStreams unconditionally); only
// whitelisted fields are logged, through the Redactor.
func Up(ctx context.Context, s StackAPI, opts UpOptions) IOE.IOEither[error, auto.UpResult] {
	if opts.DryRun {
		return IOE.Of[error](auto.UpResult{})
	}
	return IOE.TryCatchError(func() (auto.UpResult, error) {
		return runUp(ctx, s, opts)
	})
}

// runUp is the update edge: up + concurrent event collection.
func runUp(ctx context.Context, s StackAPI, opts UpOptions) (auto.UpResult, error) {
	ch := make(chan events.EngineEvent)
	done := make(chan struct{})
	go logEvents(ch, done, opts)
	res, err := s.Up(ctx, ch)
	close(ch)
	<-done
	if err != nil {
		return auto.UpResult{}, fmt.Errorf("pulumi up on %s: %w", s.Name(), err)
	}
	return res, nil
}

// logEvents drains the event channel and logs whitelisted fields only:
// step metadata (op, URN, type) at info and redacted diagnostics at debug.
func logEvents(ch <-chan events.EngineEvent, done chan<- struct{}, opts UpOptions) {
	defer close(done)
	redact := Redactor{secrets: opts.Secrets}
	log := opts.Logger
	if log == nil {
		for range ch {
			// drain only: silent runs still must consume the channel
		}
		return
	}
	for ev := range ch {
		if rp := ev.ResourcePreEvent; rp != nil {
			log.Info("resource step",
				"op", string(rp.Metadata.Op),
				"urn", redact.Redact(rp.Metadata.URN),
				"type", rp.Metadata.Type)
			continue
		}
		if d := ev.DiagnosticEvent; d != nil {
			log.Debug("pulumi diagnostic", "msg", redact.Redact(d.Message))
		}
	}
}
