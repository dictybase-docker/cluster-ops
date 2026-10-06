package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"cloud.google.com/go/storage"
	Arr "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	P "github.com/IBM/fp-go/v2/pair"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/option"
)

const (
	pulumiLoginCheckName = "Pulumi login"
	// fixPulumiGCSSetup is the remedy for every state-backend failure.
	fixPulumiGCSSetup = "run 'just gcp-pulumi pulumi-gcs-setup'"
	fixClusterEnv     = "enter the cluster shell with 'just cluster-env'"
)

// PulumiBackendConfig is the cluster shell's Pulumi wiring, read once from
// the PULUMI_* variables by the CLI.
type PulumiBackendConfig struct {
	CredsPath       string // PULUMI_GCP_CREDENTIALS
	SecretsProvider string // PULUMI_SECRET_PROVIDER
	BackendURL      string // PULUMI_BACKEND_URL
	Stack           string // PULUMI_STACK
}

// LoginReader returns the backend URL of the active `pulumi login`.
type LoginReader func() IOE.IOEither[error, string]

// PulumiChecks is the pulumi-backend gate: env, manager key, state bucket,
// secrets-provider key, active login. Checks whose input is unset report
// INFO "skipped" — the env check already fails for it.
func PulumiChecks(cfg PulumiBackendConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckEnvVars(cfg),
		CheckManagerKey(cfg),
		CheckBucketVersioning(cfg),
		CheckSecretsProviderKey(cfg),
		CheckPulumiLogin(cfg, PulumiWhoami),
	}
}

// CheckEnvVars verifies every PULUMI_* value is set.
func CheckEnvVars(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	missing := F.Pipe2(
		[]P.Pair[string, string]{
			P.MakePair("PULUMI_GCP_CREDENTIALS", cfg.CredsPath),
			P.MakePair("PULUMI_SECRET_PROVIDER", cfg.SecretsProvider),
			P.MakePair("PULUMI_BACKEND_URL", cfg.BackendURL),
			P.MakePair("PULUMI_STACK", cfg.Stack),
		},
		Arr.Filter(func(v P.Pair[string, string]) bool { return P.Tail(v) == "" }),
		Arr.Map(P.Head[string, string]),
	)
	if len(missing) > 0 {
		return IO.Of(preflight.Result{
			Name:   "Pulumi env vars",
			Detail: "unset: " + strings.Join(missing, ", "),
			Fix:    fixClusterEnv,
			Status: preflight.StatusFail,
		})
	}
	return IO.Of(preflight.Result{Name: "Pulumi env vars", Detail: "all set", Status: preflight.StatusPass})
}

// CheckManagerKey verifies the PULUMI_GCP_CREDENTIALS key file exists.
func CheckManagerKey(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.CredsPath == "" {
			return skipped("manager key", "PULUMI_GCP_CREDENTIALS")
		}
		if _, err := os.Stat(cfg.CredsPath); err != nil {
			return preflight.Result{
				Name:   "manager key",
				Detail: "missing at " + cfg.CredsPath,
				Fix:    "run 'just gcp-sa create-sa --sa-name pulumi-manager'",
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{Name: "manager key", Detail: cfg.CredsPath, Status: preflight.StatusPass}
	}
}

// CheckBucketVersioning verifies the state bucket exists with versioning on.
func CheckBucketVersioning(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	bucket := strings.TrimPrefix(cfg.BackendURL, "gs://")
	name := "state bucket gs://" + bucket
	if cfg.BackendURL == "" || cfg.CredsPath == "" {
		return IO.Of(skipped(name, "PULUMI_BACKEND_URL or PULUMI_GCP_CREDENTIALS"))
	}
	return F.Pipe2(
		bucketVersioned(bucket),
		IOE.WithResource[bool](newStorageClient(cfg.CredsPath), closeStorageClient),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: "not found or unreachable: " + err.Error(),
					Fix:    fixPulumiGCSSetup,
					Status: preflight.StatusFail,
				})
			},
			func(versioned bool) IO.IO[preflight.Result] {
				if !versioned {
					return IO.Of(preflight.Result{
						Name: name, Detail: "versioning NOT enabled", Fix: fixPulumiGCSSetup, Status: preflight.StatusFail,
					})
				}
				return IO.Of(preflight.Result{Name: name, Detail: "versioning enabled", Status: preflight.StatusPass})
			},
		),
	)
}

func bucketVersioned(bucket string) func(*storage.Client) IOE.IOEither[error, bool] {
	return func(client *storage.Client) IOE.IOEither[error, bool] {
		return IOE.TryCatchError(func() (bool, error) {
			attrs, err := client.Bucket(bucket).Attrs(context.Background())
			if err != nil {
				return false, err
			}
			return attrs.VersioningEnabled, nil
		})
	}
}

