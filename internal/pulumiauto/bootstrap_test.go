package pulumiauto

import (
	"context"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/require"
)

// Shared fixture values (never real).
const (
	testFolder     = "modware-annotation"
	testStack      = "experiments"
	testBackend    = "gs://pulumi-state-proj"
	testSecretsURI = "gcpkms://projects/p/keys/pulumi" //nolint:gosec // fixture
)

// fakePreflight records the gate and replays canned results.
func fakePreflight(log *[]string, results ...preflight.Result) PreflightRunner {
	return func() []preflight.Result {
		*log = append(*log, "preflight")
		return results
	}
}

// fakeRollout records the rollout wait.
func fakeRollout(log *[]string) RolloutWaiter {
	return func(context.Context) (string, error) {
		*log = append(*log, "rollout")
		return "ready", nil
	}
}

func bootstrapFixture(t *testing.T, log *[]string, st *fakeStack) (StackOpener, DeployTarget, ServiceSpec) {
	t.Helper()
	op := &fakeOpener{returned: st, log: log}
	target := DeployTarget{Folder: testFolder, Stack: testStack,
		Backend: testBackend, Secrets: testSecretsURI}
	return op, target, ServiceSpec{
		Folder: testFolder, Stack: testStack, ImageTag: "v1.2.3",
		Preflight: fakePreflight(log), Rollout: fakeRollout(log),
	}
}

// Review Focus: the composite runs preflight → open → set-config → preview →
// up → rollout in that order.
func TestBootstrapHappy(t *testing.T) {
	log := []string{}
	st := &fakeStack{name: testStack, log: &log,
		previewRes: previewChanges(map[apitype.OpType]int{apitype.OpCreate: 1}),
		previewEv:  []events.EngineEvent{createEvent()},
		upRes:      auto.UpResult{},
	}
	op, target, svc := bootstrapFixture(t, &log, st)
	got, err := E.UnwrapError(BootstrapService(context.Background(), op, target, svc, DeployMode{})())
	require.NoError(t, err)
	require.Equal(t,
		[]string{"preflight", "open", "set-config:" + imageTagKey, "preview", "up", "rollout"},
		log, "preflight → open → set-config → preview → up → rollout")
	require.Equal(t, 1, got.Summary.Creates)
}

// Review Focus 2: a protected replace stops the flow before up/rollout.
func TestBootstrapStopsOnGate(t *testing.T) {
	log := []string{}
	st := &fakeStack{name: testStack, log: &log,
		previewEv: []events.EngineEvent{statefulSetReplaceEvent()},
	}
	op, target, svc := bootstrapFixture(t, &log, st)
	_, err := E.UnwrapError(BootstrapService(context.Background(), op, target, svc, DeployMode{})())
	require.Error(t, err)
	require.NotContains(t, log, "up")
	require.NotContains(t, log, "rollout")
}

// A failing preflight stops the flow before the stack is even opened.
func TestBootstrapStopsOnPreflightFail(t *testing.T) {
	log := []string{}
	st := &fakeStack{name: testStack, log: &log}
	op, target, svc := bootstrapFixture(t, &log, st)
	svc.Preflight = fakePreflight(&log, preflight.Result{Name: "namespace", Status: preflight.StatusFail})
	_, err := E.UnwrapError(BootstrapService(context.Background(), op, target, svc, DeployMode{})())
	require.Error(t, err)
	require.Equal(t, []string{"preflight"}, log, "no stack work after a failed preflight")
}

// A no-op preview (everything same) never reaches up; the run reports unchanged.
func TestBootstrapIdempotent(t *testing.T) {
	log := []string{}
	st := &fakeStack{name: testStack, log: &log,
		previewRes: previewChanges(map[apitype.OpType]int{apitype.OpSame: 3}),
	}
	op, target, svc := bootstrapFixture(t, &log, st)
	got, err := E.UnwrapError(BootstrapService(context.Background(), op, target, svc, DeployMode{})())
	require.NoError(t, err)
	require.NotContains(t, log, "up")
	require.Equal(t, "unchanged", got.Rollout)
}
