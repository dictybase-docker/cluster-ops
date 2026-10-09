package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"cloud.google.com/go/storage"
	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/toolchain"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	detailEnvUnset     = "unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>"
	detailNotRunEnv    = "not run — CLUSTER_NAME/PROJECT_ID unset"
	detailNotRunBundle = "not run — manifest bundle missing"
	nameClus           = "CLUSTER_NAME"
	nameProj           = "PROJECT_ID"
)

var templateVarRE = regexp.MustCompile(`\$\{[A-Za-z0-9_]+\}`)

// CreateProbes injects every external read (the LoginReader pattern) so the
// whole preflight-create gate is testable without binaries or a live GCP.
type CreateProbes struct {
	LookPath            func(bin string) (string, error)
	RunCapture          func(name string, args ...string) (string, error)
	ConfigValue         func(key string) (string, error) // gcloud config get-value <key>
	Stat                func(path string) (fs.FileInfo, error)
	RealPath            func(path string) (string, error) // filepath.EvalSymlinks
	ReadFile            func(path string) ([]byte, error)
	DescribeImage       func(ctx context.Context, project, image string) (bool, error)
	DescribeImageFamily func(ctx context.Context, project, family string) (string, error)
	ListBucket          func(ctx context.Context, uri string) (bool, error)
	ListBucketPath      func(ctx context.Context, uri string) (bool, error)
	ListInstances       func(ctx context.Context, filter string) ([]string, error)
	GitClean            func(dir string) (string, error) // "" clean; "uncommitted"|"staged"|"untracked"
}

// CreateEnv is the resolved operator environment (fields, never ambient reads).
type CreateEnv struct {
	Cluster, Project, KopsName, State, BucketURI, SSHPub, CredFile string
	ActiveConfig, ExpectedSA, PinFile, Root                        string
}

// CreateConfig selects the preflight-create gate.
type CreateConfig struct {
	Env    CreateEnv
	Probes CreateProbes
}

// CheckCreate returns the 12 preflight-create checks in the recipe's order:
// env, toolchain, pins, credentials, shell isolation, SSH keypair, manifest
// bundle, node images, GCE taints, git status, state store. Everything is
// reported; when CLUSTER_NAME/PROJECT_ID are unset the later groups report
// WARN "not run" instead of guessing.
func CheckCreate(cfg CreateConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		checkEnvName(cfg),
		checkEnvProject(cfg),
		checkToolchain(cfg),
		checkPins(cfg),
		checkCredentials(cfg),
		checkShellIsolation(cfg),
		checkSSHKey(cfg),
		checkManifests(cfg),
		checkNodeImages(cfg),
		checkTaints(cfg),
		checkGit(cfg),
		checkStateStore(cfg),
	}
}

func envReady(env CreateEnv) bool { return env.Cluster != "" && env.Project != "" }

func notRunEnv(name string) preflight.Result {
	return preflight.Result{Name: name, Detail: detailNotRunEnv, Status: preflight.StatusWarn}
}

func checkEnvName(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.Env.Cluster == "" {
			return preflight.Result{Name: nameClus, Detail: detailEnvUnset, Status: preflight.StatusFail}
		}
		return preflight.Result{Name: nameClus, Detail: cfg.Env.Cluster, Status: preflight.StatusPass}
	}
}

func checkEnvProject(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.Env.Project == "" {
			return preflight.Result{Name: nameProj, Detail: detailEnvUnset, Status: preflight.StatusFail}
		}
		return preflight.Result{Name: nameProj, Detail: cfg.Env.Project, Status: preflight.StatusPass}
	}
}

func checkToolchain(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "toolchain binaries"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		checks := toolchain.CheckTools(toolchain.ToolsConfig{
			LookPath: cfg.Probes.LookPath,
			Version:  func(string) (string, error) { return "", nil },
		})
		for _, r := range preflight.Run(checks) {
			if r.Status == preflight.StatusFail {
				return preflight.Result{
					Name: name, Detail: "one or more tools missing on PATH — run 'just prepare-tools'",
					Status: preflight.StatusFail,
				}
			}
		}
		return preflight.Result{
			Name: name, Detail: "all required tools present on PATH", Status: preflight.StatusPass,
		}
	}
}