func newStorageClient(credsPath string) IOE.IOEither[error, *storage.Client] {
	return IOE.TryCatchError(func() (*storage.Client, error) {
		return storage.NewClient(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, credsPath),
		)
	})
}

func closeStorageClient(c *storage.Client) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) { return struct{}{}, c.Close() })
}

// CheckSecretsProviderKey verifies the gcpkms secrets-provider key is
// readable with the manager key. Other providers are reported as INFO.
func CheckSecretsProviderKey(cfg PulumiBackendConfig) IO.IO[preflight.Result] {
	const name = "secrets provider key"
	switch {
	case cfg.SecretsProvider == "" || cfg.CredsPath == "":
		return IO.Of(skipped(name, "PULUMI_SECRET_PROVIDER or PULUMI_GCP_CREDENTIALS"))
	case !strings.HasPrefix(cfg.SecretsProvider, "gcpkms://"):
		return IO.Of(preflight.Result{Name: name, Detail: "not gcpkms, check skipped", Status: preflight.StatusInfo})
	}
	return F.Pipe2(
		IOE.FromEither(registry.ParseKMSKey(cfg.SecretsProvider)),
		IOE.Chain(func(key registry.KMSKey) IOE.IOEither[error, string] {
			return F.Pipe1(
				describeKey(key),
				IOE.WithResource[string](newKMSClient(cfg.CredsPath), closeKMSClient),
			)
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "run 'just gcp-kms create-keyring-and-key'",
					Status: preflight.StatusFail,
				})
			},
			func(keyName string) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: keyName + " reachable", Status: preflight.StatusPass})
			},
		),
	)
}

func describeKey(key registry.KMSKey) func(*kms.KeyManagementClient) IOE.IOEither[error, string] {
	return func(client *kms.KeyManagementClient) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			resp, err := client.GetCryptoKey(context.Background(), &kmspb.GetCryptoKeyRequest{
				Name: fmt.Sprintf(
					"projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
					key.Project, key.Location, key.KeyRing, key.Key,
				),
			})
			if err != nil {
				return "", fmt.Errorf("KMS key %s not reachable: %w", key.Key, err)
			}
			return resp.GetName(), nil
		})
	}
}

// CheckPulumiLogin verifies the active (global) pulumi login is this
// cluster's backend.
func CheckPulumiLogin(cfg PulumiBackendConfig, read LoginReader) IO.IO[preflight.Result] {
	if cfg.BackendURL == "" {
		return IO.Of(skipped(pulumiLoginCheckName, "PULUMI_BACKEND_URL"))
	}
	return F.Pipe1(
		read(),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name: pulumiLoginCheckName, Detail: err.Error(), Fix: fixPulumiGCSSetup, Status: preflight.StatusFail,
				})
			},
			func(active string) IO.IO[preflight.Result] {
				return IO.Of(loginResult(active, cfg.BackendURL))
			},
		),
	)
}

func loginResult(active, expected string) preflight.Result {
	if strings.TrimSuffix(active, "/") != strings.TrimSuffix(expected, "/") {
		return preflight.Result{
			Name:   pulumiLoginCheckName,
			Detail: fmt.Sprintf("active login is '%s', expected %s", active, expected),
			Fix:    "pulumi login is global — " + fixPulumiGCSSetup,
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: pulumiLoginCheckName, Detail: "matches " + expected, Status: preflight.StatusPass}
}

// PulumiWhoami is the production LoginReader.
func PulumiWhoami() IOE.IOEither[error, string] {
	return F.Pipe1(
		IOE.TryCatchError(func() ([]byte, error) {
			out, err := exec.Command("pulumi", "whoami", "--json").Output()
			if err != nil {
				return nil, fmt.Errorf("cannot determine the active Pulumi backend (not logged in?): %w", err)
			}
			return out, nil
		}),
		IOE.ChainEitherK(ParseWhoami),
	)
}

// ParseWhoami extracts the backend URL from `pulumi whoami --json`.
func ParseWhoami(out []byte) E.Either[error, string] {
	var who struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &who); err != nil {
		return E.Left[string](fmt.Errorf("parse pulumi whoami: %w", err))
	}
	if who.URL == "" {
		return E.Left[string](errors.New("pulumi whoami reported no backend url"))
	}
	return E.Right[error](who.URL)
}

func skipped(name, unset string) preflight.Result {
	return preflight.Result{Name: name, Detail: "skipped: " + unset + " unset", Status: preflight.StatusInfo}
}
