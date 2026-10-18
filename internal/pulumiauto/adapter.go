package pulumiauto

import (
	"context"
	"io"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
)

// Compile-time contracts: the production adapters satisfy the seams.
var (
	_ StackAPI    = autoStack{}
	_ StackOpener = autoOpener{}
)

// autoStack adapts auto.Stack to StackAPI. It is the only place that wires
// Pulumi options: Diff on preview, EventStreams on both, and
// ProgressStreams(io.Discard) UNCONDITIONALLY — raw engine output can carry
// decrypted secrets and never reaches logs or stdout.
type autoStack struct {
	stack auto.Stack
}

func (a autoStack) Name() string { return a.stack.Name() }

// pipeEvents bridges an internal channel closed by the Pulumi SDK's tailLogs
// to the caller-owned event channel, so the caller can safely close ev after
// Preview/Up returns without a double-close panic.
func closeIfOpen(ch chan events.EngineEvent) {
	defer func() { _ = recover() }()
	close(ch)
}

func pipeEvents(dst chan<- events.EngineEvent) (chan events.EngineEvent, func()) {
	raw := make(chan events.EngineEvent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range raw {
			dst <- e
		}
	}()
	return raw, func() {
		closeIfOpen(raw)
		<-done
	}
}

func (a autoStack) Preview(
	ctx context.Context, ev chan<- events.EngineEvent, planPath string,
) (auto.PreviewResult, error) {
	raw, wait := pipeEvents(ev)
	defer wait()
	return a.stack.Preview(ctx,
		optpreview.Diff(),
		optpreview.EventStreams(raw),
		optpreview.ProgressStreams(io.Discard),
		optpreview.SuppressOutputs(),
		optpreview.Plan(planPath),
	)
}

func (a autoStack) Up(
	ctx context.Context, ev chan<- events.EngineEvent, planPath string,
) (auto.UpResult, error) {
	raw, wait := pipeEvents(ev)
	defer wait()
	return a.stack.Up(ctx,
		optup.EventStreams(raw),
		optup.ProgressStreams(io.Discard),
		optup.ShowSecrets(false),
		optup.SuppressOutputs(),
		optup.Plan(planPath),
	)
}

func (a autoStack) Destroy(ctx context.Context) error {
	_, err := a.stack.Destroy(ctx)
	return err
}

func (a autoStack) SetConfig(ctx context.Context, key string, val auto.ConfigValue) error {
	// Path semantics: every replaced recipe used `config set --path` on keys
	// like properties.secret.password. A non-path write would flatten them.
	return a.stack.SetConfigWithOptions(ctx, key, val, &auto.ConfigOptions{Path: true})
}

func (a autoStack) GetAllConfig(ctx context.Context) (auto.ConfigMap, error) {
	return a.stack.GetAllConfig(ctx)
}

func (a autoStack) Outputs(ctx context.Context) (auto.OutputMap, error) {
	return a.stack.Outputs(ctx)
}

// AutoOpener returns the production opener.
func AutoOpener() StackOpener { return autoOpener{} }

// autoOpener opens stacks through the Automation API bound to the target.
type autoOpener struct{}

// workspaceOptions builds the local workspace bindings from the target.
func workspaceOptions(t DeployTarget) []auto.LocalWorkspaceOption {
	opts := []auto.LocalWorkspaceOption{
		auto.WorkDir(t.Folder),
		auto.SecretsProvider(t.Secrets),
		auto.EnvVars(map[string]string{
			"PULUMI_BACKEND_URL": t.Backend,
		}),
	}
	if t.CredFile != "" {
		opts = append(opts, auto.EnvVars(map[string]string{
			"GOOGLE_APPLICATION_CREDENTIALS": t.CredFile,
		}))
	}
	if t.Passphrase != "" {
		opts = append(opts, auto.EnvVars(map[string]string{
			"PULUMI_CONFIG_PASSPHRASE": t.Passphrase,
		}))
	}
	return opts
}

func (autoOpener) Select(ctx context.Context, t DeployTarget) (StackAPI, error) {
	s, err := auto.SelectStackLocalSource(ctx, t.Stack, t.Folder, workspaceOptions(t)...)
	if err != nil {
		return nil, err
	}
	return autoStack{stack: s}, nil
}

func (autoOpener) Upsert(ctx context.Context, t DeployTarget) (StackAPI, error) {
	s, err := auto.UpsertStackLocalSource(ctx, t.Stack, t.Folder, workspaceOptions(t)...)
	if err != nil {
		return nil, err
	}
	return autoStack{stack: s}, nil
}