func checkPins(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "toolchain pins"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		data, err := cfg.Probes.ReadFile(cfg.Env.PinFile)
		if err != nil {
			return preflight.Result{
				Name: name, Detail: "no pin file " + cfg.Env.PinFile + " — pins not checked",
				Status: preflight.StatusInfo,
			}
		}
		mismatches := []string{}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
				continue
			}
			tool, pinned := fields[0], fields[1]
			cur, installed := asdfCurrent(cfg.Probes, tool)
			if installed != "true" || cur != pinned {
				mismatches = append(mismatches, fmt.Sprintf(
					"tool pin %s installed '%s' != pinned '%s' in %s",
					tool, cur, pinned, cfg.Env.PinFile))
			}
		}
		if len(mismatches) > 0 {
			return preflight.Result{
				Name: name, Detail: strings.Join(mismatches, "; "),
				Fix:    "run 'just prepare-tools'",
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name:   name,
			Detail: "all tools match pinned versions in " + cfg.Env.PinFile,
			Status: preflight.StatusPass,
		}
	}
}

// asdfCurrent parses `asdf current <tool>` output (display parsing only): the
// last line's second field is the running version, its last field is the
// "installed" marker.
func asdfCurrent(probes CreateProbes, tool string) (cur, installed string) {
	out, err := probes.RunCapture("asdf", "current", tool)
	if err != nil {
		return "", ""
	}
	var fields []string
	for line := range strings.SplitSeq(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			fields = f
		}
	}
	if len(fields) < 2 {
		return "", ""
	}
	return fields[1], fields[len(fields)-1]
}

func checkCredentials(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "credentials"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		if cfg.Env.CredFile == "" {
			return preflight.Result{
				Name: name, Detail: "GOOGLE_APPLICATION_CREDENTIALS unset", Status: preflight.StatusFail,
			}
		}
		if _, err := cfg.Probes.Stat(cfg.Env.CredFile); err != nil {
			return preflight.Result{
				Name: name, Detail: "key file not found: " + cfg.Env.CredFile, Status: preflight.StatusFail,
			}
		}
		data, err := cfg.Probes.ReadFile(cfg.Env.CredFile)
		if err != nil {
			return preflight.Result{
				Name: name, Detail: "key file not readable: " + cfg.Env.CredFile, Status: preflight.StatusFail,
			}
		}
		var key struct {
			ProjectID   string `json:"project_id"`
			ClientEmail string `json:"client_email"`
		}
		if err := json.Unmarshal(data, &key); err != nil {
			return preflight.Result{
				Name: name, Detail: "key file not valid JSON: " + cfg.Env.CredFile, Status: preflight.StatusFail,
			}
		}
		if key.ProjectID != cfg.Env.Project {
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("key project (%s) != PROJECT_ID (%s)", key.ProjectID, cfg.Env.Project),
				Status: preflight.StatusFail,
			}
		}
		if key.ClientEmail != cfg.Env.ExpectedSA {
			return preflight.Result{
				Name: name,
				Detail: fmt.Sprintf("SA is '%s', expected '%s' — rotate first with 'just gcp-cluster rotate-to-creator'",
					key.ClientEmail, cfg.Env.ExpectedSA),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: name, Detail: key.ClientEmail, Status: preflight.StatusPass}
	}
}

