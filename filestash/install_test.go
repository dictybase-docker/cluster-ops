package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

type resourceMocks struct {
	mu        sync.Mutex
	resources map[string]pulumi.MockResourceArgs
	secrets   map[string]bool
}

func (m *resourceMocks) Call(pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return resource.PropertyMap{}, nil
}

func (m *resourceMocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resources[args.Name] = args
	m.secrets[args.Name] = args.Inputs.ContainsSecrets()
	return args.Name, args.Inputs, nil
}

func install(t *testing.T, cfg *FilestashConfig) *resourceMocks {
	t.Helper()
	mocks := &resourceMocks{
		resources: make(map[string]pulumi.MockResourceArgs),
		secrets:   make(map[string]bool),
	}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return NewFilestash(cfg).Install(ctx)
	}, pulumi.WithMocks("filestash", "test", mocks))
	require.NoError(t, err)
	return mocks
}

func mapped(t *testing.T, m *resourceMocks, name string) map[string]any {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	res, ok := m.resources[name]
	require.True(t, ok, "resource %s missing", name)
	return res.Inputs.Mappable()
}

func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	cur := v
	for _, p := range path {
		switch key := p.(type) {
		case string:
			obj, ok := cur.(map[string]any)
			require.True(t, ok, "not an object at %v", key)
			cur = obj[key]
		case int:
			arr, ok := cur.([]any)
			require.True(t, ok, "not an array at %d", key)
			require.Greater(t, len(arr), key)
			cur = arr[key]
		}
	}
	return cur
}

func podSpec(t *testing.T, m *resourceMocks) map[string]any {
	t.Helper()
	spec, ok := dig(t, mapped(t, m, "filestash"), "spec", "template", "spec").(map[string]any)
	require.True(t, ok)
	return spec
}

func initContainer(t *testing.T, spec map[string]any, name string) map[string]any {
	t.Helper()
	inits, ok := spec["initContainers"].([]any)
	require.True(t, ok)
	for _, c := range inits {
		cm, isMap := c.(map[string]any)
		require.True(t, isMap)
		if cm["name"] == name {
			return cm
		}
	}
	require.Failf(t, "init container missing", "%s", name)
	return nil
}

func TestDeploymentShape(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	dep := mapped(t, m, "filestash")
	require.EqualValues(t, 1, dig(t, dep, "spec", "replicas"))
	require.Equal(t, "Recreate", dig(t, dep, "spec", "strategy", "type"))
	spec := podSpec(t, m)
	require.Equal(t, true, dig(t, spec, "securityContext", "runAsNonRoot"))
	main := dig(t, spec, "containers", 0)
	require.Equal(t, "machines/filestash:latest@"+testDigest, dig(t, main, "image"))
	require.EqualValues(t, 8334, dig(t, main, "ports", 0, "containerPort"))
	require.Equal(t, "/healthz", dig(t, main, "readinessProbe", "httpGet", "path"))
	require.Equal(t, "/healthz", dig(t, main, "livenessProbe", "httpGet", "path"))
	require.Contains(t, fmt.Sprint(dig(t, main, "volumeMounts")), "/app/data/state")
	require.Contains(t, fmt.Sprint(spec["volumes"]), "filestash-state")
}

func TestDeploymentEnv(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	env, ok := dig(t, podSpec(t, m), "containers", 0, "env").([]any)
	require.True(t, ok)
	byName := map[string]map[string]any{}
	for _, e := range env {
		em, isMap := e.(map[string]any)
		require.True(t, isMap)
		name, isStr := em["name"].(string)
		require.True(t, isStr)
		byName[name] = em
	}
	for _, key := range []string{"ADMIN_PASSWORD", "CONFIG_SECRET"} {
		require.NotContains(t, byName[key], "value", "%s must not hold a literal", key)
		require.Equal(t, "filestash", dig(t, byName[key], "valueFrom", "secretKeyRef", "name"))
		require.Equal(t, key, dig(t, byName[key], "valueFrom", "secretKeyRef", "key"))
	}
	require.Equal(t, "https://"+testHost, byName["APPLICATION_URL"]["value"])
}

