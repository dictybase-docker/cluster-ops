package cluster

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/stretchr/testify/require"
)

// TEST CREDENTIAL, not real — every fixture value below is synthetic.

func greenEnv() CreateEnv {
	return CreateEnv{ //nolint:gosec // G101: synthetic fixture values (TEST CREDENTIAL, not real)
		Cluster: testCluster, Project: "proj-dcr", KopsName: "dcr-k8s.local",
		State: "gs://kops-state-dcr", BucketURI: "gs://kops-state-dcr",
		SSHPub: "/repo/credentials/proj-dcr/k8sVM.pub", CredFile: "/creds/key.json",
		ActiveConfig: "proj-dcr-kops-cluster-creator",
		ExpectedSA:   "kops-cluster-creator@proj-dcr.iam.gserviceaccount.com",
		PinFile:      "/repo/.tool-versions", Root: "/repo",
	}
}

const (
	fixtureManifest = "project: proj-dcr\nname: dcr-k8s.local\nconfigBase: gs://kops-state-dcr/dcr-k8s.local\n"
	fixtureIGS      = "spec:\n  image: proj-dcr/img-1\n"
	//nolint:gosec // G101: synthetic fixture, not a real credential
	fixtureCred = `{"project_id":"proj-dcr","client_email":"kops-cluster-creator@proj-dcr.iam.gserviceaccount.com"}`
)

//nolint:cyclop // fixture switch covering every probe shape
func greenProbes() CreateProbes {
	return CreateProbes{
		LookPath: func(string) (string, error) { return "/usr/bin/tool", nil },
		RunCapture: func(name string, args ...string) (string, error) {
			switch name {
			case "asdf":
				return "kubectl 1.28.8 true\n", nil
			case "gcloud":
				if args[len(args)-1] == "account" {
					return "kops-cluster-creator@proj-dcr.iam.gserviceaccount.com\n", nil
				}
				return "proj-dcr\n", nil
			default:
				return "", nil
			}
		},
		Stat: func(path string) (fs.FileInfo, error) {
			if strings.Contains(path, "nope") {
				return nil, fs.ErrNotExist
			}
			return fakeInfo(path), nil
		},
		ReadFile: func(path string) ([]byte, error) {
			switch {
			case strings.HasSuffix(path, "cluster.yaml"):
				return []byte(fixtureManifest), nil
			case strings.HasSuffix(path, "instancegroups.yaml"):
				return []byte(fixtureIGS), nil
			case strings.HasSuffix(path, "k8sVM.pub"):
				return []byte("ssh-rsa AAAA TEST CREDENTIAL, not real"), nil
			case strings.HasSuffix(path, "key.json"):
				return []byte(fixtureCred), nil
			default:
				return []byte("kubectl 1.28.8\n"), nil // pinfile
			}
		},
		DescribeImage:       func(context.Context, string, string) (bool, error) { return true, nil },
		DescribeImageFamily: func(context.Context, string, string) (string, error) { return "", nil },
		ListBucket:          func(context.Context, string) (bool, error) { return false, nil },
		ListBucketPath:      func(context.Context, string) (bool, error) { return false, nil },
		ListInstances:       func(context.Context, string) ([]string, error) { return nil, nil },
		GitClean:            func(string) (string, error) { return "", nil },
	}
}

type fakeInfo string

func (f fakeInfo) Name() string       { return string(f) }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

func runCreate(env CreateEnv, probes CreateProbes) []preflight.Result {
	return preflight.Run(CheckCreate(CreateConfig{Env: env, Probes: probes}))
}

func statuses(rs []preflight.Result) (fails, warns int) {
	for _, r := range rs {
		switch r.Status {
		case preflight.StatusFail:
			fails++
		case preflight.StatusWarn:
			warns++
		}
	}
	return fails, warns
}

func TestCreateEnvMissing(t *testing.T) {
	got := runCreate(CreateEnv{}, greenProbes())
	require.Len(t, got, 12)
	hint := "unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>"
	require.Equal(t, hint, got[0].Detail)
	require.Equal(t, hint, got[1].Detail)
	fails, warns := statuses(got)
	require.Equal(t, 2, fails)
	require.Equal(t, 10, warns, "later groups report not run")
	require.Equal(t, "not run — CLUSTER_NAME/PROJECT_ID unset", got[2].Detail)
}