func checkShellIsolation(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if !envReady(cfg.Env) {
			return notRunEnv("shell isolation")
		}
		if cfg.Env.ActiveConfig == "" {
			return preflight.Result{
				Name: "shell isolation", Detail: detailEnvUnset, Status: preflight.StatusFail,
			}
		}
		if cfg.Env.ActiveConfig != activeConfigName(cfg.Env.Project) {
			return preflight.Result{
				Name: "gcloud config",
				Detail: fmt.Sprintf(
					"CLOUDSDK_ACTIVE_CONFIG_NAME is '%s', expected '%s'",
					cfg.Env.ActiveConfig, activeConfigName(cfg.Env.Project)),
				Status: preflight.StatusFail,
			}
		}
		project, _ := cfg.Probes.ConfigValue("project")
		account, _ := cfg.Probes.ConfigValue("account")
		project, account = strings.TrimSpace(project), strings.TrimSpace(account)
		if project != cfg.Env.Project {
			return preflight.Result{
				Name:   "gcloud project",
				Detail: fmt.Sprintf("gcloud project is '%s', expected '%s'", project, cfg.Env.Project),
				Status: preflight.StatusFail,
			}
		}
		if account != cfg.Env.ExpectedSA {
			return preflight.Result{
				Name:   "gcloud account",
				Detail: fmt.Sprintf("gcloud account is '%s', expected '%s'", account, cfg.Env.ExpectedSA),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: "gcloud config", Detail: cfg.Env.ActiveConfig + " (" + account + ")",
			Status: preflight.StatusPass,
		}
	}
}

func activeConfigName(project string) string { return project + "-kops-cluster-creator" }

func checkSSHKey(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "SSH keypair"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		if !strings.HasSuffix(cfg.Env.SSHPub, ".pub") {
			return preflight.Result{
				Name:   "SSH public key",
				Detail: "must end in .pub: " + cfg.Env.SSHPub, Status: preflight.StatusFail,
			}
		}
		if _, err := cfg.Probes.Stat(cfg.Env.SSHPub); err != nil {
			return preflight.Result{
				Name: "SSH public key", Detail: "not found: " + cfg.Env.SSHPub, Status: preflight.StatusFail,
			}
		}
		dir := filepath.Dir(cfg.Env.SSHPub)
		expected := filepath.Join(cfg.Env.Root, "credentials", cfg.Env.Project)
		dirReal, dirErr := cfg.Probes.RealPath(dir)
		expReal, expErr := cfg.Probes.RealPath(expected)
		if dirErr != nil || expErr != nil || filepath.Clean(dirReal) != filepath.Clean(expReal) {
			return preflight.Result{
				Name:   "SSH key isolation",
				Detail: fmt.Sprintf("parent directory (%s) != expected (credentials/%s)", dir, cfg.Env.Project),
				Status: preflight.StatusFail,
			}
		}
		priv := strings.TrimSuffix(cfg.Env.SSHPub, ".pub")
		if _, err := cfg.Probes.Stat(priv); err != nil {
			return preflight.Result{
				Name:   "SSH private key",
				Detail: "matching private key not found: " + priv, Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: name, Detail: cfg.Env.SSHPub, Status: preflight.StatusPass}
	}
}

// manifestData loads both bundle files (empty byte slices when unreadable).
func manifestData(cfg CreateConfig) (cluster, igs []byte, ok bool) {
	cl, err := cfg.Probes.ReadFile(cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster + "/cluster.yaml")
	ig, err2 := cfg.Probes.ReadFile(cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster + "/instancegroups.yaml")
	return cl, ig, err == nil && err2 == nil
}

