package main

import (
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

const (
	testLogLevel    = "error"
	testNatsSubject = "OrderService.Create"
)

type issueResourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
}

func (m *issueResourceMocks) Call(
	pulumi.MockCallArgs,
) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *issueResourceMocks) NewResource(
	args pulumi.MockResourceArgs,
) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	return args.Name, args.Inputs, nil
}

func baseIssueEventMessengerConfig() *EventMessengerConfig {
	return &EventMessengerConfig{
		Namespace: "prod",
		LogLevel:  testLogLevel,
		Nats: NatsProperties{
			Subject: testNatsSubject,
		},
		Image: ImageConfig{
			Name:       "dictybase/event-messenger",
			Tag:        "v1.4.0",
			PullPolicy: "IfNotPresent",
		},
		IssueDeployment: IssueDeployment{
			Name: "event-messenger-issue",
			Secrets: IssueSecrets{
				Name: "issue-event",
				Keys: IssueSecretKeys{
					Owner:            "eventMessenger.github.owner",
					Repository:       "eventMessenger.github.repository",
					Token:            "eventMessenger.github.token",
					OpenRouterAPIKey: "eventMessenger.openrouter.apiKey",
				},
				//nolint:gosec // G101 false positive on test fixture secret values.
				Values: IssueSecretValues{
					Owner:            "dictyBase",
					Repository:       "Stock-Center-Orders",
					Token:            "ghp-test-token",
					OpenRouterAPIKey: "sk-or-v1-test-secret",
				},
			},
		},
	}
}

func runIssueInstall(
	t *testing.T,
	cfg *EventMessengerConfig,
) map[string]pulumi.MockResourceArgs {
	t.Helper()

	mocks := &issueResourceMocks{
		resources: make(map[string]pulumi.MockResourceArgs),
	}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		emn := NewEventMessenger(cfg)
		sec, secErr := emn.CreateIssueSecret(ctx)
		if secErr != nil {
			return secErr
		}
		dep, depErr := emn.CreateIssueDeployment(ctx, sec)
		if depErr != nil {
			return depErr
		}
		ctx.Export("issueDeployment", dep.URN())
		return nil
	}, pulumi.WithMocks("event-messenger", "test", mocks))
	require.NoError(t, err)

	mocks.mu.Lock()
	defer mocks.mu.Unlock()
	return mocks.resources
}

func extractContainerSpec(
	t *testing.T,
	dep pulumi.MockResourceArgs,
) resource.PropertyMap {
	t.Helper()

	spec := dep.Inputs["spec"].ObjectValue()
	tmpl := spec["template"].ObjectValue()
	podSpec := tmpl["spec"].ObjectValue()
	containers := podSpec["containers"].ArrayValue()
	require.Len(t, containers, 1)
	return containers[0].ObjectValue()
}

func unwrapSecretStringData(
	t *testing.T,
	sec pulumi.MockResourceArgs,
) resource.PropertyMap {
	t.Helper()

	raw := sec.Inputs["stringData"]
	require.True(t, raw.IsSecret(), "Secret stringData must be marked as a Pulumi secret")
	return raw.SecretValue().Element.ObjectValue()
}

func unwrapPlainOrSecretString(v resource.PropertyValue) string {
	if v.IsSecret() {
		return v.SecretValue().Element.StringValue()
	}
	return v.StringValue()
}

func TestIssueSecretAndDeploymentWithOpenRouterKey(t *testing.T) {
	resources := runIssueInstall(t, baseIssueEventMessengerConfig())

	sec, hasSec := resources["issue-event"]
	require.True(t, hasSec)

	stringData := unwrapSecretStringData(t, sec)
	orProp, hasOR := stringData[resource.PropertyKey("eventMessenger.openrouter.apiKey")]
	require.True(t, hasOR)
	require.Equal(t, "sk-or-v1-test-secret", unwrapPlainOrSecretString(orProp))

	dep, hasDep := resources["event-messenger-issue"]
	require.True(t, hasDep)
	container := extractContainerSpec(t, dep)

	var args []string
	for _, a := range container["args"].ArrayValue() {
		args = append(args, a.StringValue())
	}
	require.Equal(
		t,
		[]string{
			flagLogLevel,
			testLogLevel,
			"gh-issue",
			flagSubject,
			testNatsSubject,
			"--token",
			"$(GITHUB_TOKEN)",
			"--repository",
			"$(GITHUB_REPOSITORY)",
			"--owner",
			"$(GITHUB_OWNER)",
		},
		args,
	)

	envByName := make(map[string]resource.PropertyMap)
	for _, e := range container["env"].ArrayValue() {
		em := e.ObjectValue()
		envByName[em["name"].StringValue()] = em
	}
	require.Len(t, envByName, 4)

	orEnv, hasOREnv := envByName["OPENROUTER_API_KEY"]
	require.True(t, hasOREnv)
	ref := orEnv["valueFrom"].ObjectValue()["secretKeyRef"].ObjectValue()
	require.Equal(t, "issue-event", ref["name"].StringValue())
	require.Equal(
		t,
		"eventMessenger.openrouter.apiKey",
		ref["key"].StringValue(),
	)
	require.True(t, ref["optional"].BoolValue())
}

