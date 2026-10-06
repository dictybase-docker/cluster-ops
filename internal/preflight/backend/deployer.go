package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"google.golang.org/api/option"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// deployerKeyCheck names the key-project check.
const deployerKeyCheck = "deployer key"

// DeployerConfig holds parameters for deployer access checks.
type DeployerConfig struct {
	Entry     registry.Entry
	SAKeyPath string
}

// KubeconfigExporter writes a kubeconfig for the registry cluster and
// returns its path. KopsExport is the production implementation.
type KubeconfigExporter func(DeployerConfig) IOE.IOEither[error, string]

// ClientsFactory builds Kubernetes clients from a kubeconfig path.
type ClientsFactory func(kubeconfig string) IOE.IOEither[error, kube.Clients]

// DeployerChecks is the deployer-access gate: key project, cluster access
// (one kops export, then the deployments permission), and the KMS round-trip.
// Each check runs even when another fails.
func DeployerChecks(cfg DeployerConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckKeyProject(cfg),
		CheckDeploymentPermission(cfg, KopsExport, KubeClients),
		CheckKMSRoundTrip(cfg),
	}
}

// CheckKeyProject refuses a key minted in another project: a key from one
// project cannot deploy to another cluster's project.
func CheckKeyProject(cfg DeployerConfig) IO.IO[preflight.Result] {
	return F.Pipe1(
		IOE.TryCatchError(func() (string, error) {
			raw, err := os.ReadFile(cfg.SAKeyPath)
			if err != nil {
				return "", err
			}
			var key struct {
				ProjectID string `json:"project_id"`
			}
			if err := json.Unmarshal(raw, &key); err != nil || key.ProjectID == "" {
				return "", fmt.Errorf("%s is not a GCP service account key JSON", cfg.SAKeyPath)
			}
			return key.ProjectID, nil
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   deployerKeyCheck,
					Detail: err.Error(),
					Fix:    "pass the deployer key JSON with --sa-key",
					Status: preflight.StatusFail,
				})
			},
			func(project string) IO.IO[preflight.Result] {
				return IO.Of(keyProjectResult(project, cfg.Entry.GCPProject))
			},
		),
	)
}

func keyProjectResult(keyProject, registryProject string) preflight.Result {
	if keyProject != registryProject {
		return preflight.Result{
			Name: deployerKeyCheck,
			Detail: fmt.Sprintf(
				"key project '%s' does not match registry gcp_project '%s'",
				keyProject, registryProject,
			),
			Fix:    fmt.Sprintf("mint or reuse a key from the '%s' project only", registryProject),
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{
		Name:   deployerKeyCheck,
		Detail: "project " + keyProject,
		Status: preflight.StatusPass,
	}
}

// CheckDeploymentPermission exports a kubeconfig once, then asks the API
// server whether that identity may update deployments in the registry
// namespace. The kubeconfig file is removed when the check ends.
//
// Note: KopsExport uses `kops export kubeconfig --admin` (same as the shell
// recipe it replaces), so this proves the kops state is readable with the
// key; the admin identity itself is always allowed.
func CheckDeploymentPermission(
	cfg DeployerConfig,
	export KubeconfigExporter,
	newClients ClientsFactory,
) IO.IO[preflight.Result] {
	name := "update deployments in " + cfg.Entry.Namespace
	return F.Pipe2(
		F.Flow2(newClients, IOE.Chain(canUpdateDeployments(cfg.Entry.Namespace))),
		IOE.WithResource[bool](export(cfg), removeFile),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt",
					Status: preflight.StatusFail,
				})
			},
			func(allowed bool) IO.IO[preflight.Result] {
				return IO.Of(permissionResult(name, allowed))
			},
		),
	)
}

