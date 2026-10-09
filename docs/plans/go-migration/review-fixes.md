# Go Migration Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Commits:** The human or main agent runs `/ai-commit-gen:commit` (subagents cannot invoke skills) after each task's `git add` + `git commit -m` step.

**Goal:** Fix the defects found reviewing the port-forward, wait/poll and checks/probes phase-2 work on branch `terratest-preflight` (`83e2c7f..bd0fdee`), each pinned by a test that fails first.

**Architecture:** Minimal, local fixes. The port-forward supervisor gets an injectable dial function so its lifecycle is unit-tested without a cluster; recipe bugs get a contract test whose `go` mock really runs the forwarded child (the existing stubs only log argv, which is how these bugs passed `just check`).

**Tech Stack:** Go 1.26, fp-go v2, client-go fake clientset, testify/require, bash contract tests, just.

**Scope:** No k3d. Integration/cluster tests listed in the plans' acceptance criteria are recorded under [Deferred](#deferred-not-in-this-plan), not done here.

**Verified:** Every task was executed on a copy of `bd0fdee`: each new test fails before its fix (output recorded in the plan) and passes after; finally `go build ./...`, `go vet` (also `-tags=integration`), the full Go suite, `-race` on the touched packages, `golangci-lint` (0 issues), gofmt and every `just check` dependency pass. Two local notes: `internal/kops` exec tests need asdf to find `kops` (a parent `.tool-versions`) and fail in a copy outside the repo tree; `test-register-cluster` needs the gitignored `.env.prod.dcr-kube1`.

---

## Review findings

| # | Severity | Finding | Evidence | Task |
|---|----------|---------|----------|------|
| 1 | Breaking | `Forward` never returns after a successful dial: `Session.ready` is never closed, so `forward run` never starts its child and `forward hold` never reports | `internal/kube/portforward.go:103,111`; reproduced — child never ran, killed at 20s | 2 |
| 2 | Breaking | All four `check-pool` recipes pass `--pool { quote(pool) }` (single braces — not interpolated) → bash syntax error, reported as "pool prerequisites failed" | `arangodb.justfile:2951`, `minio.justfile:39`, `postgres.justfile:82`, `redis.justfile:39`; reproduced | 6 |
| 3 | Breaking | The same recipes keep ~30–40 lines of the old shell after the `Next:` line; under `set -u` `$nodes_json` is unbound, so they fail even after #2 | e.g. `redis.justfile:48` | 6 |
| 4 | Breaking | `minio import-bucket`: `RETRIES` not exported (BSD `seq 1 ""` runs 2 attempts, GNU `seq` none — `--retries` ignored); the target alias env holds a literal `${FORWARD_PORT}` nothing substitutes, under an upper-case name that does not match alias `minio_dst` | `minio.justfile:152-162`; the port-forward plan prescribed this pattern (`port-forward.md:448`) | 7 |
| 5 | Breaking | NATS `client rtt` execs into pod `deploy/nats-box` — kubectl shorthand; the API server returns 404 | `internal/preflight/datastore/nats.go:104` | 3 |
| 6 | High | `wait machine-image` ignores `--timeout`: the context has no deadline and `waitTick` never compares elapsed time | `internal/gcp/machine_image.go:74`, `cmd/cluster-ops/wait.go:266` | 5 |
| 7 | Medium | Reconnect budget never resets: three drops over a forward's lifetime end it, however far apart | `portforward.go` loop | 2 |
| 8 | Medium | Data race: `forward hold` sets `Session.OnReady` while the supervisor goroutine reads it; the first "Forwarding from…" line never prints | `cmd/cluster-ops/forward.go:165` | 2 |
| 9 | Low | Postgres verify accepts extra Bound PVCs (`>=`); the shell gate required `-eq` | `internal/preflight/datastore/postgres.go:108` | 4 |
| 10 | Style | Curried calls (user rule: compose with `F.Pipe`) | `forward.go:154,193`, `preflight.go:393-394` | 2, 8 |

Rejected after verification: "ArangoDB verify dropped the amd64 check" (the old `verify` never checked arch; `check-pool` still passes `--require-amd64`), and "rollout must use `observedGeneration ==`" (`>=` is what `kubectl rollout status` uses; the controller never sets observedGeneration above generation).

---

## Task 1: Baseline

**Files:** none

- [ ] **Step 1: Confirm the starting point.** Run `git log --oneline -1` → `bd0fdee docs: describe datastore verify/pool Go checks` (or a descendant without these fixes).
- [ ] **Step 2: Confirm the Go side is green.** Run `go build ./... && gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → `DONE 312 tests, 1 skipped`.
- [ ] **Step 3: Reproduce finding 2.** Run `just redis check-pool` → `command substitution: line 39: … --pool { quote(pool) } …` then `ERROR: pool prerequisites failed`.

---

## Task 2: Port-forward supervisor: ready signal, reconnect budget, OnReady race

**Files:** Create `internal/kube/supervise_test.go`; modify `internal/kube/portforward.go`, `cmd/cluster-ops/forward.go`

Fixes findings 1, 7, 8 and the curried calls in `forward.go`. The dial is injected (`dialFunc`), so `supervise` is tested without a cluster. `ready` is closed once via `sync.Once`; a dial that reached ready resets the retry budget; `OnReady` moves into `ForwardOpts` and is set before `Forward` starts.

- [ ] **Step 1: Write the failing tests.**

Write `internal/kube/supervise_test.go`:

```go
package kube

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const testLocalPort = 40123

// fastBackoff keeps retry tests in milliseconds: 3 dials, 2 waits.
var fastBackoff = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

// holdAfterReady is a dialFunc that reports ready, then holds the forward
// until shutdown — a healthy session.
func holdAfterReady(pod string) dialFunc {
	return func(ctx context.Context, _ int, onReady func(string)) error {
		onReady(pod)
		<-ctx.Done()
		return ctx.Err()
	}
}

// superviseWithin fails the test when supervise has not returned in time —
// the hang the forward commands had.
func superviseWithin(
	t *testing.T, dial dialFunc, onReady func(int, string),
) (*Session, error) {
	t.Helper()
	type result struct {
		s   *Session
		err error
	}
	got := make(chan result, 1)
	go func() {
		s, err := supervise(context.Background(), dial, testLocalPort, onReady, fastBackoff)
		got <- result{s, err}
	}()
	select {
	case r := <-got:
		return r.s, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("supervise did not return after the first successful dial")
		return nil, nil
	}
}

func TestSuperviseReturnsAfterFirstDial(t *testing.T) {
	var gotPort atomic.Int64
	var gotPod atomic.Value
	sess, err := superviseWithin(t, holdAfterReady("arangodb-0"), func(port int, pod string) {
		gotPort.Store(int64(port))
		gotPod.Store(pod)
	})
	require.NoError(t, err)
	require.Equal(t, testLocalPort, sess.LocalPort)
	require.Equal(t, int64(testLocalPort), gotPort.Load())
	require.Equal(t, "arangodb-0", gotPod.Load())
	sess.Close()
}