func TestInitContainerSeedsOnlyWhenAbsent(t *testing.T) {
	t.Parallel()
	seed := initContainer(t, podSpec(t, install(t, validConfig())), "seed")
	cmd := fmt.Sprint(seed["command"])
	require.Contains(t, cmd, "[ -e /app/data/state/config/config.json ] ||")
	require.Contains(t, cmd, "cp /seed/config.json /app/data/state/config/config.json")
}

func seedJSON(t *testing.T, m *resourceMocks) map[string]any {
	t.Helper()
	raw, ok := dig(t, mapped(t, m, "filestash-seed"), "data", "config.json").(string)
	require.True(t, ok)
	var seed map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &seed))
	return seed
}

func TestSeedHasNoCredentials(t *testing.T) {
	t.Parallel()
	seed := seedJSON(t, install(t, validConfig()))
	conns, ok := seed["connections"].([]any)
	require.True(t, ok)
	require.Len(t, conns, 1)
	conn, ok := conns[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "s3", conn["type"])
	require.Equal(t, "http://minio.dev.svc.cluster.local:9000", conn["endpoint"])
	require.Equal(t, "us-east-1", conn["region"])
	require.NotContains(t, conn, "access_key_id")
	require.NotContains(t, conn, "secret_access_key")
}

func TestServicePort(t *testing.T) {
	t.Parallel()
	svc := mapped(t, install(t, validConfig()), "filestash-svc")
	require.Equal(t, "filestash", dig(t, svc, "metadata", "name"))
	require.EqualValues(t, 8334, dig(t, svc, "spec", "ports", 0, "port"))
	require.Equal(t, "ClusterIP", dig(t, svc, "spec", "type"))
}

func TestPVCSize(t *testing.T) {
	t.Parallel()
	pvc := mapped(t, install(t, validConfig()), "filestash-state")
	require.Equal(t, "2Gi", dig(t, pvc, "spec", "resources", "requests", "storage"))
	require.Equal(t, "dictycr-balanced", dig(t, pvc, "spec", "storageClassName"))
	require.Equal(t, "ReadWriteOnce", dig(t, pvc, "spec", "accessModes", 0))
}

func TestCacheEmptyDir(t *testing.T) {
	t.Parallel()
	vols, ok := podSpec(t, install(t, validConfig()))["volumes"].([]any)
	require.True(t, ok)
	found := false
	for _, v := range vols {
		vm, isMap := v.(map[string]any)
		require.True(t, isMap)
		if vm["name"] == "cache" {
			found = true
			require.Equal(t, "2Gi", dig(t, vm, "emptyDir", "sizeLimit"))
		}
	}
	require.True(t, found)
}

func ingressConfig() *FilestashConfig {
	cfg := validConfig()
	cfg.Ingress.Enabled = true
	cfg.Ingress.Hosts = []string{testHost}
	cfg.Ingress.TLSSecret = testTLS
	cfg.Ingress.Label = LabelConfig{Name: "kcert.dev/ingress", Value: "managed"}
	return cfg
}

func TestIngressModeExperiments(t *testing.T) {
	t.Parallel()
	m := install(t, ingressConfig())
	ing := mapped(t, m, "filestash-ingress")
	ann := dig(t, ing, "metadata", "annotations")
	for key, want := range map[string]string{
		"nginx.ingress.kubernetes.io/proxy-body-size":         "0",
		"nginx.ingress.kubernetes.io/proxy-request-buffering": "off",
		"nginx.ingress.kubernetes.io/proxy-read-timeout":      "3600",
		"nginx.ingress.kubernetes.io/proxy-send-timeout":      "3600",
	} {
		require.Equal(t, want, dig(t, ann, key))
	}
	require.Equal(t, "managed", dig(t, ing, "metadata", "labels", "kcert.dev/ingress"))
	require.Equal(t, "filestash", dig(t, ing, "spec", "rules", 0, "http", "paths", 0, "backend", "service", "name"))

	block := mapped(t, m, "filestash-admin-block")
	require.Equal(t, "/", dig(t, block, "metadata", "annotations", "nginx.ingress.kubernetes.io/permanent-redirect"))
	path := dig(t, block, "spec", "rules", 0, "http", "paths", 0)
	require.Equal(t, "/admin", dig(t, path, "path"))
	require.Equal(t, "Prefix", dig(t, path, "pathType"))
}

