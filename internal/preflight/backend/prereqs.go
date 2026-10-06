package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	Arr "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	arangoPort         = 8529
	arangoProbeTimeout = 10 * time.Second
)

// PrereqsConfig holds parameters for backend prerequisite checks.
type PrereqsConfig struct {
	Clients       kube.Clients
	Namespace     string
	SecretName    string
	UserKey       string
	PassKey       string
	ArangoService string
	AppName       string
	Port          int
}

// PrereqsChecks is the backend-prereqs gate: namespace, Secret keys, the
// database through the app's Service, and the port range. Each check runs
// even when another fails.
func PrereqsChecks(cfg PrereqsConfig) []IO.IO[preflight.Result] {
	return []IO.IO[preflight.Result]{
		CheckNamespace(cfg),
		CheckSecret(cfg),
		CheckArangoDB(cfg),
		CheckPort(cfg),
	}
}

// CheckNamespace verifies the namespace exists.
func CheckNamespace(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := "namespace " + cfg.Namespace
	return F.Pipe1(
		IOE.TryCatchError(func() (struct{}, error) {
			_, err := cfg.Clients.Kube.CoreV1().Namespaces().Get(
				context.Background(), cfg.Namespace, metav1.GetOptions{},
			)
			return struct{}{}, err
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(lookupFailure(name, err, "run namespace-bootstrap for this cluster"))
			},
			func(_ struct{}) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: "exists", Status: preflight.StatusPass})
			},
		),
	)
}

// CheckSecret verifies the Secret exists and holds both credential keys.
func CheckSecret(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := fmt.Sprintf("secret %s keys %s/%s", cfg.SecretName, cfg.UserKey, cfg.PassKey)
	return F.Pipe1(
		getSecretData(cfg),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(lookupFailure(name, err, "create it with the ArangoDB credentials"))
			},
			func(data map[string][]byte) IO.IO[preflight.Result] {
				return IO.Of(secretKeysResult(name, data, cfg.UserKey, cfg.PassKey))
			},
		),
	)
}

func secretKeysResult(name string, data map[string][]byte, keys ...string) preflight.Result {
	missing := F.Pipe1(keys, Arr.Filter(func(k string) bool { return len(data[k]) == 0 }))
	if len(missing) > 0 {
		return preflight.Result{
			Name:   name,
			Detail: "missing keys: " + strings.Join(missing, ", "),
			Fix:    "key names must match the stack config",
			Status: preflight.StatusFail,
		}
	}
	return preflight.Result{Name: name, Detail: "present", Status: preflight.StatusPass}
}

// lookupFailure turns a failed Kubernetes read into a result naming the real
// cause — the shell version reported every failure as missing.
func lookupFailure(name string, err error, fixMissing string) preflight.Result {
	switch kube.Classify(err) {
	case kube.ReasonNotFound:
		return preflight.Result{Name: name, Detail: "not found", Fix: fixMissing, Status: preflight.StatusFail}
	case kube.ReasonForbidden, kube.ReasonUnauthorized:
		return preflight.Result{
			Name:   name,
			Detail: "access denied: " + err.Error(),
			Fix:    "grant the current kube identity read access",
			Status: preflight.StatusFail,
		}
	case kube.ReasonUnreachable:
		return preflight.Result{
			Name:   name,
			Detail: "API server unreachable: " + err.Error(),
			Fix:    "check KUBECONFIG and cluster connectivity",
			Status: preflight.StatusFail,
		}
	default:
		return preflight.Result{Name: name, Detail: err.Error(), Fix: fixMissing, Status: preflight.StatusFail}
	}
}

func getSecretData(cfg PrereqsConfig) IOE.IOEither[error, map[string][]byte] {
	return IOE.TryCatchError(func() (map[string][]byte, error) {
		secret, err := cfg.Clients.Kube.CoreV1().Secrets(cfg.Namespace).Get(
			context.Background(), cfg.SecretName, metav1.GetOptions{},
		)
		if err != nil {
			return nil, err
		}
		return secret.Data, nil
	})
}

// CheckArangoDB probes the application database through the same Service the
// app connects to, over a tunnel on a free local port.
func CheckArangoDB(cfg PrereqsConfig) IO.IO[preflight.Result] {
	name := fmt.Sprintf("database %s via svc/%s", cfg.AppName, cfg.ArangoService)
	return F.Pipe2(
		getSecretData(cfg),
		IOE.Chain(func(data map[string][]byte) IOE.IOEither[error, string] {
			return F.Pipe1(
				func(t *kube.Tunnel) IOE.IOEither[error, string] {
					return ProbeDatabase(
						fmt.Sprintf("http://localhost:%d", t.LocalPort),
						cfg.AppName,
						string(data[cfg.UserKey]),
						string(data[cfg.PassKey]),
					)
				},
				IOE.WithResource[string](
					kube.OpenServiceTunnel(cfg.Clients, cfg.Namespace, cfg.ArangoService, arangoPort),
					func(t *kube.Tunnel) IOE.IOEither[error, struct{}] { return t.Close() },
				),
			)
		}),
		IOE.Fold(
			func(err error) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{
					Name:   name,
					Detail: err.Error(),
					Fix:    "check the Service and its pods; apply the create-arangodb-databases stack",
					Status: preflight.StatusFail,
				})
			},
			func(detail string) IO.IO[preflight.Result] {
				return IO.Of(preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass})
			},
		),
	)
}

// ProbeDatabase asks ArangoDB at baseURL whether app exists and the
// credentials work. Credentials travel in the request header, never argv.
func ProbeDatabase(baseURL, app, user, pass string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		req, err := http.NewRequestWithContext(
			context.Background(), http.MethodGet,
			baseURL+"/_db/"+app+"/_api/database/current", nil,
		)
		if err != nil {
			return "", err
		}
		req.SetBasicAuth(user, pass)
		resp, err := (&http.Client{Timeout: arangoProbeTimeout}).Do(req)
		if err != nil {
			return "", fmt.Errorf("HTTP probe: %w", err)
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return "credentials valid, database exists", nil
		case http.StatusUnauthorized:
			return "", errors.New("credentials rejected (401) — the Secret does not match the ArangoDB user")
		case http.StatusNotFound:
			return "", fmt.Errorf("database %s not found (404)", app)
		default:
			return "", fmt.Errorf("unexpected HTTP %d", resp.StatusCode)
		}
	})
}

// CheckPort validates the port is in the 1024-65535 range.
func CheckPort(cfg PrereqsConfig) IO.IO[preflight.Result] {
	return func() preflight.Result {
		if cfg.Port < 1024 || cfg.Port > 65535 {
			return preflight.Result{
				Name:   fmt.Sprintf("port %d", cfg.Port),
				Detail: "out of range",
				Fix:    "port must be 1024-65535",
				Status: preflight.StatusFail,
			}
		}
		return preflight.Result{
			Name:   fmt.Sprintf("port %d", cfg.Port),
			Detail: "in range",
			Status: preflight.StatusPass,
		}
	}
}
