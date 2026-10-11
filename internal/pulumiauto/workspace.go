// Package pulumiauto wraps the Pulumi Automation API for the composite
// deploy flows. Every flow takes injected seams; production adapters live in
// adapter.go and are the only users of the auto package outside this folder.
package pulumiauto

import (
	"context"
	"fmt"

	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
)

// DeployTarget binds a folder+stack to its registry entry.
type DeployTarget struct {
	Folder, Stack string
	Backend       string // entry.PulumiState
	Secrets       string // entry.KmsSecretsProvider ("passphrase" locally)
	CredFile      string // GOOGLE_APPLICATION_CREDENTIALS
	Passphrase    string // PULUMI_CONFIG_PASSPHRASE for local stacks
}

// DeployMode governs mutations (the gcp-converge MutationMode shape).
type DeployMode struct {
	DryRun         bool
	AllowProtected bool
}

// StackOpener opens stacks against a FULL target. The production opener
// wires WorkDir/SecretsProvider/EnvVars from the target fields.
type StackOpener interface {
	Select(ctx context.Context, t DeployTarget) (StackAPI, error)
	Upsert(ctx context.Context, t DeployTarget) (StackAPI, error)
}

// StackAPI is the flow surface. auto.Stack is a struct and cannot be faked.
// Preview and Up receive the event channel: the caller creates it, the
// adapter wires optpreview/optup.EventStreams, the caller closes it after
// the call returns and its collector goroutine exits on close.
type StackAPI interface {
	Name() string
	Preview(ctx context.Context, events chan<- events.EngineEvent) (auto.PreviewResult, error)
	Up(ctx context.Context, events chan<- events.EngineEvent) (auto.UpResult, error)
	Destroy(ctx context.Context) error
	SetConfig(ctx context.Context, key string, val auto.ConfigValue) error
	GetAllConfig(ctx context.Context) (auto.ConfigMap, error)
	Outputs(ctx context.Context) (auto.OutputMap, error)
}

// PreflightRunner runs the preflight gate checks (internal/preflight).
type PreflightRunner func() []preflight.Result

// RolloutWaiter waits for the deployed rollout (internal/waitfor).
type RolloutWaiter func(ctx context.Context) (string, error)

// OpenStack selects the stack, or creates it when create is set. Every
// binding comes from the target; failures name the cause (missing pulumi
// CLI, no Pulumi.yaml, wrong backend).
func OpenStack(ctx context.Context, opener StackOpener, t DeployTarget, create bool) (StackAPI, error) {
	if t.Folder == "" || t.Stack == "" {
		return nil, fmt.Errorf("open stack: folder and stack must be bound")
	}
	var (
		s   StackAPI
		err error
	)
	if create {
		s, err = opener.Upsert(ctx, t)
	} else {
		s, err = opener.Select(ctx, t)
	}
	if err != nil {
		return nil, fmt.Errorf("pulumi open stack %s in %s: %w", t.Stack, t.Folder, err)
	}
	return s, nil
}
