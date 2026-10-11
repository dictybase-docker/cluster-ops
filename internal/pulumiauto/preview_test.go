package pulumiauto

import (
	"context"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/require"
)

func resourceEvent(op apitype.OpType, urn, typ string) events.EngineEvent {
	return events.EngineEvent{EngineEvent: apitype.EngineEvent{
		ResourcePreEvent: &apitype.ResourcePreEvent{
			Metadata: apitype.StepEventMetadata{Op: op, URN: urn, Type: typ},
		},
	}}
}

func previewChanges(change map[apitype.OpType]int) auto.PreviewResult {
	return auto.PreviewResult{ChangeSummary: change}
}

func statefulSetReplaceEvent() events.EngineEvent {
	return resourceEvent(apitype.OpReplace, "urn:pulumi:s::p::k8s::sts",
		"kubernetes:apps/v1:StatefulSet")
}

func createEvent() events.EngineEvent {
	return resourceEvent(apitype.OpCreate, "urn:pulumi:s::p::k8s::cm",
		"kubernetes:core/v1:ConfigMap")
}

func TestSummaryFrom(t *testing.T) {
	got := SummaryFrom(map[apitype.OpType]int{
		apitype.OpCreate: 2, apitype.OpUpdate: 1, apitype.OpDelete: 1,
		apitype.OpReplace: 3, apitype.OpSame: 9,
	})
	require.Equal(t, PreviewSummary{Creates: 2, Updates: 1, Deletes: 1, Replaces: 3, Same: 9}, got)
}

func TestGateBlocksProtectedReplace(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewRes: auto.PreviewResult{
		ChangeSummary: map[apitype.OpType]int{apitype.OpReplace: 1},
	}, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	require.NoError(t, err)
	require.Len(t, got.Blocked, 1)
	require.Equal(t, "kubernetes:apps/v1:StatefulSet", got.Blocked[0].Type)
}

func TestGateAllowsPlainCreate(t *testing.T) {
	st := &fakeStack{previewRes: auto.PreviewResult{
		ChangeSummary: map[apitype.OpType]int{apitype.OpCreate: 1},
	}, previewEv: []events.EngineEvent{createEvent()}}
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	require.NoError(t, err)
	require.Empty(t, got.Blocked)
}

// Review Focus 2: a blocked gate stops the flow before Up.
func TestGateStopsBeforeUp(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	mode := DeployMode{} // no AllowProtected
	_, err := E.UnwrapError(RunIfAllowed(context.Background(), st, mode, upCall(st, mode))())
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked")
	require.NotContains(t, log, "up")
}

func TestGateAllowsProtectedOverride(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	mode := DeployMode{AllowProtected: true}
	_, err := E.UnwrapError(RunIfAllowed(context.Background(), st, mode, upCall(st, mode))())
	require.NoError(t, err)
	require.Contains(t, log, "up")
}

// The collector exits on channel close: no goroutine leak, no deadlock.
func TestGateCollectorExitsOnClose(t *testing.T) {
	st := &fakeStack{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = E.UnwrapError(PreviewGate(context.Background(), st, nil)())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PreviewGate deadlocked on the event channel")
	}
}

// upCall stands in for the named Update step RunIfAllowed guards (the real Up
// lands in Task 5; here the step only records itself on the shared log).
func upCall(st *fakeStack, _ DeployMode) func() IOE.IOEither[error, auto.UpResult] {
	return func() IOE.IOEither[error, auto.UpResult] {
		st.record("up")
		return IOE.Of[error](auto.UpResult{})
	}
}
