package pulumiauto

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/require"
)

// serviceFixture builds a service whose steps log the app name on the shared
// sequence log.
func serviceFixture(log *[]string, app, folder string, failPreflight bool) ServiceSpec {
	return ServiceSpec{
		Folder: folder, Stack: testStack, ImageTag: "v1",
		Preflight: func() []preflight.Result {
			*log = append(*log, "preflight:"+app)
			if failPreflight {
				return []preflight.Result{{Name: app, Status: preflight.StatusFail}}
			}
			return nil
		},
		Rollout: func(context.Context) (string, error) {
			*log = append(*log, "rollout:"+app)
			return "ready", nil
		},
	}
}

func multiFixture(log *[]string) (*fakeOpener, DeployTarget) {
	st := &fakeStack{name: testStack, log: log,
		previewRes: previewChanges(map[apitype.OpType]int{apitype.OpCreate: 1}),
		previewEv:  []events.EngineEvent{createEvent()},
	}
	return &fakeOpener{returned: st, log: log}, DeployTarget{
		Folder: testFolder, Stack: testStack,
		Backend: testBackend, Secrets: testSecretsURI,
	}
}

// serviceSteps is the expected recorded sequence of one service deploy.
func serviceSteps(app string) []string {
	return []string{"preflight:" + app, stepOpen, "set-config:" + imageTagKey,
		stepPreview, stepUp, "rollout:" + app}
}

// Review Focus: services deploy serially, one fully before the next.
func TestDeployServicesSerialOrder(t *testing.T) {
	log := []string{}
	op, target := multiFixture(&log)
	services := []ServiceSpec{
		serviceFixture(&log, "order", "modware-order", false),
		serviceFixture(&log, "annotation", "modware-annotation", false),
	}
	got, err := E.UnwrapError(DeployBackendServices(context.Background(), op, target, services, DeployMode{})())
	require.NoError(t, err)
	require.Len(t, got, 2)
	want := append(serviceSteps("order"), serviceSteps("annotation")...)
	require.Equal(t, want, stepNames(log), "service A completes before service B starts")
}

// A failing service stops the loop: later services never run.
func TestDeployServicesStopsOnFirstFailure(t *testing.T) {
	log := []string{}
	op, target := multiFixture(&log)
	services := []ServiceSpec{
		serviceFixture(&log, "order", "modware-order", true),
		serviceFixture(&log, "annotation", "modware-annotation", false),
	}
	_, err := E.UnwrapError(DeployBackendServices(context.Background(), op, target, services, DeployMode{})())
	require.Error(t, err)
	require.NotContains(t, stepNames(log), "preflight:annotation", "the second service never starts")
}

// LoadServices reads config/services.yaml in list order.
func TestLoadServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "services.yaml")
	servicesYAML := "services:\n" +
		"  - app: order\n    folder: modware-order\n    port: 9250\n" +
		"  - app: annotation\n    folder: modware-annotation\n    port: 9250\n"
	require.NoError(t, os.WriteFile(path, []byte(servicesYAML), 0o600))
	got, err := LoadServices(path)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "modware-order", got[0].Folder)
	require.Equal(t, "modware-annotation", got[1].Folder)

	_, err = LoadServices(filepath.Join(dir, "missing.yaml"))
	require.Error(t, err, "missing file is a named error, not an empty list")
}