func TestSuperviseFailsFastOnNotFound(t *testing.T) {
	var dials atomic.Int32
	notFound := func(_ context.Context, _ int, _ func(string)) error {
		dials.Add(1)
		return apierrors.NewNotFound(schema.GroupResource{Resource: "services"}, "arangodb")
	}
	_, err := superviseWithin(t, notFound, nil)
	require.Error(t, err)
	require.Equal(t, int32(1), dials.Load(), "a missing Service is not retried")
}

func TestSuperviseGivesUpAfterRetries(t *testing.T) {
	var dials atomic.Int32
	gone := func(_ context.Context, _ int, _ func(string)) error {
		dials.Add(1)
		return errors.New("pod terminated")
	}
	_, err := superviseWithin(t, gone, nil)
	require.ErrorContains(t, err, "after 3 attempt(s)")
	require.Equal(t, int32(3), dials.Load())
}

// A forward that keeps reconnecting successfully must not exhaust the retry
// budget: the budget limits consecutive failures, not drops over a lifetime.
func TestReconnectBudgetResetsAfterHealthyDial(t *testing.T) {
	var dials, readies atomic.Int32
	flaky := func(ctx context.Context, _ int, onReady func(string)) error {
		n := dials.Add(1)
		onReady("minio-0")
		if n <= 5 {
			return errors.New("connection reset") // drop after each healthy dial
		}
		<-ctx.Done()
		return ctx.Err()
	}
	sess, err := superviseWithin(t, flaky, func(int, string) { readies.Add(1) })
	require.NoError(t, err)
	require.Eventually(t, func() bool { return dials.Load() == 6 }, 2*time.Second, time.Millisecond)
	select {
	case <-sess.Done():
		t.Fatalf("session gave up after %d healthy reconnects: %v", dials.Load()-1, sess.Err())
	default:
	}
	require.Equal(t, int32(6), readies.Load())
	sess.Close()
}
```


- [ ] **Step 2: Run them and confirm they fail.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 -run 'Supervise|Reconnect' ./internal/kube/...`

Expected: build failure — `undefined: dialFunc`, `too many arguments in call to supervise`.

- [ ] **Step 3: Replace the supervisor.**

In `internal/kube/portforward.go`, replace:

```go
	"net/http"
	"net/url"
	"time"
```

with:

```go
	"net/http"
	"net/url"
	"sync"
	"time"
```

In `internal/kube/portforward.go`, replace:

```go
	RemotePort int
	LocalPort  int // 0 = free port
}
```

with:

```go
	RemotePort int
	LocalPort  int // 0 = free port
	// OnReady, when set, is called after every successful dial (including
	// reconnects) with the local port and the pod behind the forward.
	OnReady func(localPort int, pod string)
}
```

In `internal/kube/portforward.go`, replace:

```go
	LocalPort int
	cancel    context.CancelFunc
	done      chan struct{}    // closed when the supervisor gives up
	ready     chan struct{}    // closed on the first successful dial
	failed    chan error       // final, non-retryable supervisor error
	OnReady   func(pod string) // called after every successful dial (incl. reconnects)
}

// Close stops the forward and its reconnect supervisor.
```

with:

```go
	LocalPort int
	cancel    context.CancelFunc
	done      chan struct{} // closed when the supervisor gives up
	ready     chan struct{} // closed on the first successful dial
	readyOnce sync.Once
	failed    chan error // final, non-retryable supervisor error
}

// dialFunc dials the forward once on localPort and holds it until it ends or
// ctx is cancelled, calling onReady(pod) once the forward is up.
type dialFunc func(ctx context.Context, localPort int, onReady func(pod string)) error

// Close stops the forward and its reconnect supervisor.
```

In `internal/kube/portforward.go`, replace:

```go
			return nil, err
		}
		return supervise(ctx, clients, opts, localPort)
	})
}

func supervise(ctx context.Context, clients Clients, opts ForwardOpts, localPort int) (*Session, error) {
	sctx, cancel := context.WithCancel(ctx)
	s := &Session{
```

with:

```go
			return nil, err
		}
		dial := func(ctx context.Context, port int, onReady func(string)) error {
			return dialOnce(ctx, clients, opts, port, onReady)
		}
		sess, err := supervise(ctx, dial, localPort, opts.OnReady, backoffSchedule(reconnectAttempts))
		if err != nil {
			return nil, fmt.Errorf("%s/%s:%d: %w", opts.Namespace, opts.Service, opts.RemotePort, err)
		}
		return sess, nil
	})
}

// supervise starts the reconnect loop and blocks until the first dial is
// ready, the loop fails without retry, or ctx is cancelled.
func supervise(
	ctx context.Context,
	dial dialFunc,
	localPort int,
	onReady func(localPort int, pod string),
	backoff []time.Duration,
) (*Session, error) {
	sctx, cancel := context.WithCancel(ctx)
	s := &Session{
```

In `internal/kube/portforward.go`, replace:

```go
		failed:    make(chan error, 1),
	}
	go s.loop(sctx, clients, opts)

	// Block until the first dial is ready, the parent cancels, or the
```

with:

```go
		failed:    make(chan error, 1),
	}
	go s.loop(sctx, dial, onReady, backoff)

	// Block until the first dial is ready, the parent cancels, or the
```

In `internal/kube/portforward.go`, replace:

```go
}

func (s *Session) loop(ctx context.Context, clients Clients, opts ForwardOpts) {
	defer close(s.done)
	backoff := backoffSchedule(reconnectAttempts)
	for attempt := 0; ; attempt++ {
		err := dialOnce(ctx, clients, opts, s.LocalPort, s.OnReady)
		if ctx.Err() != nil {
			return // parent cancelled: not an error, just shutdown
		}
		if !retryable(err, Classify(err)) || attempt >= len(backoff)-1 {
			s.failed <- fmt.Errorf(
				"port-forward %s/%s:%d after %d attempt(s): %w",
				opts.Namespace, opts.Service, opts.RemotePort, attempt+1, err,
			)
			return
```

with:

```go
}

// loop re-dials after every drop. The retry budget counts consecutive failed
// dials; a dial that reached ready resets it, so a long-lived forward
// survives any number of separate pod restarts.
func (s *Session) loop(
	ctx context.Context,
	dial dialFunc,
	onReady func(localPort int, pod string),
	backoff []time.Duration,
) {
	defer close(s.done)
	for attempt := 0; ; attempt++ {
		connected := false
		err := dial(ctx, s.LocalPort, func(pod string) {
			connected = true
			s.readyOnce.Do(func() { close(s.ready) })
			if onReady != nil {
				onReady(s.LocalPort, pod)
			}
		})
		if ctx.Err() != nil {
			return // parent cancelled: not an error, just shutdown
		}
		if connected {
			attempt = 0
		}
		if !retryable(err, Classify(err)) || attempt >= len(backoff)-1 {
			s.failed <- fmt.Errorf(
				"port-forward after %d attempt(s): %w", attempt+1, err,
			)
			return
```