func TestNoIngressWhenDisabled(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotContains(t, m.resources, "filestash-ingress")
	require.NotContains(t, m.resources, "filestash-admin-block")
}

func initNames(t *testing.T, spec map[string]any) []string {
	t.Helper()
	inits, ok := spec["initContainers"].([]any)
	require.True(t, ok)
	names := make([]string, 0, len(inits))
	for _, c := range inits {
		cm, isMap := c.(map[string]any)
		require.True(t, isMap)
		name, isStr := cm["name"].(string)
		require.True(t, isStr)
		names = append(names, name)
	}
	return names
}

func TestPluginInitContainerOrder(t *testing.T) {
	t.Parallel()
	spec := podSpec(t, install(t, validConfig()))
	require.Equal(t, []string{"plugins", "seed"}, initNames(t, spec))
	plugins := initContainer(t, spec, "plugins")
	require.Equal(t, "dictybase/filestash-plugins:2026.11.17@"+testDigest, plugins["image"])
}

func TestPluginInitReplacesZips(t *testing.T) {
	t.Parallel()
	plugins := initContainer(t, podSpec(t, install(t, validConfig())), "plugins")
	cmd := fmt.Sprint(plugins["command"])
	del := strings.Index(cmd, "rm -f /app/data/state/plugins/*.zip")
	cp := strings.Index(cmd, "cp /plugins/*.zip /app/data/state/plugins/")
	require.GreaterOrEqual(t, del, 0)
	require.Greater(t, cp, del, "delete must come before copy")
}

func TestSeedDisablesConsole(t *testing.T) {
	t.Parallel()
	seed := seedJSON(t, install(t, validConfig()))
	require.Equal(t, false, dig(t, seed, "features", "server", "console_enable"))
	require.EqualValues(t, 5, dig(t, seed, "features", "search", "explore_timeout"))
	require.Equal(t, false, dig(t, seed, "log", "telemetry"))
}

func TestSecretsFromStackConfig(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	sec := mapped(t, m, "filestash-secret")
	require.Equal(t, "filestash", dig(t, sec, "metadata", "name"))
	require.Equal(t, "filestash", dig(t, sec, "metadata", "labels", "app.kubernetes.io/name"))
	data := fmt.Sprint(dig(t, sec, "stringData"))
	require.Contains(t, data, "ADMIN_PASSWORD")
	require.Contains(t, data, "CONFIG_SECRET")
	users := fmt.Sprint(dig(t, mapped(t, m, "filestash-users"), "stringData"))
	for _, role := range userRoles {
		require.Contains(t, users, role+"AccessKey")
		require.Contains(t, users, role+"SecretKey")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	require.True(t, m.secrets["filestash-secret"], "Secret filestash must be a Pulumi secret output")
	require.True(t, m.secrets["filestash-users"], "Secret filestash-users must be a Pulumi secret output")
}

func TestVeleroExcludesState(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	pvc := mapped(t, m, "filestash-state")
	require.Equal(t, "true", dig(t, pvc, "metadata", "labels", "velero.io/exclude-from-backup"))
	ann := dig(t, mapped(t, m, "filestash"), "spec", "template", "metadata", "annotations")
	require.Equal(t, "state,cache", dig(t, ann, "backup.velero.io/backup-volumes-excludes"))
}

func TestNoStateBackupInThisStack(t *testing.T) {
	t.Parallel()
	m := install(t, validConfig())
	m.mu.Lock()
	defer m.mu.Unlock()
	require.NotContains(t, m.resources, "filestash-state-backup", "the filestash-backup stack owns the backup")
}
