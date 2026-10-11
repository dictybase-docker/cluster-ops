package pulumiauto

import (
	"context"
	"fmt"
	"strings"

	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// PreviewSummary counts plan changes.
type PreviewSummary struct {
	Creates, Updates, Deletes, Replaces, Same int
}

// SummaryFrom maps the typed ChangeSummary (apitype OpTypes — data keys).
func SummaryFrom(change map[apitype.OpType]int) PreviewSummary {
	return PreviewSummary{
		Creates:  change[apitype.OpCreate],
		Updates:  change[apitype.OpUpdate],
		Deletes:  change[apitype.OpDelete],
		Replaces: change[apitype.OpReplace],
		Same:     change[apitype.OpSame],
	}
}

// ProtectedChange is one blocked delete/replace on a protected type.
type ProtectedChange struct {
	URN, Type string
	Op        apitype.OpType
}

// GateResult is one preview: summary plus blocked changes.
type GateResult struct {
	Summary PreviewSummary
	Blocked []ProtectedChange
}

// defaultProtected lists the protected type tokens (substring match on
// Metadata.Type — data matching on typed metadata, never message text).
var defaultProtected = []string{
	"PersistentVolumeClaim",
	"StatefulSet",
	"Deployment",
	"Database",
}

// PreviewGate runs the preview and collects the event stream. It creates the
// channel, drains it in a collector goroutine until close, calls Preview,
// then closes the channel and waits for the collector — no leak, no deadlock.
func PreviewGate(
	ctx context.Context, s StackAPI, protected []string,
) IOE.IOEither[error, GateResult] {
	if len(protected) == 0 {
		protected = defaultProtected
	}
	return IOE.TryCatchError(func() (GateResult, error) {
		return runGate(ctx, s, protected)
	})
}

// runGate is the gate edge: preview + concurrent event collection.
func runGate(ctx context.Context, s StackAPI, protected []string) (GateResult, error) {
	ch := make(chan events.EngineEvent)
	collected := make(chan []ProtectedChange)
	go collectProtected(ch, collected, protected)
	res, err := s.Preview(ctx, ch)
	close(ch)
	blocked := <-collected
	if err != nil {
		return GateResult{}, fmt.Errorf("pulumi preview on %s: %w", s.Name(), err)
	}
	return GateResult{Summary: SummaryFrom(res.ChangeSummary), Blocked: blocked}, nil
}

// collectProtected drains the event channel until close.
func collectProtected(ch <-chan events.EngineEvent, out chan<- []ProtectedChange, protected []string) {
	blocked := []ProtectedChange{}
	for ev := range ch {
		if ev.ResourcePreEvent == nil {
			continue
		}
		meta := ev.ResourcePreEvent.Metadata
		if meta.Op != apitype.OpDelete && meta.Op != apitype.OpReplace {
			continue
		}
		if isProtected(meta.Type, protected) {
			blocked = append(blocked, ProtectedChange{URN: meta.URN, Type: meta.Type, Op: meta.Op})
		}
	}
	out <- blocked
}

func isProtected(typ string, protected []string) bool {
	for _, p := range protected {
		if strings.Contains(typ, p) {
			return true
		}
	}
	return false
}

// RunIfAllowed gates the Update step: it runs the preview first, refuses on
// protected changes unless AllowProtected is set, and never runs the step in
// dry-run mode.
func RunIfAllowed(
	ctx context.Context, s StackAPI, mode DeployMode,
	run func() IOE.IOEither[error, auto.UpResult],
) IOE.IOEither[error, auto.UpResult] {
	return F.Pipe1(
		PreviewGate(ctx, s, nil),
		IOE.Chain(ensureAllowed(mode, run)),
	)
}

func ensureAllowed(
	mode DeployMode, run func() IOE.IOEither[error, auto.UpResult],
) func(GateResult) IOE.IOEither[error, auto.UpResult] {
	return func(gate GateResult) IOE.IOEither[error, auto.UpResult] {
		if len(gate.Blocked) > 0 && !mode.AllowProtected {
			return IOE.Left[auto.UpResult](blockedError(gate.Blocked))
		}
		if mode.DryRun {
			return IOE.Of[error](auto.UpResult{})
		}
		return run()
	}
}

func blockedError(blocked []ProtectedChange) error {
	names := make([]string, 0, len(blocked))
	for _, b := range blocked {
		names = append(names, fmt.Sprintf("%s (%s)", b.Type, b.Op))
	}
	return fmt.Errorf("preview blocked: %d protected change(s): %s — re-run with --allow-protected to proceed",
		len(blocked), strings.Join(names, ", "))
}