- [ ] **Step 4: Move `OnReady` into the options and drop the curried calls in the forward commands.**

In `cmd/cluster-ops/forward.go`, replace:

```go
		})
	}

	result := F.Pipe2(
		newClientsIOE(c),
		IOE.Chain(func(clients kube.Clients) IOE.IOEither[error, *kube.Session] {
			return kube.Forward(ctx, clients, forwardOpts(c))
		}),
		IOE.Chain(runChildOnSession),
	)
	return exitOrWrap("forward run", IOE.Fold(
		IO.Of[error],
		func(struct{}) IO.IO[error] { return IO.Of[error](nil) },
	)(result)())
}

func forwardHoldAction(c *cli.Context) error {
	ctx, stop := signalContext()
	defer stop()

	holdSession := func(sess *kube.Session) IOE.IOEither[error, struct{}] {
		return IOE.TryCatchError(func() (struct{}, error) {
			defer sess.Close()
			opts := forwardOpts(c)
			sess.OnReady = func(pod string) {
				fmt.Fprintf(os.Stderr, "Forwarding from 127.0.0.1:%d -> %s:%d (pod %s)\n",
					sess.LocalPort, opts.Service, opts.RemotePort, pod)
			}
			select {
			case <-ctx.Done():
```

with:

```go
		})
	}
	return openForward(c, ctx, forwardOpts(c), runChildOnSession, "forward run")
}

func forwardHoldAction(c *cli.Context) error {
	ctx, stop := signalContext()
	defer stop()

	opts := forwardOpts(c)
	// Set before Forward starts: the supervisor calls it from its own
	// goroutine on the first dial and on every reconnect.
	opts.OnReady = func(localPort int, pod string) {
		fmt.Fprintf(os.Stderr, "Forwarding from 127.0.0.1:%d -> %s:%d (pod %s)\n",
			localPort, opts.Service, opts.RemotePort, pod)
	}
	holdSession := func(sess *kube.Session) IOE.IOEither[error, struct{}] {
		return IOE.TryCatchError(func() (struct{}, error) {
			defer sess.Close()
			select {
			case <-ctx.Done():
```

In `cmd/cluster-ops/forward.go`, replace:

```go
		})
	}

	result := F.Pipe2(
		newClientsIOE(c),
		IOE.Chain(func(clients kube.Clients) IOE.IOEither[error, *kube.Session] {
			return kube.Forward(ctx, clients, forwardOpts(c))
		}),
		IOE.Chain(holdSession),
	)
	return exitOrWrap("forward hold", IOE.Fold(
		IO.Of[error],
		func(struct{}) IO.IO[error] { return IO.Of[error](nil) },
	)(result)())
}
```

with:

```go
		})
	}
	return openForward(c, ctx, opts, holdSession, "forward hold")
}

// openForward opens the forward, runs use on the live session and maps the
// outcome to the CLI error contract.
func openForward(
	c *cli.Context,
	ctx context.Context,
	opts kube.ForwardOpts,
	use func(*kube.Session) IOE.IOEither[error, struct{}],
	prefix string,
) error {
	return exitOrWrap(prefix, F.Pipe3(
		newClientsIOE(c),
		IOE.Chain(func(clients kube.Clients) IOE.IOEither[error, *kube.Session] {
			return kube.Forward(ctx, clients, opts)
		}),
		IOE.Chain(use),
		IOE.Fold(
			IO.Of[error],
			func(struct{}) IO.IO[error] { return IO.Of[error](nil) },
		),
	)())
}
```


- [ ] **Step 5: Run the tests with the race detector.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 -race -run 'Supervise|Reconnect' ./internal/kube/... && go vet ./cmd/cluster-ops/`

Expected: 4 PASS (`TestSuperviseReturnsAfterFirstDial`, `TestSuperviseFailsFastOnNotFound`, `TestSuperviseGivesUpAfterRetries`, `TestReconnectBudgetResetsAfterHealthyDial`), no race report.

Mutation check (optional): replace `s.readyOnce.Do(func() { close(s.ready) })` with `_ = 0` and re-run — `TestSuperviseReturnsAfterFirstDial` fails with `supervise did not return after the first successful dial`. Restore the line.

- [ ] **Step 6: Lint.**

Run: `golangci-lint run ./internal/kube/... ./cmd/cluster-ops/...` → `0 issues.`

- [ ] **Step 7: Commit.**

```bash
git add internal/kube/supervise_test.go internal/kube/portforward.go cmd/cluster-ops/forward.go
git commit -m "fix(kube): return from Forward after the first dial and reset the reconnect budget"
```

---

## Task 3: NATS rtt: exec into the resolved nats-box pod

**Files:** Create `internal/preflight/datastore/nats_internal_test.go`; modify `internal/preflight/datastore/nats.go`, `internal/preflight/datastore/verify_test.go`

Fixes finding 5. `natsBoxPod` resolves the Deployment's selector to a ready pod (the package's `podReady`: Running + all containers ready); a Deployment without a selector is an error rather than an invalid `<none>` selector.

- [ ] **Step 1: Write the failing tests.**

Write `internal/preflight/datastore/nats_internal_test.go`:

```go
package datastore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

const natsTestNS = "dev"

func natsBoxDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: natsBoxName, Namespace: natsTestNS},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/component": natsBoxName}},
		},
	}
}

func natsBoxPodObj(name string, ready bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: natsTestNS,
			Labels: map[string]string{"app.kubernetes.io/component": natsBoxName},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Ready: ready}},
		},
	}
}

// The exec API needs a pod name; `deploy/nats-box` is kubectl shorthand that
// the API server rejects with 404.
func TestNatsBoxPodResolvesDeploymentToReadyPod(t *testing.T) {
	objs := []runtime.Object{
		natsBoxDeployment(),
		natsBoxPodObj("nats-box-6c9f-old", false),
		natsBoxPodObj("nats-box-6c9f-abcde", true),
	}
	pod, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(objs...), natsTestNS)
	require.NoError(t, err)
	require.Equal(t, "nats-box-6c9f-abcde", pod)
}

func TestNatsBoxPodNoReadyPod(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(natsBoxDeployment(), natsBoxPodObj("nats-box-x", false))
	_, err := natsBoxPod(context.Background(), cs, natsTestNS)
	require.ErrorContains(t, err, "no ready nats-box pod")
}

func TestNatsBoxPodNoSelector(t *testing.T) {
	dep := natsBoxDeployment()
	dep.Spec.Selector = nil
	_, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(dep), natsTestNS)
	require.ErrorContains(t, err, "has no pod selector")
}

func TestNatsBoxPodMissingDeployment(t *testing.T) {
	_, err := natsBoxPod(context.Background(), k8sfake.NewSimpleClientset(), natsTestNS)
	require.ErrorContains(t, err, "nats-box deployment missing")
}
```


- [ ] **Step 2: Run them and confirm they fail.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 -run 'NatsBox' ./internal/preflight/datastore/...`

Expected: build failure — `undefined: natsBoxName`.