//nolint:cyclop // linear if/elif chain mirroring the recipe's check order
func checkManifests(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if !envReady(cfg.Env) {
			return notRunEnv("manifests")
		}
		bundle := cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster
		cl, err := cfg.Probes.ReadFile(bundle + "/cluster.yaml")
		if err != nil {
			return preflight.Result{
				Name:   "manifests",
				Detail: fmt.Sprintf("missing %s/cluster.yaml — run 'just gcp-cluster bootstrap-bundle' first", bundle),
				Status: preflight.StatusFail,
			}
		}
		ig, err := cfg.Probes.ReadFile(bundle + "/instancegroups.yaml")
		if err != nil {
			return preflight.Result{
				Name:   "manifests",
				Detail: fmt.Sprintf("missing %s/instancegroups.yaml — run 'just gcp-cluster bootstrap-bundle' first", bundle),
				Status: preflight.StatusFail,
			}
		}
		if project := manifestField(cl, "project"); project != cfg.Env.Project {
			return preflight.Result{
				Name:   "manifest project",
				Detail: fmt.Sprintf("cluster.yaml project (%s) != PROJECT_ID (%s)", project, cfg.Env.Project),
				Status: preflight.StatusFail,
			}
		}
		if name := manifestField(cl, "name"); name != cfg.Env.KopsName {
			return preflight.Result{
				Name:   "manifest name",
				Detail: fmt.Sprintf("cluster.yaml metadata.name (%s) != %s", name, cfg.Env.KopsName),
				Status: preflight.StatusFail,
			}
		}
		if cfg.Env.State != "" {
			base := manifestField(cl, "configBase")
			if base != cfg.Env.State+"/"+cfg.Env.KopsName && base != cfg.Env.State {
				return preflight.Result{
					Name:   "manifest state",
					Detail: fmt.Sprintf("cluster.yaml configBase (%s) != %s/%s", base, cfg.Env.State, cfg.Env.KopsName),
					Status: preflight.StatusFail,
				}
			}
		}
		if templateVarRE.Match(cl) || templateVarRE.Match(ig) {
			return preflight.Result{
				Name:   "manifest template",
				Detail: "unresolved ${VAR} placeholders found in bundle", Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name:   "manifest bundle",
			Detail: fmt.Sprintf("config/kops/%s/ (%s)", cfg.Env.Cluster, cfg.Env.KopsName),
			Status: preflight.StatusPass,
		}
	}
}

// manifestField reads the first `key: value` line, quotes stripped.
func manifestField(data []byte, key string) string {
	prefix := key + ":"
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), `"'`)
	}
	return ""
}

// manifestImages lists the unique `image:` values in instancegroups.yaml.
func manifestImages(data []byte) []string {
	seen := map[string]bool{}
	images := []string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "image:") {
			continue
		}
		img := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "image:")), `"'`)
		if img != "" && !seen[img] {
			seen[img] = true
			images = append(images, img)
		}
	}
	return images
}

func checkNodeImages(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "node image"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		bundle := cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster
		ig, err := cfg.Probes.ReadFile(bundle + "/instancegroups.yaml")
		if err != nil {
			return preflight.Result{
				Name: name, Detail: detailNotRunBundle, Status: preflight.StatusWarn,
			}
		}
		problems := []string{}
		okImages := []string{}
		for _, img := range manifestImages(ig) {
			project, image, found := strings.Cut(img, "/")
			if !found {
				problems = append(problems, "image not found: image '"+img+"' not found in GCP project ''")
				continue
			}
			exists, err := cfg.Probes.DescribeImage(context.Background(), project, image)
			if err != nil {
				problems = append(problems, fmt.Sprintf("image check failed: '%s': %v", img, err))
				continue
			}
			if exists {
				okImages = append(okImages, img)
				continue
			}
			// Family resolution runs only after a confirmed 404.
			resolved, ferr := cfg.Probes.DescribeImageFamily(context.Background(), project, image)
			if ferr == nil && resolved != "" {
				problems = append(problems, fmt.Sprintf(
					"image family: '%s' is an image family — pin exact image '%s/%s'", img, project, resolved))
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"image not found: image '%s' not found in GCP project '%s'", img, project))
		}
		if len(problems) > 0 {
			return preflight.Result{
				Name: name, Detail: strings.Join(problems, "; "), Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: name, Detail: strings.Join(okImages, ", "), Status: preflight.StatusPass,
		}
	}
}

func checkTaints(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "GCE taints"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		bundle := cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster
		ig, err := cfg.Probes.ReadFile(bundle + "/instancegroups.yaml")
		if err != nil {
			return preflight.Result{
				Name: name, Detail: detailNotRunBundle, Status: preflight.StatusWarn,
			}
		}
		if strings.Contains(string(ig), "taints:") {
			return preflight.Result{
				Name: name,
				Detail: fmt.Sprintf("spec.taints found in %s/instancegroups.yaml — "+
					"GCE rejects autoscaler taint labels containing slashes; "+
					"use nodeLabels instead", bundle),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: name, Detail: "no spec.taints in instancegroups.yaml", Status: preflight.StatusPass,
		}
	}
}