func permissionResult(name string, allowed bool) preflight.Result {
	if !allowed {
		return preflight.Result{
			Name:   name,
			Detail: "denied",
			Fix:    "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt",
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: name, Detail: "allowed", Status: preflight.StatusPass}
}

func canUpdateDeployments(namespace string) func(kube.Clients) IOE.IOEither[error, bool] {
	return func(clients kube.Clients) IOE.IOEither[error, bool] {
		return IOE.TryCatchError(func() (bool, error) {
			resp, err := clients.Kube.AuthorizationV1().SelfSubjectAccessReviews().Create(
				context.Background(),
				&authv1.SelfSubjectAccessReview{
					Spec: authv1.SelfSubjectAccessReviewSpec{
						ResourceAttributes: &authv1.ResourceAttributes{
							Namespace: namespace,
							Verb:      "update",
							Group:     "apps",
							Resource:  "deployments",
						},
					},
				},
				metav1.CreateOptions{},
			)
			if err != nil {
				return false, fmt.Errorf("access review: %w", err)
			}
			return resp.Status.Allowed, nil
		})
	}
}

// KopsExport runs `kops export kubeconfig --admin` against the registry
// state bucket with the deployer key, into a private temp file.
func KopsExport(cfg DeployerConfig) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		file, err := os.CreateTemp("", "kubeconfig-*")
		if err != nil {
			return "", err
		}
		path := file.Name()
		if err := file.Close(); err != nil {
			return "", errors.Join(err, os.Remove(path))
		}
		//nolint:gosec // G204: kops args come from the cluster registry, not user input
		cmd := exec.Command(
			"kops", "export", "kubeconfig", "--admin",
			"--name", cfg.Entry.Cluster+"-k8s.local",
			"--state", cfg.Entry.KopsState,
			"--kubeconfig", path,
		)
		// Bind the deployer identity to this subprocess only.
		cmd.Env = append(os.Environ(), "GOOGLE_APPLICATION_CREDENTIALS="+cfg.SAKeyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", errors.Join(
				fmt.Errorf("kops export kubeconfig from %s: %w: %s", cfg.Entry.KopsState, err, out),
				os.Remove(path),
			)
		}
		return path, nil
	})
}

// KubeClients is the production ClientsFactory.
func KubeClients(kubeconfig string) IOE.IOEither[error, kube.Clients] {
	return IOE.FromEither(kube.NewClients(kubeconfig, ""))
}

func removeFile(path string) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, os.Remove(path)
	})
}

// CheckKMSRoundTrip verifies the deployer SA can encrypt/decrypt with the KMS key.
func CheckKMSRoundTrip(cfg DeployerConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		result := F.Pipe1(
			registry.ParseKMSKey(cfg.Entry.KMSSecretsProvider),
			E.Chain(func(key registry.KMSKey) E.Either[error, preflight.Result] {
				return kmsRoundTrip(key, cfg.SAKeyPath)()
			}),
		)

		return F.Pipe1(
			result,
			E.GetOrElse(func(err error) preflight.Result {
				return preflight.Result{
					Name:   "KMS encrypt/decrypt",
					Detail: err.Error(),
					Fix:    "grant deployer SA roles/cloudkms.cryptoKeyEncrypterDecrypter on the registry key",
					Status: preflight.StatusFail,
				}
			}),
		)
	}
}

func kmsRoundTrip(key registry.KMSKey, saKeyPath string) IOE.IOEither[error, preflight.Result] {
	return F.Pipe1(
		performRoundTrip(key),
		IOE.WithResource[preflight.Result](
			newKMSClient(saKeyPath),
			closeKMSClient,
		),
	)
}

func newKMSClient(saKeyPath string) IOE.IOEither[error, *kms.KeyManagementClient] {
	return IOE.TryCatchError(func() (*kms.KeyManagementClient, error) {
		return kms.NewKeyManagementClient(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, saKeyPath),
		)
	})
}

func closeKMSClient(c *kms.KeyManagementClient) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, c.Close()
	})
}

func performRoundTrip(key registry.KMSKey) func(*kms.KeyManagementClient) IOE.IOEither[error, preflight.Result] {
	return func(client *kms.KeyManagementClient) IOE.IOEither[error, preflight.Result] {
		return IOE.TryCatchError(func() (preflight.Result, error) {
			plaintext := []byte(fmt.Sprintf("probe-%d", time.Now().Unix()))
			keyName := fmt.Sprintf(
				"projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
				key.Project, key.Location, key.KeyRing, key.Key,
			)

			encResp, err := client.Encrypt(context.Background(), &kmspb.EncryptRequest{
				Name:      keyName,
				Plaintext: plaintext,
			})
			if err != nil {
				return preflight.Result{}, fmt.Errorf("KMS encrypt failed: %w", err)
			}

			decResp, err := client.Decrypt(context.Background(), &kmspb.DecryptRequest{
				Name:       keyName,
				Ciphertext: encResp.Ciphertext,
			})
			if err != nil {
				return preflight.Result{}, fmt.Errorf("KMS decrypt failed: %w", err)
			}

			if string(decResp.Plaintext) != string(plaintext) {
				return preflight.Result{}, fmt.Errorf("KMS round-trip mismatch")
			}

			return preflight.Result{
				Name:   "KMS encrypt/decrypt",
				Detail: fmt.Sprintf("key %s", key.Key),
				Status: preflight.StatusPass,
			}, nil
		})
	}
}