- [ ] **Step 3: Implement the resolver and use it.**

In `internal/preflight/datastore/nats.go`, replace:

```go
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
```

with:

```go
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
```

In `internal/preflight/datastore/nats.go`, replace:

```go
}

// natsClientPort is the default NATS client port.
const natsClientPort = int32(4222)

// VerifyNats is the post-install check: server StatefulSet readiness, client
```

with:

```go
}

const (
	// natsClientPort is the default NATS client port.
	natsClientPort = int32(4222)
	// natsBoxName is the chart's nats-box Deployment (carries the nats CLI).
	natsBoxName = "nats-box"
)

// VerifyNats is the post-install check: server StatefulSet readiness, client
```

In `internal/preflight/datastore/nats.go`, replace:

```go
	label := "client rtt"
	return func() preflight.Result {
		if _, err := clients.Kube.AppsV1().Deployments(ns).Get(
			context.Background(), "nats-box", metav1.GetOptions{},
		); err != nil {
			return preflight.Result{
				Name:   label,
				Detail: "nats-box deployment missing in " + ns + " (handshake skipped)",
				Status: preflight.StatusFail,
			}
		}
		// Exec needs a live API server; the fake clientset cannot run it.
```

with:

```go
	label := "client rtt"
	return func() preflight.Result {
		pod, err := natsBoxPod(context.Background(), clients.Kube, ns)
		if err != nil {
			return preflight.Result{Name: label, Detail: err.Error() + " (handshake skipped)", Status: preflight.StatusFail}
		}
		// Exec needs a live API server; the fake clientset cannot run it.
```

In `internal/preflight/datastore/nats.go`, replace:

```go
		var stdout, stderr bytes.Buffer
		req := clients.Kube.CoreV1().RESTClient().Post().
			Namespace(ns).Resource("pods").Name("deploy/nats-box").
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
```

with:

```go
		var stdout, stderr bytes.Buffer
		req := clients.Kube.CoreV1().RESTClient().Post().
			Namespace(ns).Resource("pods").Name(pod).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
```

In `internal/preflight/datastore/nats.go`, replace:

```go
		return preflight.Result{Name: label, Detail: strings.TrimSpace(stdout.String()), Status: preflight.StatusPass}
	}
}
```

with:

```go
		return preflight.Result{Name: label, Detail: strings.TrimSpace(stdout.String()), Status: preflight.StatusPass}
	}
}

// natsBoxPod resolves the nats-box Deployment to a ready pod. The exec API
// takes a pod name — kubectl's `deploy/nats-box` shorthand is resolved
// client-side by kubectl and is a 404 for the API server.
func natsBoxPod(ctx context.Context, kube kubernetes.Interface, ns string) (string, error) {
	dep, err := kube.AppsV1().Deployments(ns).Get(ctx, natsBoxName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("nats-box deployment missing in %s: %w", ns, err)
	}
	if dep.Spec.Selector == nil || len(dep.Spec.Selector.MatchLabels)+len(dep.Spec.Selector.MatchExpressions) == 0 {
		return "", fmt.Errorf("nats-box deployment in %s has no pod selector", ns)
	}
	pods, err := kube.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(dep.Spec.Selector),
	})
	if err != nil {
		return "", fmt.Errorf("list nats-box pods in %s: %w", ns, err)
	}
	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			return pods.Items[i].Name, nil
		}
	}
	return "", fmt.Errorf("no ready nats-box pod in %s", ns)
}
```


- [ ] **Step 4: Give the existing `TestVerifyNats` fixture a selector and a ready pod, so its rtt case still reaches the live-API-server branch.**

In `internal/preflight/datastore/verify_test.go`, replace:

```go
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "nats-box"}},
```

with:

```go
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: fixNS, Name: "nats-box"},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nats-box"}},
				},
			},
			readyPod(fixNS, "nats-box-7d9f-abcde", map[string]string{"app": "nats-box"}),
```


- [ ] **Step 5: Run the package tests.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 ./internal/preflight/datastore/...`

Expected: all PASS, including 4 `TestNatsBoxPod*` and `TestVerifyNats`.

- [ ] **Step 6: Commit.**

```bash
git add internal/preflight/datastore/nats_internal_test.go internal/preflight/datastore/nats.go internal/preflight/datastore/verify_test.go
git commit -m "fix(datastore): exec nats rtt in the resolved nats-box pod"
```

---

## Task 4: Postgres verify: exactly one Bound PVC per instance

**Files:** Modify `internal/preflight/datastore/verify_test.go`, `internal/preflight/datastore/postgres.go`

Fixes finding 9.

- [ ] **Step 1: Add the failing case.**

In `internal/preflight/datastore/verify_test.go`, replace:

```go
	t.Run("zero ScheduledBackup fails", func(t *testing.T) {
```

with:

```go
	// The shell gate required exactly one Bound PVC per instance (-eq); a
	// leftover PVC from a deleted instance must not pass as "enough".
	t.Run("extra Bound PVC fails", func(t *testing.T) {
		objs := append(pgObjs(true), pvcObj(fixNS, "logto-2", "dictycr-balanced", "1Gi", "Bound",
			map[string]string{"cnpg.io/cluster": "logto"}))
		clients := kube.Clients{Kube: k8sfake.NewSimpleClientset(objs...)}
		dyn := dynamicClients(
			cnpgCluster(fixNS, "logto", "Cluster in healthy state"),
			scheduledBackupCR(fixNS, "logto-daily"),
		)
		results := preflight.Run(datastore.VerifyPostgres(pgCfg(clients, dyn)))
		pvc := findDatastoreResult(results, "Bound data PVC(s)")
		require.Equal(t, preflight.StatusFail, pvc.Status, "%s", pvc.Detail)
		require.Contains(t, pvc.Detail, "expected 1")
	})

	t.Run("zero ScheduledBackup fails", func(t *testing.T) {
```


- [ ] **Step 2: Run it and confirm it fails.**

Run: `go test -count=1 -run 'TestVerifyPostgres/extra' ./internal/preflight/datastore/`

Expected: FAIL — `expected: "fail"`, `actual  : "pass"`, message `2 Bound data PVC(s)`.

- [ ] **Step 3: Fix the comparison.**

In `internal/preflight/datastore/postgres.go`, replace:

```go
}

// pvcsBoundCount counts Bound PVCs behind the selector.
func pvcsBoundCount(
	clients kube.Clients, ns, selector string, want int, role string,
```

with:

```go
}

// pvcsBoundCount requires exactly want Bound PVCs behind the selector (the
// shell gate's -eq): a leftover PVC from a deleted instance must not pass.
func pvcsBoundCount(
	clients kube.Clients, ns, selector string, want int, role string,
```

In `internal/preflight/datastore/postgres.go`, replace:

```go
		}
		detail := fmt.Sprintf("%d Bound %s", count, role)
		if count >= want {
			return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass}
		}
```

with:

```go
		}
		detail := fmt.Sprintf("%d Bound %s", count, role)
		if count == want {
			return preflight.Result{Name: name, Detail: detail, Status: preflight.StatusPass}
		}
```


- [ ] **Step 4: Run the package tests.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 ./internal/preflight/datastore/...` → all PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/preflight/datastore/verify_test.go internal/preflight/datastore/postgres.go
git commit -m "fix(datastore): require exactly one Bound PVC per postgres instance"
```

---

## Task 5: `wait machine-image`: enforce --timeout

**Files:** Create `internal/gcp/machine_image_test.go`; modify `internal/gcp/machine_image.go`

Fixes finding 6. The budget comes from `cfg.Timeout` (the CLI context only carries signals); the last sleep is capped at the time left.

- [ ] **Step 1: Write the failing tests.**

Write `internal/gcp/machine_image_test.go`:

```go
package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dictybase-docker/cluster-ops/internal/waitfor"
	"github.com/stretchr/testify/require"
)

