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

// protectedPolicy is the shared gate policy fixture (prod = database ns).
func protectedPolicy() ProtectionPolicy {
	return PolicyFrom(DeployMode{ProtectDeployIn: []string{testNS}})
}

// testNS is the protected database namespace fixture.
const testNS = "prod"

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
	ev := resourceEvent(apitype.OpReplace, "urn:pulumi:s::p::k8s::sts",
		"kubernetes:apps/v1:StatefulSet")
	ev.ResourcePreEvent.Metadata.New = &apitype.StepEventStateMetadata{ID: "prod/order-db"}
	return ev
}

func createEvent() events.EngineEvent {
	return resourceEvent(apitype.OpCreate, "urn:pulumi:s::p::k8s::cm",
		"kubernetes:core/v1:ConfigMap")
}

// deletedPVCEvent mimics a delete pre-event as the engine sends it:
// "new" is null, and the namespace-bearing ID lives only on Old. A restore
// stack preview always deletes the previous run's Job/PVC (fresh restoreId).
func deletedPVCEvent() events.EngineEvent {
	ev := resourceEvent(apitype.OpDelete, "urn:pulumi:s::p::k8s::pvc",
		"kubernetes:core/v1:PersistentVolumeClaim")
	ev.ResourcePreEvent.Metadata.Old = &apitype.StepEventStateMetadata{ID: "prod/arangodb-restore-scratch"}
	return ev
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
	policy := protectedPolicy()
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, policy)())
	require.NoError(t, err)
	require.Len(t, got.Blocked, 1)
	require.Equal(t, "kubernetes:apps/v1:StatefulSet", got.Blocked[0].Type)
}

func TestGateAllowsPlainCreate(t *testing.T) {
	st := &fakeStack{previewRes: auto.PreviewResult{
		ChangeSummary: map[apitype.OpType]int{apitype.OpCreate: 1},
	}, previewEv: []events.EngineEvent{createEvent()}}
	got, err := E.UnwrapError(PreviewGate(context.Background(), st, protectedPolicy())())
	require.NoError(t, err)
	require.Empty(t, got.Blocked)
}

// Review Focus 2: a blocked gate stops the flow before Up.
func TestGateStopsBeforeUp(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log, previewEv: []events.EngineEvent{statefulSetReplaceEvent()}}
	mode := DeployMode{ProtectDeployIn: []string{testNS}} // no AllowProtected
	_, err := E.UnwrapError(RunIfAllowed(context.Background(), st, mode, upCall(st, mode))())
	require.Error(t, err)
	require.Contains(t, err.Error(), "blocked")
	require.NotContains(t, stepNames(log), stepUp)
}

func TestGateAllowsProtectedOverride(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log,
		previewRes: previewChanges(map[apitype.OpType]int{apitype.OpReplace: 1}),
		previewEv:  []events.EngineEvent{statefulSetReplaceEvent()},
	}
	mode := DeployMode{AllowProtected: true, ProtectDeployIn: []string{testNS}}
	_, err := E.UnwrapError(RunIfAllowed(context.Background(), st, mode, upCall(st, mode))())
	require.NoError(t, err)
	require.Contains(t, stepNames(log), stepUp)
}

// The collector exits on channel close: no goroutine leak, no deadlock.
func TestGateHandlesNullNewState(t *testing.T) {
	// Delete pre-event: New is nil, ID lives on Old. The gate must read the
	// namespace from Old instead of panicking, and a protected delete blocks.
	ch := make(chan events.EngineEvent, 1)
	out := make(chan []ProtectedChange)
	ch <- deletedPVCEvent()
	close(ch)
	go collectProtected(ch, out, protectedPolicy())
	blocked := <-out
	require.Len(t, blocked, 1)
	require.Equal(t, "prod", blocked[0].Namespace)
	require.Equal(t, apitype.OpDelete, blocked[0].Op)

	// Delete with both states nil: no namespace, no panic, no block.
	ch2 := make(chan events.EngineEvent, 1)
	out2 := make(chan []ProtectedChange)
	ch2 <- resourceEvent(apitype.OpDelete, "urn:pulumi:s::p::k8s::job",
		"kubernetes:batch/v1:Job")
	close(ch2)
	go collectProtected(ch2, out2, protectedPolicy())
	require.Empty(t, <-out2)
}

func TestGateCollectorExitsOnClose(t *testing.T) {
	st := &fakeStack{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		policy := protectedPolicy()
		_, _ = E.UnwrapError(PreviewGate(context.Background(), st, policy)())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PreviewGate deadlocked on the event channel")
	}
}

func TestPipeEventsHandlesSDKClosedAndUnclosedChannel(t *testing.T) {
	dst := make(chan events.EngineEvent, 2)
	raw, wait := pipeEvents(dst)
	raw <- createEvent()
	close(raw) // simulate Pulumi SDK tailLogs.Close() closing the EventStreams channel
	wait()
	close(dst)
	require.Len(t, dst, 1)

	// Also verify wait() does not block if the SDK errors before closing raw.
	dstEarly := make(chan events.EngineEvent, 1)
	_, waitEarly := pipeEvents(dstEarly)
	waitEarly()
	close(dstEarly)
}

// upCall stands in for the named Update step RunIfAllowed guards (the real Up
// lands in Task 5; here the step only records itself on the shared log).
func upCall(st *fakeStack, _ DeployMode) func(string) IOE.IOEither[error, auto.UpResult] {
	return func(planPath string) IOE.IOEither[error, auto.UpResult] {
		st.record(stepUp + ":" + planPath)
		return IOE.Of[error](auto.UpResult{})
	}
}
