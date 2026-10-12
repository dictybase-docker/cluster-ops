package pulumiauto

import (
	"context"
	"fmt"
	"os"
	"slices"
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

// SummaryFromResourceChanges converts the UpResult summary shape
// (*map[string]int) into PreviewSummary counts.
func SummaryFromResourceChanges(rc *map[string]int) PreviewSummary {
	if rc == nil {
		return PreviewSummary{}
	}
	change := map[apitype.OpType]int{}
	for k, v := range *rc {
		change[apitype.OpType(k)] = v
	}
	return SummaryFrom(change)
}

// ProtectedChange is one blocked delete/replace on a protected resource.
type ProtectedChange struct {
	URN, Type, Namespace string
	Op                   apitype.OpType
}

// ProtectionPolicy names what a protected change is: type tokens (substring
// match on the typed Type metadata) plus the namespaces whose Deployments
// count as protected (the database namespaces).
type ProtectionPolicy struct {
	Types            []string
	DeployNamespaces []string
}

// defaultProtectedTypes are the type tokens that always count as protected.
var defaultProtectedTypes = []string{
	"PersistentVolumeClaim",
	"StatefulSet",
	"Database",
}

// PolicyFrom builds the gate policy: Deployments are protected only inside
// the named namespaces (empty list = Deployments unprotected).
func PolicyFrom(mode DeployMode) ProtectionPolicy {
	return ProtectionPolicy{
		Types:            defaultProtectedTypes,
		DeployNamespaces: mode.ProtectDeployIn,
	}
}

// GateResult is one preview: summary, blocked changes and the saved plan
// file that binds this preview to its update.
type GateResult struct {
	Summary  PreviewSummary
	Blocked  []ProtectedChange
	PlanPath string
}

// PreviewGate runs the preview with a saved plan file and collects the
// event stream. The plan file is the reviewed artifact Up must apply: gate
// and update run the same plan (no TOCTOU on protected changes).
func PreviewGate(
	ctx context.Context, s StackAPI, policy ProtectionPolicy,
) IOE.IOEither[error, GateResult] {
	return IOE.TryCatchError(func() (GateResult, error) {
		return runGate(ctx, s, policy)
	})
}

// runGate is the gate edge: saved plan + preview + concurrent collection.
func runGate(ctx context.Context, s StackAPI, policy ProtectionPolicy) (GateResult, error) {
	plan, err := os.CreateTemp("", "pulumi-plan-*.json")
	if err != nil {
		return GateResult{}, fmt.Errorf("create plan file: %w", err)
	}
	planPath := plan.Name()
	plan.Close()
	ch := make(chan events.EngineEvent)
	collected := make(chan []ProtectedChange)
	go collectProtected(ch, collected, policy)
	res, err := s.Preview(ctx, ch, planPath)
	close(ch)
	blocked := <-collected
	if err != nil {
		CleanupPlan(planPath)
		return GateResult{}, fmt.Errorf("pulumi preview on %s: %w", s.Name(), err)
	}
	return GateResult{
		Summary: SummaryFrom(res.ChangeSummary), Blocked: blocked, PlanPath: planPath,
	}, nil
}

// CleanupPlan removes a saved plan file (best-effort).
func CleanupPlan(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// collectProtected drains the event channel until close.
func collectProtected(ch <-chan events.EngineEvent, out chan<- []ProtectedChange, policy ProtectionPolicy) {
	blocked := []ProtectedChange{}
	for ev := range ch {
		if ev.ResourcePreEvent == nil {
			continue
		}
		meta := ev.ResourcePreEvent.Metadata
		if meta.Op != apitype.OpDelete && meta.Op != apitype.OpReplace {
			continue
		}
		ns := resourceNamespace(meta)
		if isProtected(meta.Type, ns, policy) {
			blocked = append(blocked, ProtectedChange{
				URN: meta.URN, Type: meta.Type, Namespace: ns, Op: meta.Op,
			})
		}
	}
	out <- blocked
}

// resourceNamespace reads the namespace from the k8s resource ID
// ("namespace/name"); empty when unknown (conservative for Deployments).
func resourceNamespace(meta apitype.StepEventMetadata) string {
	id := meta.New.ID
	if id == "" {
		id = meta.Old.ID
	}
	ns, _, found := strings.Cut(id, "/")
	if !found {
		return ""
	}
	return ns
}

func isProtected(typ, namespace string, policy ProtectionPolicy) bool {
	for _, p := range policy.Types {
		if strings.Contains(typ, p) {
			return true
		}
	}
	if !strings.Contains(typ, "Deployment") {
		return false
	}
	return slices.Contains(policy.DeployNamespaces, namespace)
}

// UpdateOutcome reports a gated run: the preview summary plus whether the
// update actually ran (dry-run and no-op previews skip it).
type UpdateOutcome struct {
	Summary PreviewSummary
	Updated bool
}

// RunIfAllowed gates the Update step: preview first, refuse on protected
// changes unless AllowProtected, never run in dry-run mode, and pass the
// saved plan to the update so the applied plan is the reviewed plan.
func RunIfAllowed(
	ctx context.Context, s StackAPI, mode DeployMode,
	run func(planPath string) IOE.IOEither[error, auto.UpResult],
) IOE.IOEither[error, UpdateOutcome] {
	return F.Pipe1(
		PreviewGate(ctx, s, PolicyFrom(mode)),
		IOE.Chain(ensureAllowed(mode, run)),
	)
}

func ensureAllowed(
	mode DeployMode, run func(planPath string) IOE.IOEither[error, auto.UpResult],
) func(GateResult) IOE.IOEither[error, UpdateOutcome] {
	return func(gate GateResult) IOE.IOEither[error, UpdateOutcome] {
		if len(gate.Blocked) > 0 && !mode.AllowProtected {
			CleanupPlan(gate.PlanPath)
			return IOE.Left[UpdateOutcome](blockedError(gate.Blocked))
		}
		if mode.DryRun || zeroChanges(gate.Summary) {
			CleanupPlan(gate.PlanPath)
			return IOE.Of[error](UpdateOutcome{Summary: gate.Summary, Updated: false})
		}
		return F.Pipe1(run(gate.PlanPath), IOE.Map[error](toOutcome(gate)))
	}
}

func toOutcome(gate GateResult) func(auto.UpResult) UpdateOutcome {
	return func(auto.UpResult) UpdateOutcome {
		CleanupPlan(gate.PlanPath)
		return UpdateOutcome{Summary: gate.Summary, Updated: true}
	}
}

func zeroChanges(s PreviewSummary) bool {
	return s.Creates+s.Updates+s.Deletes+s.Replaces == 0
}

func blockedError(blocked []ProtectedChange) error {
	names := make([]string, 0, len(blocked))
	for _, b := range blocked {
		names = append(names, fmt.Sprintf("%s (%s)", b.Type, b.Op))
	}
	return fmt.Errorf(
		"preview blocked: %d protected change(s): %s — re-run with --allow-protected to proceed",
		len(blocked), strings.Join(names, ", "))
}