// tickWithin fails the test when waitTick has not returned in time.
func tickWithin(t *testing.T, cfg waitfor.WaitConfig, start time.Time) error {
	t.Helper()
	got := make(chan error, 1)
	go func() { got <- waitTick(context.Background(), cfg, start, "dcr-node-image") }()
	select {
	case err := <-got:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("waitTick ignored cfg.Timeout and kept waiting")
		return nil
	}
}

// The CLI passes a signal-only context (no deadline): --timeout must be
// enforced from cfg, or a machine image stuck in CREATING waits forever.
func TestWaitTickEnforcesTimeout(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: 50 * time.Millisecond, Interval: time.Hour}
	err := tickWithin(t, cfg, time.Now().Add(-time.Second))
	var werr *waitfor.Error
	require.True(t, errors.As(err, &werr), "want *waitfor.Error, got %v", err)
	require.Equal(t, waitfor.KindTimeout, werr.Kind)
}

// A long interval must not overshoot the deadline: the last sleep is capped
// at the time remaining.
func TestWaitTickCapsSleepAtDeadline(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: 100 * time.Millisecond, Interval: time.Hour}
	require.NoError(t, tickWithin(t, cfg, time.Now()))
}

func TestWaitTickSleepsOneInterval(t *testing.T) {
	cfg := waitfor.WaitConfig{Timeout: time.Hour, Interval: time.Millisecond}
	require.NoError(t, tickWithin(t, cfg, time.Now()))
}
```


- [ ] **Step 2: Run them and confirm they fail.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 -run 'WaitTick' ./internal/gcp/...`

Expected: `TestWaitTickEnforcesTimeout` and `TestWaitTickCapsSleepAtDeadline` FAIL after 2s — `waitTick ignored cfg.Timeout and kept waiting`.

- [ ] **Step 3: Replace `waitTick`.**

In `internal/gcp/machine_image.go`, replace:

```go
}

func waitTick(ctx context.Context, cfg waitfor.WaitConfig, start time.Time, name string) error {
	if ctx.Err() != nil {
		return &waitfor.Error{Kind: waitfor.KindTimeout,
			Msg: fmt.Sprintf("timed out waiting for machine image %s after %s", name, time.Since(start))}
	}
	select {
	case <-time.After(intervalOr(cfg)):
		return nil
	case <-ctx.Done():
		return &waitfor.Error{Kind: waitfor.KindTimeout,
			Msg: fmt.Sprintf("timed out waiting for machine image %s after %s", name, time.Since(start))}
	}
}
```

with:

```go
}

// waitTick sleeps one interval, capped at the time left in cfg.Timeout, and
// reports a timeout once the budget is spent. The budget comes from cfg, not
// ctx: the CLI's context only carries signals.
func waitTick(ctx context.Context, cfg waitfor.WaitConfig, start time.Time, name string) error {
	timedOut := &waitfor.Error{
		Kind: waitfor.KindTimeout,
		Msg:  fmt.Sprintf("timed out waiting for machine image %s after %s", name, time.Since(start).Round(time.Second)),
	}
	sleep := intervalOr(cfg)
	if cfg.Timeout > 0 {
		remaining := cfg.Timeout - time.Since(start)
		if remaining <= 0 {
			return timedOut
		}
		sleep = min(sleep, remaining)
	}
	if ctx.Err() != nil {
		return timedOut
	}
	select {
	case <-time.After(sleep):
		return nil
	case <-ctx.Done():
		return timedOut
	}
}
```


- [ ] **Step 4: Run them again.**

Run: `gotestsum --format-hide-empty-pkg --format testname -- -count=1 -run 'WaitTick' ./internal/gcp/...` → 3 PASS.

- [ ] **Step 5: Commit.**

```bash
git add internal/gcp/machine_image_test.go internal/gcp/machine_image.go
git commit -m "fix(gcp): bound the machine-image wait by --timeout"
```

---

## Task 6: `check-pool` recipes: interpolate flags, drop the leftover shell

**Files:** Create `scripts/test-datastore-recipes.sh`; modify `just_modules/{arangodb,minio,postgres,redis}.justfile`

Fixes findings 2 and 3. The Go `CheckPool` already covers everything the leftover shell checked (count, amd64, taint, zones, Ready). The contract test's `go` mock records argv and honours `MOCK_GO_EXIT`.

- [ ] **Step 1: Write the contract test (check-pool sections).**

Write `scripts/test-datastore-recipes.sh`:

```bash
#!/usr/bin/env bash
# Contract tests for the datastore recipes that wrap cluster-ops:
#   - `just <arangodb|minio|postgres|redis> check-pool` → `cluster-ops preflight pool`
#   - `just minio import-bucket` → `cluster-ops forward run -- bash -c '<mc …>'`
#
# go is mocked on PATH. For `forward run` the mock behaves like the real
# command: it exports FORWARD_PORT/FORWARD_ADDR and RUNS the child after `--`,
# so variables the child needs must really reach it. kubectl, mc and sleep are
# mocked too. Go-side behaviour (pool checks, reconnects) is covered by Go tests.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

tmp="$(mktemp -d)"
mock_bin="${tmp}/bin"
mkdir -p "${mock_bin}"
calls="${tmp}/calls.log"
: > "${calls}"
cleanup() { rm -rf "${tmp}"; }
trap cleanup EXIT

fail() {
    printf 'ERROR: %s\n' "$*" >&2
    exit 1
}

cat > "${mock_bin}/go" <<'MOCK'
#!/usr/bin/env bash
echo "go $*" >> "${MOCK_CALLS_LOG}"
if [[ "$*" == *" forward run "* ]]; then
    while [[ $# -gt 0 && "$1" != "--" ]]; do shift; done
    shift
    export FORWARD_PORT=45678 FORWARD_ADDR=127.0.0.1:45678
    exec "$@"
fi
exit "${MOCK_GO_EXIT:-0}"
MOCK
cat > "${mock_bin}/kubectl" <<'MOCK'
#!/usr/bin/env bash
echo "kubectl $*" >> "${MOCK_CALLS_LOG}"
case "$*" in
    *"{.data.rootUser}"*) printf 'rootuser' | base64 ;;
    *"{.data.rootPassword}"*) printf 'rootpass' | base64 ;;
esac
MOCK
cat > "${mock_bin}/mc" <<'MOCK'
#!/usr/bin/env bash
echo "mc $1 MC_HOST_minio_dst=${MC_HOST_minio_dst:-unset}" >> "${MOCK_CALLS_LOG}"
[[ "$1" == "mirror" && "${MOCK_MC_MIRROR:-ok}" == "fail" ]] && exit 1
exit 0
MOCK
printf '#!/usr/bin/env bash\nexit 0\n' > "${mock_bin}/sleep"
chmod +x "${mock_bin}"/*
export MOCK_CALLS_LOG="${calls}"
export PATH="${mock_bin}:${PATH}"

echo "=== 1. check-pool passes its flags to the Go pool check ==="
for module in arangodb minio postgres redis; do
    : > "${calls}"
    out=$(just "${module}" check-pool 2>&1) || fail "${module} check-pool failed: ${out}"
    grep -q -- "preflight pool --pool database --count 3" "${calls}" \
        || fail "${module}: defaults not passed: $(cat "${calls}")"
    printf '%s' "${out}" | grep -q "All pool checks passed" || fail "${module}: no success line: ${out}"
done
grep -q -- "--require-amd64" <(: > "${calls}"; just arangodb check-pool >/dev/null 2>&1; cat "${calls}") \
    || fail "arangodb check-pool must require amd64"
: > "${calls}"
just redis check-pool --pool cache --node-count 2 >/dev/null 2>&1 || fail "redis check-pool with flags failed"
grep -q -- "preflight pool --pool cache --count 2" "${calls}" || fail "flags not passed: $(cat "${calls}")"
echo "  flag pass-through: PASS"

echo "=== 2. check-pool propagates a failing pool check ==="
for module in arangodb minio postgres redis; do
    if MOCK_GO_EXIT=1 just "${module}" check-pool >/dev/null 2>&1; then
        fail "${module} check-pool exited 0 although the pool check failed"
    fi
done
echo "  exit propagated: PASS"

echo "datastore recipe contract tests PASSED"
```

Run: `chmod +x scripts/test-datastore-recipes.sh`

- [ ] **Step 2: Run it and confirm it fails.**

Run: `bash scripts/test-datastore-recipes.sh`

Expected: `command substitution: line 2951: … --pool { quote(pool) } … --require-amd64` and the script exits non-zero.

- [ ] **Step 3: Rewrite `check-pool` in `just_modules/arangodb.justfile` (double braces, old shell removed).**