func checkGit(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "git status"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		if _, _, ok := manifestData(cfg); !ok {
			return preflight.Result{
				Name: name, Detail: detailNotRunBundle, Status: preflight.StatusWarn,
			}
		}
		dirty, err := cfg.Probes.GitClean(cfg.Env.Root + "/config/kops/" + cfg.Env.Cluster)
		if err != nil {
			return preflight.Result{
				Name: name, Detail: "git status error: " + err.Error(), Status: preflight.StatusFail,
			}
		}
		switch dirty {
		case "uncommitted":
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("uncommitted modifications in config/kops/%s/ — review and commit first", cfg.Env.Cluster),
				Status: preflight.StatusFail,
			}
		case "staged":
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("staged uncommitted changes in config/kops/%s/ — commit first", cfg.Env.Cluster),
				Status: preflight.StatusFail,
			}
		case "untracked":
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("untracked files in config/kops/%s/ — commit or clean first", cfg.Env.Cluster),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: name, Detail: "manifests committed and clean in Git", Status: preflight.StatusPass,
		}
	}
}

func checkStateStore(cfg CreateConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		name := "state store"
		if !envReady(cfg.Env) {
			return notRunEnv(name)
		}
		uri := cfg.Env.BucketURI
		exists, err := cfg.Probes.ListBucket(context.Background(), uri)
		if err != nil {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("error querying bucket %s: %v", uri, err), Status: preflight.StatusFail,
			}
		}
		if !exists {
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("bucket does not exist yet (%s will be created by create-cluster)", uri),
				Status: preflight.StatusPass,
			}
		}
		inState, err := cfg.Probes.ListBucketPath(context.Background(), uri+"/"+cfg.Env.KopsName+"/")
		if err != nil {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("error checking cluster in %s: %v", uri, err), Status: preflight.StatusFail,
			}
		}
		if !inState {
			return preflight.Result{
				Name:   name,
				Detail: fmt.Sprintf("bucket exists (%s), no active cluster (clean for creation)", uri),
				Status: preflight.StatusPass,
			}
		}
		running, err := cfg.Probes.ListInstances(context.Background(), "name ~ "+cfg.Env.Cluster)
		if err != nil {
			return preflight.Result{
				Name: name, Detail: fmt.Sprintf("error checking cluster in %s: %v", uri, err), Status: preflight.StatusFail,
			}
		}
		if len(running) > 0 {
			return preflight.Result{
				Name: name,
				Detail: fmt.Sprintf("cluster '%s' already has running VMs in GCP — "+
					"use 'just gcp-cluster apply-cluster' for updates, or 'delete-cluster' first",
					cfg.Env.KopsName),
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name: name,
			Detail: fmt.Sprintf("bucket exists (%s), manifests in state, "+
				"compute not yet provisioned (clean to create/resume)", uri),
			Status: preflight.StatusPass,
		}
	}
}

// ---- production probe wiring (exec + GCP SDKs; ADC authenticated) ----

// DefaultCreateProbes wires the real readers. project is the GCP project the
// compute probes run against.
func DefaultCreateProbes(project string) CreateProbes {
	return CreateProbes{
		LookPath:            exec.LookPath,
		RunCapture:          runCapture,
		ConfigValue:         gcloudConfigValue,
		Stat:                os.Stat,
		RealPath:            filepath.EvalSymlinks,
		ReadFile:            os.ReadFile,
		DescribeImage:       describeImage,
		DescribeImageFamily: describeImageFamily,
		ListBucket:          listBucketExists,
		ListBucketPath:      listBucketPathExists,
		ListInstances:       listInstances(project),
		GitClean:            gitClean,
	}
}

// gcloudConfigValue reads gcloud's active config values — the one surface the
// GCP SDKs do not expose (active config name/account live in gcloud's config).
func gcloudConfigValue(key string) (string, error) {
	return runCapture("gcloud", "config", "get-value", key)
}

