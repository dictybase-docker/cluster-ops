package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/pulumiauto"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/events"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// fakeStack records the composite's calls (cmd-level wiring proof).
type fakeStack struct {
	log  []string
	name string
}

func (f *fakeStack) Name() string { return f.name }

func (f *fakeStack) Preview(
	_ context.Context, _ chan<- events.EngineEvent, planPath string,
) (auto.PreviewResult, error) {
	f.log = append(f.log, "preview:"+planPath)
	return auto.PreviewResult{ChangeSummary: map[apitype.OpType]int{apitype.OpCreate: 1}}, nil
}

func (f *fakeStack) Up(_ context.Context, _ chan<- events.EngineEvent, planPath string) (auto.UpResult, error) {
	f.log = append(f.log, "up:"+planPath)
	return auto.UpResult{}, nil
}

func (f *fakeStack) Destroy(context.Context) error {
	f.log = append(f.log, destroyCmd)
	return nil
}

func (f *fakeStack) SetConfig(_ context.Context, key string, _ auto.ConfigValue) error {
	f.log = append(f.log, "set-config:"+key)
	return nil
}

func (f *fakeStack) GetAllConfig(context.Context) (auto.ConfigMap, error) {
	return auto.ConfigMap{}, nil
}

func (f *fakeStack) Outputs(context.Context) (auto.OutputMap, error) {
	return auto.OutputMap{}, nil
}

type fakeOpener struct {
	stack *fakeStack
	opens int
}

func (f *fakeOpener) Select(context.Context, pulumiauto.DeployTarget) (pulumiauto.StackAPI, error) {
	f.opens++
	return f.stack, nil
}

func (f *fakeOpener) Upsert(context.Context, pulumiauto.DeployTarget) (pulumiauto.StackAPI, error) {
	f.opens++
	return f.stack, nil
}

// Review Focus 1: the batch command must EXECUTE without --folder and bind
// each service's own folder to its preflight.
func TestDeployBackendWiresServices(t *testing.T) {
	opener := &fakeOpener{stack: &fakeStack{name: "s1"}}
	restoreOpener := newOpener
	newOpener = func() pulumiauto.StackOpener { return opener }
	defer func() { newOpener = restoreOpener }()

	dir := t.TempDir()
	servicesPath := filepath.Join(dir, "services.yaml")
	require.NoError(t, os.WriteFile(servicesPath,
		[]byte(servicesFixtureYAML), 0o600))
	t.Setenv("PULUMI_BACKEND_URL", "gs://pulumi-state-proj")
	t.Setenv("PULUMI_SECRET_PROVIDER", "gcpkms://projects/p/keys/pulumi")
	t.Setenv("PULUMI_GCP_CREDENTIALS", "/creds/deployer.json")

	boundFolders := []string{}
	restorePre, restoreRoll := prereqsRunnerFn, rolloutWaiterFn
	prereqsRunnerFn = func(_ *cli.Context, folder, _ string) pulumiauto.PreflightRunner {
		return func() []preflight.Result {
			boundFolders = append(boundFolders, folder)
			return nil
		}
	}
	rolloutWaiterFn = func(_ *cli.Context, _, _ string) pulumiauto.RolloutWaiter {
		return func(context.Context) (string, error) { return "ready", nil }
	}
	defer func() { prereqsRunnerFn, rolloutWaiterFn = restorePre, restoreRoll }()

	app := stackTestApp()
	app.Commands = append(app.Commands, deployCommand())
	app.ExitErrHandler = func(*cli.Context, error) {}
	require.NoError(t, app.Run([]string{
		appName, "deploy", "backend", "--services-file", servicesPath,
		"--stack", "s1", "--image-tag", "v1",
	}))
	require.Equal(t, 2, opener.opens, "both services deploy (no --folder required)")
	require.Equal(t, []string{"modware-order", "modware-annotation"}, boundFolders,
		"each service's own folder drives its preflight")
	require.Contains(t, opener.stack.log, "set-config:properties.image.tag")
}

// Review Focus: destroy must not mutate before an explicit confirmation.
func TestStackDestroyRequiresConfirm(t *testing.T) {
	opener := &fakeOpener{stack: &fakeStack{name: "s1"}}
	restoreOpener := newOpener
	newOpener = func() pulumiauto.StackOpener { return opener }
	defer func() { newOpener = restoreOpener }()
	t.Setenv("PULUMI_BACKEND_URL", "gs://pulumi-state-proj")
	t.Setenv("PULUMI_SECRET_PROVIDER", "passphrase")

	app := stackTestApp()
	app.ExitErrHandler = func(*cli.Context, error) {}
	err := app.Run([]string{appName, "stack", destroyCmd, "--folder", "f", "--stack", "s1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "confirm")
	require.NotContains(t, opener.stack.log, destroyCmd)
}

// Shared fixtures (never real).
const (
	fixtureCredPath     = "/creds/deployer.json" //nolint:gosec // fixture path, not a credential
	servicesFixtureYAML = "services:\n" +
		"  - app: order\n    folder: modware-order\n" +
		"  - app: annotation\n    folder: modware-annotation\n"
)

// registryMissing is a typed registry miss (may fall back to env).
func registryMissing() E.Either[error, registry.Entry] {
	return E.Left[registry.Entry](fmt.Errorf("no registry entry: %w", registry.ErrEntryNotFound))
}

// registryBroken is an untyped registry failure (must fail closed).
func registryBroken() E.Either[error, registry.Entry] {
	return E.Left[registry.Entry](fmt.Errorf("parse registry entry: %w", errors.New("yaml: bad")))
}

// Review Focus 5: the Pulumi credential comes from PULUMI_GCP_CREDENTIALS
// (the deployer key), never the ambient kops-creator key.
func TestTargetFromUsesDeployerCredential(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/creds/kops-creator.json")
	t.Setenv("PULUMI_GCP_CREDENTIALS", fixtureCredPath)
	require.Equal(t, fixtureCredPath, pulumiCredential())

	t.Setenv("PULUMI_GCP_CREDENTIALS", "")
	require.Empty(t, pulumiCredential(),
		"an unset deployer key must not silently use the kops-creator key")

	got, err := targetFromEnv(t.Context(), registryMissing(), targetEnv{ //nolint:gosec // fixture
		Folder: "f", Stack: "s1",
		Backend: "gs://pulumi-state-proj", Secrets: "gcpkms://projects/p/keys/pulumi",
		CredFile: fixtureCredPath,
	})
	require.NoError(t, err)
	require.Equal(t, "/creds/deployer.json", got.CredFile)
	require.Equal(t, "gs://pulumi-state-proj", got.Backend)
}

// A malformed registry entry must fail closed (no silent env fallback).
func TestTargetFromFailsOnRegistryError(t *testing.T) {
	_, err := targetFromEnv(t.Context(), registryBroken(), targetEnv{Folder: "f", Stack: "s1"})
	require.Error(t, err)
	require.NotErrorIs(t, err, registry.ErrEntryNotFound)
}

// A typed registry miss may fall back to the environment, but only with
// complete bindings.
func TestTargetFromRequiresBindingsOnFallback(t *testing.T) {
	_, err := targetFromEnv(t.Context(), registryMissing(), targetEnv{Folder: "f", Stack: "s1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "PULUMI_BACKEND_URL")
}