In `just_modules/arangodb.justfile`, replace:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool { quote(pool) } --count { quote(node_count) } --require-amd64)
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/arangodb/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just arangodb configure-backup-secrets."

    # Architecture check (must be amd64 for production ArangoDB)
    amd64_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select(.status.nodeInfo.architecture == "amd64")] | length')
    if [[ "$amd64_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $amd64_count node(s) are amd64"
    else
        bad "$amd64_count/$node_count node(s) are amd64 — ArangoDB Cluster requires amd64"
    fi

    # Taint check
    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    # Zone spread (informational)
    zones=$(printf '%s\n' "$nodes_json" | jq -r '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | sort | join(", ")')
    zone_count=$(printf '%s\n' "$nodes_json" | jq '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | length')
    info "zones: $zones ($zone_count unique)"

    # Ready status
    ready_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.status.conditions[] | select(.type == "Ready" and .status == "True")] | length > 0)] | length')
    if [[ "$ready_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $ready_count node(s) are Ready"
    else
        bad "$ready_count/$node_count node(s) are Ready"
    fi

    # Summary
    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll pool checks passed.\033[0m Next: just arangodb configure-backup-secrets.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/arangodb/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi
```

with:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool {{ quote(pool) }} --count {{ quote(node_count) }} --require-amd64)
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/arangodb/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just arangodb configure-backup-secrets."
```


- [ ] **Step 4: Rewrite `check-pool` in `just_modules/minio.justfile` (double braces, old shell removed).**

In `just_modules/minio.justfile`, replace:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool { quote(pool) } --count { quote(node_count) })
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/minio/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just minio deploy --root-user <user> --root-password <pw>."

    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    zones=$(printf '%s\n' "$nodes_json" | jq -r '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | sort | join(", ")')
    info "zones: $zones"

    ready_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.status.conditions[] | select(.type == "Ready" and .status == "True")] | length > 0)] | length')
    if [[ "$ready_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $ready_count node(s) are Ready"
    else
        bad "$ready_count/$node_count node(s) are Ready"
    fi

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll pool checks passed.\033[0m Next: just minio deploy --root-user <user> --root-password <pw>.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/minio/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi
```

with:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool {{ quote(pool) }} --count {{ quote(node_count) }})
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/minio/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just minio deploy --root-user <user> --root-password <pw>."
```


- [ ] **Step 5: Rewrite `check-pool` in `just_modules/postgres.justfile` (double braces, old shell removed).**

In `just_modules/postgres.justfile`, replace:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool { quote(pool) } --count { quote(node_count) })
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/postgres/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just postgres deploy-operator."

    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    zones=$(printf '%s\n' "$nodes_json" | jq -r '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | sort | join(", ")')
    zone_count=$(printf '%s\n' "$nodes_json" | jq '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | length')
    info "zones: $zones ($zone_count unique)"

    ready_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.status.conditions[] | select(.type == "Ready" and .status == "True")] | length > 0)] | length')
    if [[ "$ready_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $ready_count node(s) are Ready"
    else
        bad "$ready_count/$node_count node(s) are Ready"
    fi

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll pool checks passed.\033[0m Next: just postgres deploy-operator.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/postgres/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi
```

with:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool {{ quote(pool) }} --count {{ quote(node_count) }})
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/postgres/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just postgres deploy-operator."
```


- [ ] **Step 6: Rewrite `check-pool` in `just_modules/redis.justfile` (double braces, old shell removed).**

In `just_modules/redis.justfile`, replace:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool { quote(pool) } --count { quote(node_count) })
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/redis/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just redis deploy."

    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    zones=$(printf '%s\n' "$nodes_json" | jq -r '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | sort | join(", ")')
    info "zones: $zones"

    ready_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.status.conditions[] | select(.type == "Ready" and .status == "True")] | length > 0)] | length')
    if [[ "$ready_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $ready_count node(s) are Ready"
    else
        bad "$ready_count/$node_count node(s) are Ready"
    fi

    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll pool checks passed.\033[0m Next: just redis deploy.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/redis/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi
```

with:

```just
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    set +e
    out=$(go run ./cmd/cluster-ops preflight pool \
        --pool {{ quote(pool) }} --count {{ quote(node_count) }})
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: pool prerequisites failed. See reference/redis/pool-requirements.md for fixes." >&2
        exit "$code"
    fi
    echo "All pool checks passed. Next: just redis deploy."
```


- [ ] **Step 7: Run the contract test and lint.**

Run: `bash scripts/test-datastore-recipes.sh && just lint-recipes`

Expected: sections 1–2 print `PASS`, last line `datastore recipe contract tests PASSED`; `lint-recipes: no recipe findings`.

- [ ] **Step 8: Commit.**

```bash
git add scripts/test-datastore-recipes.sh just_modules/arangodb.justfile just_modules/minio.justfile just_modules/postgres.justfile just_modules/redis.justfile
git commit -m "fix(recipes): pass check-pool flags and drop leftover shell"
```

---

## Task 7: `minio import-bucket`: retries and target alias reach the forwarded child

**Files:** Modify `scripts/test-datastore-recipes.sh`, `just_modules/minio.justfile`, `docs/reference/minio/import.md`, `Justfile`

Fixes finding 4. `FORWARD_PORT` exists only inside the child, so the child builds `MC_HOST_minio_dst` (suffix = alias name, case-sensitive) from exported credentials — env, never argv.

- [ ] **Step 1: Add the import-bucket sections to the contract test.**

In `scripts/test-datastore-recipes.sh`, replace:

```bash
echo "datastore recipe contract tests PASSED"
```

with:

```bash
import_bucket() {
    just minio import-bucket --bucket media --source-url https://src.example \
        --source-user srcuser --source-password srcpass "$@"
}

echo "=== 3. import-bucket mirrors through the forward port ==="
: > "${calls}"
out=$(import_bucket 2>&1) || fail "import-bucket failed: ${out}"
[[ "$(grep -c '^mc mirror' "${calls}")" == "1" ]] || fail "expected one mirror: $(cat "${calls}")"
grep -q '^mc mirror MC_HOST_minio_dst=http://rootuser:rootpass@localhost:45678$' "${calls}" \
    || fail "target alias must use the forward port: $(grep '^mc mirror' "${calls}")"
echo "  target alias + mirror: PASS"

echo "=== 4. import-bucket retries the mirror --retries times, then fails ==="
: > "${calls}"
if out=$(MOCK_MC_MIRROR=fail import_bucket --retries 3 2>&1); then
    fail "import-bucket exited 0 although every mirror failed"
fi
[[ "$(grep -c '^mc mirror' "${calls}")" == "3" ]] || fail "expected 3 mirror attempts: $(grep -c '^mc mirror' "${calls}")"
printf '%s' "${out}" | grep -q "after 3 attempts" || fail "failure must name the attempts: ${out}"
echo "  retries: PASS"

echo "datastore recipe contract tests PASSED"
```


- [ ] **Step 2: Run it and confirm it fails.**

Run: `bash scripts/test-datastore-recipes.sh`

Expected: sections 1–2 PASS, then `ERROR: target alias must use the forward port: mc mirror MC_HOST_minio_dst=unset`.

- [ ] **Step 3: Fix the recipe: export what the child needs and build the target alias inside it.**

In `just_modules/minio.justfile`, replace:

```just
    # Reach the cluster-internal API through a forward-held child: the Go
    # parent picks a free local port, exports FORWARD_PORT, and reconnects
    # across pod restarts. Target credentials ride in MC_HOST_MINIO_DST env —
    # never argv. (Source credentials come from the caller's own flags.)
    # Source credentials stay in one alias-set call (they come from the
    # caller's own flags); the target alias rides in MC_HOST_MINIO_DST env.
    mc alias set minio-src "$SRC_URL" "$SRC_USER" "$SRC_PASSWORD" --quiet
    RETRIES="{{ retries }}"
    [[ "$RETRIES" =~ ^[1-9][0-9]*$ ]] || { echo "Error: --retries must be a positive integer." >&2; exit 1; }

    export MC_HOST_MINIO_DST="http://${USER_KEY}:${PASS_KEY}@localhost:\${FORWARD_PORT}"
    export BUCKET REMOVE_FLAG=""
    [[ "{{ remove }}" == "yes" ]] && REMOVE_FLAG="--remove"
    echo "Ensuring bucket '$BUCKET' exists on the target..."
    go run ./cmd/cluster-ops forward run --ns "$NS" --svc minio --remote-port 9000 -- \
        bash -c '
            mc mb --ignore-existing "minio_dst/$BUCKET"
```

with:

```just
    # Reach the cluster-internal API through a forward-held child: the Go
    # parent picks a free local port, exports FORWARD_PORT, and reconnects
    # across pod restarts. FORWARD_PORT exists only inside the child, so the
    # child builds the target alias (MC_HOST_minio_dst — the suffix is the
    # alias name, case-sensitive) from the exported root credentials: env,
    # never argv. Source credentials come from the caller's own flags.
    mc alias set minio-src "$SRC_URL" "$SRC_USER" "$SRC_PASSWORD" --quiet
    RETRIES="{{ retries }}"
    [[ "$RETRIES" =~ ^[1-9][0-9]*$ ]] || { echo "Error: --retries must be a positive integer." >&2; exit 1; }

    export USER_KEY PASS_KEY BUCKET RETRIES REMOVE_FLAG=""
    [[ "{{ remove }}" == "yes" ]] && REMOVE_FLAG="--remove"
    echo "Ensuring bucket '$BUCKET' exists on the target..."
    go run ./cmd/cluster-ops forward run --ns "$NS" --svc minio --remote-port 9000 -- \
        bash -c '
            export MC_HOST_minio_dst="http://${USER_KEY}:${PASS_KEY}@localhost:${FORWARD_PORT}"
            mc mb --ignore-existing "minio_dst/$BUCKET"
```

In `just_modules/minio.justfile`, replace:

```just
            mc ls --recursive "minio_dst/$BUCKET" | wc -l
        '
    unset MC_HOST_MINIO_DST BUCKET REMOVE_FLAG RETRIES

# Post-install check: pool, pod, PVC, Service, Secret. Exits non-zero on failure.
```

with:

```just
            mc ls --recursive "minio_dst/$BUCKET" | wc -l
        '
    unset USER_KEY PASS_KEY BUCKET REMOVE_FLAG RETRIES

# Post-install check: pool, pod, PVC, Service, Secret. Exits non-zero on failure.
```


- [ ] **Step 4: Correct the reference doc.**

In `docs/reference/minio/import.md`, replace:

```markdown
1. Reads this MinIO's root credentials from Secret `minio-root` in the namespace
2. Wraps the whole mc flow in one `cluster-ops forward run` child to `svc/minio:9000` — a free local port exported as `FORWARD_PORT`, torn down when the child exits
3. Registers the `mc` alias `minio-src` (explicit source credentials); the target alias `minio_dst` rides in the `MC_HOST_MINIO_DST` env (Secret-derived credentials)
4. Creates the bucket on the target if missing (`mc mb --ignore-existing`)
5. Runs `mc mirror --preserve --overwrite --retry --summary` with HTTP/1.1 forced for the source (`GODEBUG=http2client=0`); `--remove` only when the flag is passed
```

with:

```markdown
1. Reads this MinIO's root credentials from Secret `minio-root` in the namespace
2. Wraps the whole mc flow in one `cluster-ops forward run` child to `svc/minio:9000` — a free local port exported as `FORWARD_PORT`, torn down when the child exits
3. Registers the `mc` alias `minio-src` (explicit source credentials); the target alias `minio_dst` is built inside the forwarded child as `MC_HOST_minio_dst` from the Secret-derived credentials and `FORWARD_PORT` — env, never argv
4. Creates the bucket on the target if missing (`mc mb --ignore-existing`)
5. Runs `mc mirror --preserve --overwrite --retry --summary` with HTTP/1.1 forced for the source (`GODEBUG=http2client=0`); `--remove` only when the flag is passed
```

In `docs/reference/minio/import.md`, replace:

```markdown
|-------|------------------|-----------|
| `minio-src` | `--source-user` / `--source-password` flags | source endpoint |
| `minio_dst` | Secret `minio-root` in this cluster, via `MC_HOST_MINIO_DST` env | this MinIO via the forward |

The recipe guards the direction: source → target, one bucket. To seed multiple buckets, run once per bucket.
```

with:

```markdown
|-------|------------------|-----------|
| `minio-src` | `--source-user` / `--source-password` flags | source endpoint |
| `minio_dst` | Secret `minio-root` in this cluster, via `MC_HOST_minio_dst` env (set inside the forwarded child) | this MinIO via the forward |

The recipe guards the direction: source → target, one bucket. To seed multiple buckets, run once per bucket.
```


- [ ] **Step 5: Wire the contract test into `just check`.**

In `Justfile`, replace:

```just
    "{{ justfile_directory() }}/scripts/test-pulumi-backend.sh"

# Run the preflight integration tests against the local k3d cluster.
# Needs the cluster running (just k3d start-cluster). Not part of `just check`.
```

with:

```just
    "{{ justfile_directory() }}/scripts/test-pulumi-backend.sh"

test-datastore-recipes:
    #!/usr/bin/env bash
    set -euo pipefail
    "{{ justfile_directory() }}/scripts/test-datastore-recipes.sh"

# Run the preflight integration tests against the local k3d cluster.
# Needs the cluster running (just k3d start-cluster). Not part of `just check`.
```

In `Justfile`, replace:

```just
[group('dev-tools')]
[no-cd]
check: lint-recipes test-bootstrap test-postgres-logical test-logto test-tool-versions test-arangodb-finalization test-arangodb-single-db-restore test-cluster-registry test-register-cluster test-backend-scaffold test-backend-prereqs test-backend-bootstrap test-backend-services test-ci-recipes test-pulumi-backend test-render-tag-deploy build
    #!/usr/bin/env bash
    set -euo pipefail
```

with:

```just
[group('dev-tools')]
[no-cd]
check: lint-recipes test-bootstrap test-postgres-logical test-logto test-tool-versions test-arangodb-finalization test-arangodb-single-db-restore test-cluster-registry test-register-cluster test-backend-scaffold test-backend-prereqs test-backend-bootstrap test-backend-services test-ci-recipes test-pulumi-backend test-datastore-recipes test-render-tag-deploy build
    #!/usr/bin/env bash
    set -euo pipefail
```


- [ ] **Step 6: Run it.**

Run: `just test-datastore-recipes && just lint-recipes && just docs docs-lint`

Expected: sections 1–4 `PASS`, `datastore recipe contract tests PASSED`; no lint findings; docs-lint `PASS`.

- [ ] **Step 7: Commit.**

```bash
git add scripts/test-datastore-recipes.sh just_modules/minio.justfile docs/reference/minio/import.md Justfile
git commit -m "fix(minio): pass retries and the target alias to the forwarded mirror"
```

---

## Task 8: Remove the remaining curried calls

**Files:** Modify `cmd/cluster-ops/preflight.go`

Finding 10 (`forward.go` was done in Task 2). `E.UnwrapError` is the fp-go v2 Either → `(value, error)` bridge.

- [ ] **Step 1: Replace `newClientsValue`.**

In `cmd/cluster-ops/preflight.go`, replace:

```go

func newClientsValue(cltx *cli.Context) (kube.Clients, error) {
	res := newClientsIOE(cltx)()
	err := E.Fold(F.Identity[error], func(kube.Clients) error { return nil })(res)
	clients := E.GetOrElse(func(_ error) kube.Clients { return kube.Clients{} })(res)
	return clients, err
}
```

with:

```go

func newClientsValue(cltx *cli.Context) (kube.Clients, error) {
	return E.UnwrapError(newClientsIOE(cltx)())
}
```


- [ ] **Step 2: Check.**

Run: `go vet ./cmd/cluster-ops/ && gotestsum --format-hide-empty-pkg --format testname -- -count=1 ./cmd/cluster-ops/...` → all PASS.

Run: `grep -nE '\)\((result|res|got|err|cfg|ctx)\)' cmd/cluster-ops/*.go internal/kube/*.go internal/waitfor/*.go internal/preflight/datastore/*.go internal/gcp/machine_image.go | grep -v _test` → no output.

- [ ] **Step 3: Commit.**

```bash
git add cmd/cluster-ops/preflight.go
git commit -m "refactor(cli): unwrap clients with E.UnwrapError"
```

---

## Task 9: Final gate

**Files:** none

- [ ] **Step 1:** `go build ./... && go vet ./... && go vet -tags=integration ./internal/...` → no output.
- [ ] **Step 2:** `go test -race -count=1 ./internal/kube/ ./internal/gcp/ ./internal/preflight/datastore/ ./cmd/cluster-ops/` → four `ok` lines.
- [ ] **Step 3:** `gotestsum --format-hide-empty-pkg --format dots -- -count=1 ./...` → no failures (324 tests, 1 skipped).
- [ ] **Step 4:** `golangci-lint run --build-tags=integration ./internal/registry/... ./internal/preflight/... ./internal/kube/... ./internal/waitfor/... ./internal/gcp/... ./cmd/cluster-ops/...` → `0 issues.`
- [ ] **Step 5:** `just check; echo "exit=$?"` from a checkout that has `.env.prod.dcr-kube1` → each contract script prints its `PASSED` line (now including `datastore recipe contract tests PASSED`), final `exit=0`.

---

## Deferred (not in this plan)

Excluded by request (no k3d): the port-forward plan's reconnect, decoy-port and SIGINT-orphan integration tests and its k3d integration suite; wait/poll tests against a live API server. Covered here without a cluster instead: the supervisor lifecycle (Task 2) and the forwarded-child environment (Task 7).

Still open, no cluster needed (candidates for the next plan): contract tests for the migrated `cluster-ops wait …` recipes; fake-reactor tests that change objects mid-poll (wait/poll acceptance); `minio import-bucket` still passes the **source** credentials to `mc alias set` in argv (they come from the caller's own flags, so already in `ps`, but `MC_HOST_minio-src`-style env would remove them); `port-forward.md:448` describes the broken alias pattern — correct the plan text.
