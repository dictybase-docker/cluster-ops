package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type recorded struct {
	typ    string
	name   string
	inputs resource.PropertyMap
}

type mocks struct {
	mu        sync.Mutex
	resources []recorded
}

func (m *mocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources = append(m.resources, recorded{typ: args.TypeToken, name: args.Name, inputs: args.Inputs})
	return args.Name + "_id", args.Inputs, nil
}

func (m *mocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func (m *mocks) byType(typ string) []recorded {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []recorded{}
	for _, r := range m.resources {
		if r.typ == typ {
			out = append(out, r)
		}
	}
	return out
}

func TestInstallCreatesNamespaceSecretAndRelease(t *testing.T) {
	t.Parallel()
	m := &mocks{}
	cfg := validConfig()
	cfg.ProvisioningToken = testToken
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return (&Falcon{Config: cfg}).Install(ctx)
	}, pulumi.WithMocks("falcon-sensor", "test", m))
	require.NoError(t, err)

	ns := m.byType("kubernetes:core/v1:Namespace")
	require.Len(t, ns, 1)
	labels := ns[0].inputs["metadata"].ObjectValue()["labels"].ObjectValue()
	require.Equal(t, "privileged", labels["pod-security.kubernetes.io/enforce"].StringValue())

	sec := m.byType("kubernetes:core/v1:Secret")
	require.Len(t, sec, 1)
	meta := sec[0].inputs["metadata"].ObjectValue()
	require.Equal(t, secretName, meta["name"].StringValue())
	require.Equal(t, "falcon-system", meta["namespace"].StringValue())
	data := sec[0].inputs["stringData"]
	require.True(t, data.ContainsSecrets(), "Secret data must stay a Pulumi secret")
	plain := data.SecretValue().Element.ObjectValue()
	require.Equal(t, testCID, plain[cidKey].StringValue())
	require.Equal(t, testToken, plain[tokenKey].StringValue())

	rel := m.byType("kubernetes:helm.sh/v3:Release")
	require.Len(t, rel, 1)
	require.Equal(t, releaseName, rel[0].inputs["name"].StringValue())
	require.Equal(t, "1.37.0", rel[0].inputs["version"].StringValue())
	require.Equal(t, "falcon-system", rel[0].inputs["namespace"].StringValue())
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	t.Parallel()
	m := &mocks{}
	err := pulumi.RunErr(Run, pulumi.WithMocks("falcon-sensor", "test", m))
	require.Error(t, err)
	require.Empty(t, m.byType("kubernetes:helm.sh/v3:Release"))
}
