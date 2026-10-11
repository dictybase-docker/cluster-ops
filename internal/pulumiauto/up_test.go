package pulumiauto

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/require"
)

func diagnosticEvent(msg string) events.EngineEvent {
	return events.EngineEvent{EngineEvent: apitype.EngineEvent{
		DiagnosticEvent: &apitype.DiagnosticEvent{Message: msg},
	}}
}

// Review Focus 1: a secret value must not leak through any log path.
func TestUpRedactsSecretValues(t *testing.T) {
	var buf bytes.Buffer
	st := &fakeStack{upEv: []events.EngineEvent{
		diagnosticEvent("db password is " + testSecret),
		resourceEvent(apitype.OpUpdate, "urn:pulumi:s::p::k8s::cm", "kubernetes:core/v1:ConfigMap"),
	}}
	opts := UpOptions{
		Logger:  slog.New(slog.NewTextHandler(&buf, nil)),
		Secrets: []string{testSecret},
	}
	_, err := E.UnwrapError(Up(context.Background(), st, opts)())
	require.NoError(t, err)
	require.NotContains(t, buf.String(), testSecret)
	require.Contains(t, buf.String(), "urn:pulumi:s::p::k8s::cm", "resource metadata is logged")
}

func TestUpDryRunNeverUpdates(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log}
	mode := DeployMode{DryRun: true}
	_, err := E.UnwrapError(Up(context.Background(), st, mode.UpOptions(nil, nil))())
	require.NoError(t, err)
	require.NotContains(t, log, "up")
}

func TestRedactorMasksExactValues(t *testing.T) {
	r := Redactor{secrets: []string{testSecret, ""}}
	require.Equal(t, "db pass is ***", r.Redact("db pass is "+testSecret))
	require.Equal(t, "plain text", (Redactor{}).Redact("plain text"),
		"empty secrets are ignored; empty redactor is identity")
}

// The event consumer must exit when the channel closes.
func TestUpCollectorExitsOnClose(t *testing.T) {
	st := &fakeStack{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = E.UnwrapError(Up(context.Background(), st, UpOptions{})())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Up deadlocked on the event channel")
	}
}