func runCapture(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output() //nolint:gosec // G204: argv comes from fixed probe shapes
	return string(out), err
}

// isNotFound classifies SDK 404s by code (never by message).
func isNotFound(err error) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == http.StatusNotFound
}

func listBucketExists(ctx context.Context, uri string) (bool, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return false, err
	}
	defer client.Close()
	name := strings.TrimSuffix(strings.TrimPrefix(uri, "gs://"), "/")
	if _, err := client.Bucket(name).Attrs(ctx); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// bucketAndPrefix splits a gs:// URI into bucket and object prefix, keeping
// the prefix's trailing slash so a sibling directory (kn-backup/) cannot
// satisfy a kn/ lookup.
func bucketAndPrefix(uri string) (bucket, prefix string) {
	bucket, prefix, _ = strings.Cut(strings.TrimPrefix(uri, "gs://"), "/")
	return bucket, prefix
}

func listBucketPathExists(ctx context.Context, uri string) (bool, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return false, err
	}
	defer client.Close()
	bucket, prefix := bucketAndPrefix(uri)
	it := client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix})
	if _, err := it.Next(); err != nil {
		if errors.Is(err, iterator.Done) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func listInstances(project string) func(context.Context, string) ([]string, error) {
	return func(ctx context.Context, filter string) ([]string, error) {
		svc, err := compute.NewService(ctx, option.WithScopes(compute.ComputeScope))
		if err != nil {
			return nil, err
		}
		names := []string{}
		call := svc.Instances.AggregatedList(project).Filter(filter)
		if err := call.Pages(ctx, func(page *compute.InstanceAggregatedList) error {
			for _, scoped := range page.Items {
				for _, inst := range scoped.Instances {
					names = append(names, inst.Name)
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		return names, nil
	}
}

func describeImage(ctx context.Context, project, image string) (bool, error) {
	svc, err := compute.NewService(ctx, option.WithScopes(compute.ComputeScope))
	if err != nil {
		return false, err
	}
	if _, err := svc.Images.Get(project, image).Context(ctx).Do(); err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func describeImageFamily(ctx context.Context, project, family string) (string, error) {
	svc, err := compute.NewService(ctx, option.WithScopes(compute.ComputeScope))
	if err != nil {
		return "", err
	}
	img, err := svc.Images.GetFromFamily(project, family).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return img.Name, nil
}

// gitClean probes the bundle path with the recipe's three git checks:
// unstaged modifications, staged changes, untracked files (first match wins).
func gitClean(path string) (string, error) {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain", "--", ".").Output()
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		switch {
		case line[0] != '?' && line[0] != ' ':
			return "uncommitted", nil
		case line[1] != '?' && line[1] != ' ':
			return "staged", nil
		case strings.HasPrefix(line, "??"):
			return "untracked", nil
		}
	}
	return "", nil
}

// CreateEnvInputs are the resolved boundary values (flags + environment).
type CreateEnvInputs struct {
	Cluster, Project, KopsName, State, BucketURI, SSHPub, CredFile string
	ActiveConfig                                                   string // $CLOUDSDK_ACTIVE_CONFIG_NAME (actual)
	PinFile, Root                                                  string
}

// BuildCreateEnv assembles CreateEnv from resolved inputs. ActiveConfig stays
// the operator's real value: the shell-isolation check compares actual vs
// expected, so synthesizing it here would make the check unable to fail.
func BuildCreateEnv(in CreateEnvInputs) CreateEnv {
	return CreateEnv{
		Cluster: in.Cluster, Project: in.Project, KopsName: in.KopsName,
		State: in.State, BucketURI: in.BucketURI, SSHPub: in.SSHPub,
		CredFile:     in.CredFile,
		ActiveConfig: in.ActiveConfig,
		ExpectedSA:   "kops-cluster-creator@" + in.Project + ".iam.gserviceaccount.com",
		PinFile:      in.PinFile, Root: in.Root,
	}
}
