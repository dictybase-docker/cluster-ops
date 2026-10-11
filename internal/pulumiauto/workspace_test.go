package pulumiauto

import (
	"context"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeOpener struct {
	selected []string // "stack|folder|backend" per call
	upserted []string
	failWith error
	returned *fakeStack
	log      *[]string // shared sequence log
}

func (f *fakeOpener) Select(_ context.Context, t DeployTarget) (StackAPI, error) {
	if f.log != nil {
		*f.log = append(*f.log, stepOpen)
	}
	f.selected = append(f.selected, t.Stack+"|"+t.Folder+"|"+t.Backend)
	return f.returned, f.failWith
}

func (f *fakeOpener) Upsert(_ context.Context, t DeployTarget) (StackAPI, error) {
	if f.log != nil {
		*f.log = append(*f.log, stepOpen)
	}
	f.upserted = append(f.upserted, t.Stack+"|"+t.Folder+"|"+t.Backend)
	return f.returned, f.failWith
}

func TestOpenStackBindsRegistryEntry(t *testing.T) {
	op := &fakeOpener{returned: &fakeStack{name: testStack}}
	target := DeployTarget{Folder: "modware-annotation", Stack: testStack, //nolint:gosec // fixture
		Backend: "gs://pulumi-state-proj", Secrets: "gcpkms://projects/p/keys/pulumi"}
	s, err := OpenStack(context.Background(), op, target, false)
	require.NoError(t, err)
	require.Equal(t, []string{"experiments|modware-annotation|gs://pulumi-state-proj"}, op.selected)
	require.Empty(t, op.upserted, "select must not create")
	require.Equal(t, testStack, s.Name())
}

func TestOpenStackCreatesWhenAsked(t *testing.T) {
	op := &fakeOpener{returned: &fakeStack{name: "new-stack"}}
	_, err := OpenStack(context.Background(), op, DeployTarget{Folder: "f", Stack: "new-stack"}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"new-stack|f|"}, op.upserted)
	require.Empty(t, op.selected)
}

// Review Focus 3: a missing CLI must surface as a named cause, not a panic.
func TestOpenStackNamesMissingCLI(t *testing.T) {
	_, err := OpenStack(context.Background(), &fakeOpener{failWith: exec.ErrNotFound},
		DeployTarget{Folder: "f", Stack: "s"}, false)
	require.ErrorIs(t, err, exec.ErrNotFound)
	require.Contains(t, err.Error(), "pulumi")
}

func TestOpenStackRejectsUnboundTarget(t *testing.T) {
	_, err := OpenStack(context.Background(), &fakeOpener{}, DeployTarget{}, true)
	require.Error(t, err, "folder and stack must be bound")
}
