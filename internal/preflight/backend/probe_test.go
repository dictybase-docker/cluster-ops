package backend_test

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func failingGet(err error) backend.PrereqsConfig {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "*", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, err
	})
	return backend.PrereqsConfig{
		Clients:    kube.Clients{Kube: cs},
		Namespace:  testNamespace,
		SecretName: testSecretName,
		UserKey:    testUserKey,
		PassKey:    testPassKey,
	}
}

// The shell gate reported RBAC denials and an unreachable API server as
// "MISSING"; each cause must now be named.
func TestLookupFailuresNameTheCause(t *testing.T) {
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "namespaces"}, testNamespace, errors.New("rbac"))
	unreachable := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}

	for _, tc := range []struct {
		name  string
		err   error
		check func(backend.PrereqsConfig) IO.IO[preflight.Result]
		want  string
	}{
		{"namespace forbidden", forbidden, backend.CheckNamespace, "access denied"},
		{"namespace unreachable", unreachable, backend.CheckNamespace, "API server unreachable"},
		{"secret forbidden", forbidden, backend.CheckSecret, "access denied"},
		{"secret unreachable", unreachable, backend.CheckSecret, "API server unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.check(failingGet(tc.err))()
			require.Equal(t, preflight.StatusFail, result.Status)
			require.Contains(t, result.Detail, tc.want)
			require.NotEqual(t, "not found", result.Detail)
		})
	}
}

func arangoStub(t *testing.T, status int, gotAuth *string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/_db/order/_api/database/current", r.URL.Path)
		*gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProbeDatabaseStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		ok     bool
		want   string
	}{
		{http.StatusOK, true, "database exists"},
		{http.StatusUnauthorized, false, "credentials rejected (401)"},
		{http.StatusNotFound, false, "database order not found (404)"},
		{http.StatusInternalServerError, false, "unexpected HTTP 500"},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var auth string
			got := backend.ProbeDatabase(arangoStub(t, tc.status, &auth), testSecretName, "root", "s3cret")()
			require.Equal(t, tc.ok, E.IsRight(got))
			require.Contains(t, F.Pipe1(got, E.Fold(error.Error, F.Identity[string])), tc.want)
			require.Equal(t, "Basic cm9vdDpzM2NyZXQ=", auth, "credentials go in the header, not argv")
		})
	}
}

// Regression: the shell gate port-forwarded to fixed port 18529 and accepted
// any listener there. A decoy answering 200 on 18529 must not turn a missing
// database into a pass — the probe only talks to the address it is given.
func TestProbeDatabaseIgnoresDecoyOnOldPort(t *testing.T) {
	decoy, err := net.Listen("tcp", "127.0.0.1:18529")
	if err != nil {
		t.Skipf("port 18529 busy, cannot stage decoy: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(decoy) }()
	t.Cleanup(func() { _ = srv.Close() })

	var auth string
	got := backend.ProbeDatabase(arangoStub(t, http.StatusNotFound, &auth), testSecretName, "root", "s3cret")()
	require.True(t, E.IsLeft(got), "missing database must fail even with a decoy on 18529")
}