func TestIssueSecretAndDeploymentWithAbsentOpenRouterValue(t *testing.T) {
	cfg := baseIssueEventMessengerConfig()
	cfg.IssueDeployment.Secrets.Values.OpenRouterAPIKey = ""

	resources := runIssueInstall(t, cfg)

	sec := resources["issue-event"]
	stringData := unwrapSecretStringData(t, sec)
	_, hasOR := stringData[resource.PropertyKey("eventMessenger.openrouter.apiKey")]
	require.False(t, hasOR, "empty OpenRouter API key value must not write an entry into Secret stringData")

	dep := resources["event-messenger-issue"]
	container := extractContainerSpec(t, dep)
	envByName := make(map[string]resource.PropertyMap)
	for _, e := range container["env"].ArrayValue() {
		em := e.ObjectValue()
		envByName[em["name"].StringValue()] = em
	}
	require.Len(t, envByName, 4)
	orEnv, hasOREnv := envByName["OPENROUTER_API_KEY"]
	require.True(t, hasOREnv)
	ref := orEnv["valueFrom"].ObjectValue()["secretKeyRef"].ObjectValue()
	require.True(t, ref["optional"].BoolValue())
}

func TestIssueSecretAndDeploymentWithLegacyStackOmittingOpenRouterKey(
	t *testing.T,
) {
	legacyCfg := &EventMessengerConfig{
		Namespace: "prod",
		LogLevel:  testLogLevel,
		Nats: NatsProperties{
			Subject: testNatsSubject,
		},
		Image: ImageConfig{
			Name:       "dictybase/event-messenger",
			Tag:        "v1.4.0",
			PullPolicy: "IfNotPresent",
		},
		IssueDeployment: IssueDeployment{
			Name: "event-messenger-issue",
			Secrets: IssueSecrets{
				Name: "issue-event",
				Keys: IssueSecretKeys{
					Owner:      "eventMessenger.github.owner",
					Repository: "eventMessenger.github.repository",
					Token:      "eventMessenger.github.token",
				},
				//nolint:gosec // G101 false positive on test fixture secret values.
				Values: IssueSecretValues{
					Owner:      "dictyBase",
					Repository: "Stock-Center-Orders",
					Token:      "ghp-test-token",
				},
			},
		},
	}

	resources := runIssueInstall(t, legacyCfg)

	sec := resources["issue-event"]
	stringData := unwrapSecretStringData(t, sec)
	require.Len(t, stringData, 3)

	dep := resources["event-messenger-issue"]
	container := extractContainerSpec(t, dep)
	envByName := make(map[string]resource.PropertyMap)
	for _, e := range container["env"].ArrayValue() {
		em := e.ObjectValue()
		envByName[em["name"].StringValue()] = em
	}
	require.Len(t, envByName, 3)
	_, hasOREnv := envByName["OPENROUTER_API_KEY"]
	require.False(
		t,
		hasOREnv,
		"legacy stack omitting keys.openrouterApiKey must not emit an empty secretKeyRef",
	)

	whitespaceCfg := baseIssueEventMessengerConfig()
	whitespaceCfg.IssueDeployment.Secrets.Keys.OpenRouterAPIKey = "   "
	whitespaceCfg.IssueDeployment.Secrets.Values.OpenRouterAPIKey = ""
	wsResources := runIssueInstall(t, whitespaceCfg)
	wsContainer := extractContainerSpec(t, wsResources["event-messenger-issue"])
	require.Len(t, wsContainer["env"].ArrayValue(), 3)
}
