package pulumiauto

import (
	"context"
	"fmt"
	"log/slog"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// imageTagKey is the config path key the deploy flows write (the recipes'
// `config set --path --plaintext properties.image.tag`).
const imageTagKey = "properties.image.tag"

// ServiceSpec describes one service deploy.
type ServiceSpec struct {
	App                     string // services.yaml app name (csv filter key)
	Folder, Stack, ImageTag string
	Preflight               PreflightRunner
	Rollout                 RolloutWaiter
	Logger                  *slog.Logger // nil = silent up events
}

// BootstrapResult reports one composite run.
type BootstrapResult struct {
	Summary PreviewSummary
	Rollout string
}

type bootstrapWork struct {
	opener StackOpener
	target DeployTarget
	svc    ServiceSpec
	mode   DeployMode
}

// BootstrapService runs the full service gate in order: preflight → open
// stack → set image tag → preview + protected gate → up → rollout wait. A
// failing preflight stops the flow before the stack is touched; a protected
// gate stops it before up; a no-op preview skips up and reports unchanged.
func BootstrapService(
	ctx context.Context, opener StackOpener, t DeployTarget, svc ServiceSpec, mode DeployMode,
) IOE.IOEither[error, BootstrapResult] {
	w := bootstrapWork{opener: opener, target: t, svc: svc, mode: mode}
	return F.Pipe1(w.runPreflight(), IOE.Chain(w.openAndDeploy(ctx)))
}

// runPreflight runs the gate checks and fails the flow when any of them fail.
func (w bootstrapWork) runPreflight() IOE.IOEither[error, struct{}] {
	return F.Pipe1(
		IOE.TryCatchError(func() ([]preflight.Result, error) { return w.svc.Preflight(), nil }),
		IOE.ChainEitherK(requirePreflight),
	)
}

// requirePreflight turns gate results into the flow verdict (named, pure).
func requirePreflight(results []preflight.Result) E.Either[error, struct{}] {
	if preflight.Failed(results) {
		return E.Left[struct{}](fmt.Errorf(
			"preflight failed: %d check(s) — fix the FAIL lines above", countFails(results)))
	}
	return E.Of[error](struct{}{})
}

func countFails(results []preflight.Result) int {
	n := 0
	for _, r := range results {
		if r.Status == preflight.StatusFail {
			n++
		}
	}
	return n
}

func (w bootstrapWork) openAndDeploy(ctx context.Context) func(struct{}) IOE.IOEither[error, BootstrapResult] {
	return func(struct{}) IOE.IOEither[error, BootstrapResult] {
		s, err := OpenStack(ctx, w.opener, w.target, true)
		if err != nil {
			return IOE.Left[BootstrapResult](err)
		}
		return w.deploy(ctx, s)
	}
}

func (w bootstrapWork) deploy(ctx context.Context, s StackAPI) IOE.IOEither[error, BootstrapResult] {
	return F.Pipe1(
		SetConfig(ctx, s, imageTagKey, w.svc.ImageTag, false),
		IOE.Chain(w.gateAndUpdate(ctx, s)),
	)
}

func (w bootstrapWork) gateAndUpdate(
	ctx context.Context, s StackAPI,
) func(struct{}) IOE.IOEither[error, BootstrapResult] {
	return func(struct{}) IOE.IOEither[error, BootstrapResult] {
		return F.Pipe1(
			PreviewGate(ctx, s, PolicyFrom(w.mode)),
			IOE.Chain(w.fromGate(ctx, s)),
		)
	}
}

// fromGate applies the gate: block on protected changes, skip up on dry-run
// or a no-op preview, otherwise update and wait for the rollout.
func (w bootstrapWork) fromGate(
	ctx context.Context, s StackAPI,
) func(GateResult) IOE.IOEither[error, BootstrapResult] {
	return func(gate GateResult) IOE.IOEither[error, BootstrapResult] {
		if len(gate.Blocked) > 0 && !w.mode.AllowProtected {
			return IOE.Left[BootstrapResult](blockedError(gate.Blocked))
		}
		if w.mode.DryRun || zeroChanges(gate.Summary) {
			CleanupPlan(gate.PlanPath)
			return IOE.Of[error](BootstrapResult{Summary: gate.Summary, Rollout: "unchanged"})
		}
		return w.upThenRollout(ctx, s, gate)
	}
}

func (w bootstrapWork) upThenRollout(
	ctx context.Context, s StackAPI, gate GateResult,
) IOE.IOEither[error, BootstrapResult] {
	return F.Pipe1(
		w.runUp(ctx, s, gate),
		IOE.Chain(w.rollout(ctx, gate.Summary)),
	)
}

func (w bootstrapWork) runUp(ctx context.Context, s StackAPI, gate GateResult) IOE.IOEither[error, struct{}] {
	return F.Pipe2(
		SecretValues(ctx, s),
		IOE.Chain(w.upWithSecrets(ctx, s, gate)),
		IOE.Map[error](dropUpResult),
	)
}

func (w bootstrapWork) upWithSecrets(
	ctx context.Context, s StackAPI, gate GateResult,
) func([]string) IOE.IOEither[error, auto.UpResult] {
	return func(secrets []string) IOE.IOEither[error, auto.UpResult] {
		opts := w.mode.UpOptions(w.svc.Logger, secrets)
		opts.PlanPath = gate.PlanPath
		return Up(ctx, s, opts)
	}
}

func dropUpResult(auto.UpResult) struct{} { return struct{}{} }

func (w bootstrapWork) rollout(
	ctx context.Context, summary PreviewSummary,
) func(struct{}) IOE.IOEither[error, BootstrapResult] {
	return func(struct{}) IOE.IOEither[error, BootstrapResult] {
		return F.Pipe2(
			IOE.TryCatchError(func() (string, error) { return w.svc.Rollout(ctx) }),
			IOE.MapLeft[string](wrapRolloutErr),
			IOE.Map[error](toBootstrapResult(summary)),
		)
	}
}

func wrapRolloutErr(err error) error { return fmt.Errorf("rollout wait: %w", err) }

func toBootstrapResult(summary PreviewSummary) func(string) BootstrapResult {
	return func(state string) BootstrapResult {
		return BootstrapResult{Summary: summary, Rollout: state}
	}
}
