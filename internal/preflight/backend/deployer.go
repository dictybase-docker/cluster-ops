package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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
		CheckDeploymentPermission(cfg, withReachableAPI(KopsExport), KubeClients),
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
					Fix:    deploymentAccessFix(err),
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

// withReachableAPI keeps the admin credentials from kops export, but
// points the server at the load balancer address already in KUBECONFIG.
// Gossip clusters with dns type None publish api.<name>.k8s.local, and
// that name does not resolve. The access review then fails before it can
// prove the API is reachable.
func withReachableAPI(export KubeconfigExporter) KubeconfigExporter {
	return func(cfg DeployerConfig) IOE.IOEither[error, string] {
		return F.Pipe1(export(cfg), IOE.Chain(alignExportedAPI(cfg.Entry.Cluster)))
	}
}

func alignExportedAPI(cluster string) func(string) IOE.IOEither[error, string] {
	return func(path string) IOE.IOEither[error, string] {
		return IOE.TryCatchError(func() (string, error) {
			if err := alignAPIServer(path, cluster+"-k8s.local", os.Getenv("KUBECONFIG")); err != nil {
				return "", err
			}
			return path, nil
		})
	}
}

func alignAPIServer(exportedPath, clusterName, kubeconfigList string) error {
	exported, err := clientcmd.LoadFromFile(exportedPath)
	if err != nil {
		return fmt.Errorf("read exported kubeconfig: %w", err)
	}
	current, ok := exported.Clusters[clusterName]
	if !ok || current == nil || hostResolves(current.Server) {
		return nil
	}
	alt, ok := reachableCluster(kubeconfigList, clusterName, exportedPath)
	if !ok {
		return nil
	}
	current.Server = alt.Server
	if alt.TLSServerName != "" {
		current.TLSServerName = alt.TLSServerName
	}
	return clientcmd.WriteToFile(*exported, exportedPath)
}

func reachableCluster(kubeconfigList, clusterName, skipPath string) (*clientcmdapi.Cluster, bool) {
	for _, path := range filepath.SplitList(kubeconfigList) {
		if path == "" || samePath(path, skipPath) {
			continue
		}
		loaded, err := clientcmd.LoadFromFile(path)
		if err != nil {
			continue
		}
		cluster, ok := loaded.Clusters[clusterName]
		if !ok || cluster == nil || !hostResolves(cluster.Server) {
			continue
		}
		return cluster, true
	}
	return nil, false
}

func hostResolves(server string) bool {
	host := serverHost(server)
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	//nolint:gosec // G704: host comes from local kubeconfig server URL
	_, err := net.LookupHost(host)
	return err == nil
}

func serverHost(server string) string {
	parsed, err := url.Parse(server)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return aa == bb
}

func deploymentAccessFix(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp") {
		return "API name does not resolve. Run inside cluster-env so KUBECONFIG has the load balancer address."
	}
	return "grant the deployer SA the roles in gcs-files/roles-permissions/deployer-roles.txt"
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
			plaintext := fmt.Appendf(nil, "probe-%d", time.Now().Unix())
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