func TestCreateToolchainAndPins(t *testing.T) {
	probes := greenProbes()
	probes.LookPath = func(bin string) (string, error) {
		if bin == "kubectl" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + bin, nil
	}
	got := runCreate(greenEnv(), probes)
	require.Equal(t, "one or more tools missing on PATH — run 'just prepare-tools'", got[2].Detail)

	probes = greenProbes()
	probes.RunCapture = func(name string, args ...string) (string, error) {
		if name == "asdf" {
			return "kubectl 1.27.0 true\n", nil
		}
		return greenProbes().RunCapture(name, args...)
	}
	got = runCreate(greenEnv(), probes)
	require.Equal(t, preflight.StatusFail, got[3].Status)
	require.Contains(t, got[3].Name+" "+got[3].Detail,
		"tool pin kubectl installed '1.27.0' != pinned '1.28.8' in /repo/.tool-versions")
	require.Contains(t, got[3].Fix, "just prepare-tools")

	got = runCreate(greenEnv(), greenProbes())
	require.Equal(t, preflight.StatusPass, got[3].Status)
	require.Contains(t, got[3].Detail, "all tools match pinned versions in /repo/.tool-versions")
}

func TestCreateCredentials(t *testing.T) {
	got := runCreate(greenEnv(), greenProbes())
	require.Equal(t, preflight.StatusPass, got[4].Status)

	t.Run("missing key file", func(t *testing.T) {
		probes := greenProbes()
		probes.Stat = func(path string) (fs.FileInfo, error) {
			if strings.Contains(path, "key.json") {
				return nil, fs.ErrNotExist
			}
			return fakeInfo(path), nil
		}
		got := runCreate(greenEnv(), probes)
		require.Equal(t, preflight.StatusFail, got[4].Status)
		require.Contains(t, got[4].Detail, "key file not found:")
	})

	t.Run("wrong SA", func(t *testing.T) {
		probes := greenProbes()
		probes.ReadFile = func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "key.json") {
				return []byte(`{"project_id":"proj-dcr","client_email":"someoneelse@proj-dcr.iam.gserviceaccount.com"}`), nil
			}
			return greenProbes().ReadFile(path)
		}
		got := runCreate(greenEnv(), probes)
		require.Equal(t, preflight.StatusFail, got[4].Status)
		require.Contains(t, got[4].Detail, "rotate first with 'just gcp-cluster rotate-to-creator'")
	})

	t.Run("unset", func(t *testing.T) {
		env := greenEnv()
		env.CredFile = ""
		got := runCreate(env, greenProbes())
		require.Equal(t, preflight.StatusFail, got[4].Status)
		require.Equal(t, "GOOGLE_APPLICATION_CREDENTIALS unset", got[4].Detail)
	})
}

func TestCreateStateStore(t *testing.T) {
	cases := []struct {
		name     string
		bucket   bool
		path     bool
		running  []string
		wantStat preflight.Status
		want     string
	}{
		{"bucket + manifests + running VMs", true, true,
			[]string{"vm-1"}, preflight.StatusFail, "already has running VMs in GCP"},
		{"bucket + manifests, clean", true, true,
			nil, preflight.StatusPass, "manifests in state, compute not yet provisioned"},
		{"bucket, no cluster", true, false, nil, preflight.StatusPass, "no active cluster"},
		{"bucket absent", false, false, nil, preflight.StatusPass, "will be created by create-cluster"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probes := greenProbes()
			probes.ListBucket = func(context.Context, string) (bool, error) { return tc.bucket, nil }
			probes.ListBucketPath = func(context.Context, string) (bool, error) { return tc.path, nil }
			probes.ListInstances = func(context.Context, string) ([]string, error) {
				return tc.running, nil
			}
			got := runCreate(greenEnv(), probes)
			require.Equal(t, tc.wantStat, got[11].Status, got[11].Detail)
			require.Contains(t, got[11].Detail, tc.want)
		})
	}
}

func TestCreateProbeForbidden(t *testing.T) {
	probes := greenProbes()
	probes.ListBucket = func(context.Context, string) (bool, error) {
		return false, errors.New("forbidden")
	}
	got := runCreate(greenEnv(), probes)
	require.Equal(t, preflight.StatusFail, got[11].Status)
	require.Contains(t, got[11].Detail, "error querying bucket")
}

func TestCreateAllGreen(t *testing.T) {
	got := runCreate(greenEnv(), greenProbes())
	fails, warns := statuses(got)
	require.Zero(t, fails, "all green")
	require.Zero(t, warns)
	require.Len(t, got, 12)
}

func TestCreateNodeImages(t *testing.T) {
	probes := greenProbes()
	probes.DescribeImage = func(context.Context, string, string) (bool, error) { return false, nil }
	probes.DescribeImageFamily = func(context.Context, string, string) (string, error) {
		return "img-1-resolved", nil
	}
	got := runCreate(greenEnv(), probes)
	require.Equal(t, preflight.StatusFail, got[8].Status)
	require.Contains(t, got[8].Detail,
		"is an image family — pin exact image 'proj-dcr/img-1-resolved'")

	probes.DescribeImageFamily = func(context.Context, string, string) (string, error) {
		return "", errors.New("not found")
	}
	got = runCreate(greenEnv(), probes)
	require.Equal(t, preflight.StatusFail, got[8].Status)
	require.Contains(t, got[8].Detail,
		"image not found: image 'proj-dcr/img-1' not found in GCP project 'proj-dcr'")
}
