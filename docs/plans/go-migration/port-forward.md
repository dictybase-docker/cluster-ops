# Plan: Go Migration — Port-Forward Flows

**Status**: Proposed. Depends on phase-1 foundation (`internal/kube`, `internal/preflight`, `internal/registry`) landing first. Part of the just→Go migration program.

## Table of Contents
- [Goal](#goal)
- [Non-goals](#non-goals)
- [Current state](#current-state)
  - [Inventory](#inventory)
  - [Brittleness patterns](#brittleness-patterns)
- [Design](#design)
  - [Classification](#classification)
  - [Architecture](#architecture)
  - [Two implementations](#two-implementations)
  - [Reconnect logic](#reconnect-logic)
  - [Signal handling](#signal-handling)
  - [Secrets](#secrets)
- [Go code sketch](#go-code-sketch)
- [Phases](#phases)
  - [Phase 1: Foundation](#phase-1-foundation)
  - [Phase 2: Short HTTP probes](#phase-2-short-http-probes)
  - [Phase 3: Long data transfers](#phase-3-long-data-transfers)
  - [Phase 4: Interactive dev forwards](#phase-4-interactive-dev-forwards)
- [Acceptance criteria](#acceptance-criteria)
- [Test strategy](#test-strategy)
- [Risks](#risks)
- [Related plans](#related-plans)

## Goal

Eliminate brittle `kubectl port-forward` shell flows: fixed ports, nc-loop readiness probes, no reconnect on pod restart, credentials in argv. Replace with two Go implementations: (A) ephemeral terratest tunnels for short HTTP probes; (B) long-lived client-go forwards with pod re-resolution, context cancellation, and free local ports for data-transfer CLIs (arangodump, pg_dump, mc).

## Non-goals

- Do **not** change the behavior of interactive dev forwards (`k3d minio-forward`) beyond reconnect. Foreground port-forward for manual use is out of scope for full automation.
- Do **not** migrate port-forwards embedded in Pulumi programs. Those live inside Go already; they use different client-go patterns.
- Do **not** change the external CLI tools (arangodump, pg_dump, mc, restic). Docker invocations stay; only the port-forward lifecycle moves to Go.

## Current state

8 `kubectl port-forward` invocations across 7 recipes in 5 files (arangodb, postgres, minio, pulumi, k3d justfiles; `restore-logical` opens twice). The earlier "~24" estimate counted every line mentioning `port-forward`, including comments and echoes. Every flow uses fixed local ports, nc-loop probes, and trap cleanup. None reconnect on pod restart. 3 recipes pass credentials in argv (visible in `ps`, logs). One recipe re-opens the forward mid-flow after detecting a disconnect (`postgres restore-logical` lines 905–918, for post-restore ANALYZE).

### Inventory

| Recipe | File:Line | Target | Ports | Purpose | Duration | Readiness | Cleanup | Brittleness |
|--------|-----------|--------|-------|---------|----------|-----------|---------|-------------|
| `dump-kops-arangodb` | arangodb.justfile:273 | svc/arangodb | 8529:8529 | arangodump via Docker | Long (minutes) | nc -z + wait helper | trap EXIT | Fixed port (lsof kill before), password in argv (`arangodump --server.password`), no reconnect |
| `restore-local-arangodb` | arangodb.justfile:441 | svc/arangodb-single-* | 9529:8529 | arangorestore via Docker | Long (minutes) | wait helper | trap EXIT | Fixed port (lsof kill before), password in argv, no reconnect |
| `dump-logical` | postgres.justfile:611 | svc/*-rw | 15432:5432 | pg_dump via Docker | Long (minutes) | nc -z 30×1s | trap EXIT | Fixed port (nc precheck), password via PGPASSWORD (good), no reconnect |
| `restore-logical` (open) | postgres.justfile:880 | svc/*-rw | 16432:5432 | pg_restore via Docker | Long (minutes) | nc -z 30×1s | trap EXIT | Fixed port, no reconnect |
| `restore-logical` (reopen) | postgres.justfile:907 | svc/*-rw | 16432:5432 | Post-restore ANALYZE | Short (seconds) | nc -z 30×1s | (same trap) | **Re-opens forward after restore** (unique pattern); detects dead forward with nc probe, kills PID, restarts |
| `mirror-minio-bucket` | minio.justfile:175 | svc/minio | 19000:9000 | mc mirror via host CLI | Long (minutes) | nc -z 30×1s | trap EXIT | Fixed port, re-probe before retry (line 212) but no reconnect, credentials in argv (`mc alias set … $USER $PASS`) |
| `check-backend-prereqs` | pulumi.justfile:710 | svc/arangodb | 18529:8529 | curl probe to ArangoDB REST API | Short (1 request) | nc -z 15×1s | trap cleanup_pf | Fixed port, credentials in curl `-u` argv (visible in ps), probe-only (no long transfer) |
| `minio-forward` | k3d.justfile:76 | svc/minio | 9000:9000 | Interactive dev access to MinIO | Indefinite (user-driven) | kubectl implicit | None (foreground, Ctrl-C) | Foreground kubectl, no trap, user kills manually |

**Counts**: 7 recipes, 8 port-forward invocations (`restore-logical` has open + reopen at postgres.justfile:880 and :907).

**Fixed ports in use**: 8529 (arangodb dump/prereq), 9529 (arangodb restore), 15432 (postgres dump), 16432 (postgres restore), 18529 (prereq probe), 19000 (minio mirror), 9000 (minio dev).

### Brittleness patterns

1. **Fixed local ports**: every recipe hardcodes a port; `lsof -ti:<port> | xargs kill -9` or `nc -z` precheck before starting. Concurrent runs on the same machine fail.
2. **nc-loop readiness**: `for i in $(seq 1 30); do nc -z localhost $PORT && break; sleep 1; done`. No exponential backoff, no classified error types (NotFound vs Unauthorized).
3. **No reconnect on pod restart**: long transfers (arangodump 10+ minutes) fail silently if the target pod restarts mid-flow. `kubectl port-forward` dies with the pod; the external CLI (arangodump/pg_dump/mc) sees a closed socket and aborts.
4. **Re-open pattern in postgres restore**: lines 905–918 detect a dead forward with `nc -z`, kill the PID, and `kubectl port-forward` again before ANALYZE. This is manual reconnect logic duplicated for one recipe; no other recipe has it.
5. **Credentials in argv**: `arangodump --server.password "$PASSWORD"` (visible in ps, logs). `curl -u "$user:$pass"`. `mc alias set … "$USER" "$PASS"`. `pg_dump` uses `PGPASSWORD` env (correct).
6. **trap cleanup race**: `trap cleanup EXIT` sets `PF_PID=$!` after backgrounding kubectl. If the script exits before that line, PID is unset and the forward orphans.
7. **minio re-probe before retry** (minio.justfile:212): `nc -z` loop before each mc retry. Detects dead forward but doesn't fix it; mirror attempt proceeds on a dead socket if nc succeeds on a zombie listener.

## Design

### Classification

| Class | Count | Pattern | Implementation |
|-------|-------|---------|----------------|
| **A. Short HTTP/API probe** | 1 | `check-backend-prereqs` curl to ArangoDB | Terratest `k8s.NewTunnelWithLogger` wrapped by `kube.OpenServiceTunnel`, used via `IOE.WithResource`. HTTP request over the tunnel in Go (net/http). No external process. |
| **B. Long data transfer** | 5 recipes, 6 invocations | arangodump, arangorestore, pg_dump, pg_restore, mc mirror | Go `cluster-ops forward run --svc <name> --ns <ns> --remote-port <n> -- <cmd>` spawns the child (arangodump etc.) with a free local port exported to env (`FORWARD_PORT` / `FORWARD_ADDR`). Client-go portforward with pod re-resolution on loss, context cancellation, signal propagation. Reconnect with exponential backoff (3 attempts, 2s/4s/8s). |
| **C. Interactive dev** | 1 | `k3d minio-forward` | `cluster-ops forward hold --svc <name> --ns <ns> --remote-port <n> [--local-port <n>]`. Runs foreground (user Ctrl-C to stop). Reconnects on pod restart. Prints `Forwarding from 127.0.0.1:<port> -> <pod>:<remote>` on connect; `Reconnecting…` on loss. Optional fixed local port for mc alias compatibility. |
| **D. Leave in shell** | 0 | (none identified) | — |

**Class B details**: the child process (arangodump, pg_dump, mc) connects to `127.0.0.1:$FORWARD_PORT`. The Go parent exports `FORWARD_PORT=<free-port>` and `FORWARD_ADDR=127.0.0.1:<free-port>` before exec. On SIGINT/SIGTERM, parent cancels the forward context, waits for child exit (max 5s), force-kills if needed, and exits with the child's code.

**Class C details**: foreground loop. On pod restart, re-resolve Service→Endpoints→pod, re-dial. Print reconnect notices to stderr so `mc` output stays clean.

### Architecture

```
internal/kube/
  tunnel.go       # Terratest wrapper: OpenServiceTunnel(clients, ns, svc, remotePort) IOE[*Tunnel] (class A)
  portforward.go  # Client-go forward: Forward(ctx, clients, opts ForwardOpts) IOE[*Session] (class B/C)
  classify.go     # Classify(err) Reason (existing, phase-1 foundation)
  client.go       # NewClients(kubeconfig, context) E[Clients] (existing, phase-1 foundation)

cmd/cluster-ops/
  forward.go      # CLI: `forward run` (class B), `forward hold` (class C)

internal/preflight/
  (unchanged)     # Class A probes (check-backend-prereqs) use preflight.Run with kube.OpenServiceTunnel

Recipes:
  just pulumi check-backend-prereqs  → go run ./cmd/cluster-ops preflight check-backend ...
  just postgres dump-logical          → go run ./cmd/cluster-ops forward run --svc logto-rw --ns dev --remote-port 5432 -- pg_dump ...
  just arangodb dump-kops-arangodb    → go run ./cmd/cluster-ops forward run --svc arangodb --ns prod --remote-port 8529 -- arangodump ...
  just k3d minio-forward              → go run ./cmd/cluster-ops forward hold --svc minio --ns dev --remote-port 9000 [--local-port 9000]
```

### Two implementations

**Why two?**

1. **Terratest tunnel** (`kube.OpenServiceTunnel` wrapping `k8s.NewTunnelWithLogger`):
   - **Pros**: Simple API; free local port under a global lock; blocks until ready; integrates with fp-go `IOE.WithResource`.
   - **Cons**: No context.Context (can't cancel without panic-recover); no reconnect on pod restart (tunnel dies with the pod); `ForwardPortE` internally calls non-E `GetService` (→ `t.Fatal` → panic, recovered by our `runT` adapter); local port 0 → free port but opaque to caller (fine for Go HTTP client, breaks for "export to child env" use).
   - **Use**: Class A only (short HTTP probes in Go, no child process, no long-lived forwards).

2. **Client-go portforward** (`kube.Forward` wrapping `k8s.io/client-go/tools/portforward`):
   - **Pros**: context.Context for cancellation; explicit pod selection (re-resolve on loss); streams stdout/stderr to logger; port pair known before dial (export to child env).
   - **Cons**: More code (SPDY upgrader, pod lister, reconnect loop); no global port lock (must probe with net.Listen before use).
   - **Use**: Class B (long transfers with child process) and C (interactive dev forwards).

**Port selection for client-go**: on class B (`forward run`), always use local port 0 (free port via `net.Listen(":0")`), export to child. On class C (`forward hold`), accept `--local-port <n>` (user compatibility with mc alias) with conflict detection (`net.Listen` on the exact port before forward; fail fast if taken), default to 0 if omitted.

### Reconnect logic

**Trigger**: `portforward.ForwardPorts()` returns with error. Classify the error with `kube.Classify(err)`:
- `NotFound` / `Unauthorized` / `Forbidden` → fail (don't retry; config/RBAC error).
- `Unreachable` / `Other` → retry (pod restarted, network blip).

**Backoff**: exponential 2s, 4s, 8s (3 attempts total). On exhaustion, fail.

**Pod re-resolution**: on each attempt, re-list Endpoints/EndpointSlice for the Service (client-go `corev1.Endpoints`), pick first ready pod, dial it. If no pods ready, classify as `Unreachable` and retry.

**Class B behavior**: on reconnect failure mid-transfer, cancel child context (SIGTERM), wait 5s, SIGKILL if needed, exit non-zero. The external CLI (arangodump, pg_dump) sees EOF on the socket and aborts; Go propagates the exit code.

**Class C behavior**: on reconnect failure, print error to stderr, retry indefinitely (user Ctrl-C to abort). Interactive use tolerates longer outages (user re-creating the cluster).

### Signal handling

**Class B (`forward run`)**:
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
// Start child process with ctx
// On signal: ctx cancelled → cancel forward → child gets EOF → wait for child exit (5s) → SIGKILL if needed → exit with child code
```

**Class C (`forward hold`)**:
```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
// Forward loop: on ctx.Done(), print "Shutting down…", close forward, exit 0
```

### Secrets

**Rule**: credentials never in argv or logs. Always env or stdin.

- `arangodump` / `arangorestore`: change from `--server.password "$PASS"` (argv) to env var `ARANGODB_PASSWORD` (arangodump 3.11+ supports it; verify in Docker image).
- `pg_dump` / `pg_restore`: already use `PGPASSWORD` env (correct).
- `mc alias set`: change from `mc alias set <alias> <url> $USER $PASS` (argv) to `mc alias set <alias> <url> --env` (reads `MC_HOST_<ALIAS>` env with embedded credentials: `MC_HOST_MINIO_DST=http://$USER:$PASS@localhost:$PORT`; verify mc supports it or use stdin).
- `curl` in Go (class A): use `req.SetBasicAuth(user, pass)` (net/http), not `-u` flag.

**Verification**: `ps aux | grep <port>` during a test run must not show passwords.

## Go code sketch

### `internal/kube/portforward.go`

```go
package kube

import (
    "context"
    "fmt"
    "io"
    "net"
    "net/http"
    "net/url"
    "time"

    IOE "github.com/IBM/fp-go/v2/ioeither"
    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/tools/portforward"
    "k8s.io/client-go/transport/spdy"
)

type ForwardOpts struct {
    Namespace  string
    Service    string
    RemotePort int
    LocalPort  int // 0 = free port
}

type Session struct {
    LocalPort int
    closeFn   func()
}

func (s *Session) Close() error {
    s.closeFn()
    return nil
}

// Forward opens a port-forward to a Service, resolving to the first ready pod.
// LocalPort 0 → free port (returned in Session). Reconnects on pod loss up to 3 attempts.
func Forward(ctx context.Context, clients Clients, opts ForwardOpts) IOE.IOEither[error, *Session] {
    return IOE.TryCatch(func() (*Session, error) {
        // Resolve Service → Endpoints → first ready pod
        pod, err := resolvePod(ctx, clients.Kube, opts.Namespace, opts.Service)
        if err != nil {
            return nil, err
        }

        // Acquire local port (0 → free port via net.Listen)
        localPort := opts.LocalPort
        if localPort == 0 {
            l, err := net.Listen("tcp", ":0")
            if err != nil {
                return nil, err
            }
            localPort = l.Addr().(*net.TCPAddr).Port
            l.Close()
        } else {
            // Verify fixed port is free
            l, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
            if err != nil {
                return nil, fmt.Errorf("local port %d in use: %w", localPort, err)
            }
            l.Close()
        }

        // SPDY upgrader for portforward
        restURL := clients.Rest.Host
        u, _ := url.Parse(restURL)
        u.Path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", opts.Namespace, pod)
        transport, upgrader, err := spdy.RoundTripperFor(clients.Rest)
        if err != nil {
            return nil, err
        }

        // Portforward streams (discard stdout/stderr for now; wire to slog later)
        readyCh := make(chan struct{})
        stopCh := make(chan struct{})
        errCh := make(chan error, 1)
        ports := []string{fmt.Sprintf("%d:%d", localPort, opts.RemotePort)}

        dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, u)
        fw, err := portforward.New(dialer, ports, stopCh, readyCh, io.Discard, io.Discard)
        if err != nil {
            return nil, err
        }

        go func() {
            errCh <- fw.ForwardPorts()
        }()

        // Wait for ready or context cancel
        select {
        case <-readyCh:
        case <-ctx.Done():
            close(stopCh)
            return nil, ctx.Err()
        case err := <-errCh:
            return nil, err
        }

        // Cleanup on context cancel
        go func() {
            <-ctx.Done()
            close(stopCh)
        }()

        sess := &Session{
            LocalPort: localPort,
            closeFn: func() {
                close(stopCh)
            },
        }
        return sess, nil
    })
}

func resolvePod(ctx context.Context, kube kubernetes.Interface, ns, svc string) (string, error) {
    eps, err := kube.CoreV1().Endpoints(ns).Get(ctx, svc, metav1.GetOptions{})
    if err != nil {
        return "", err
    }
    for _, subset := range eps.Subsets {
        for _, addr := range subset.Addresses {
            if addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" {
                return addr.TargetRef.Name, nil
            }
        }
    }
    return "", fmt.Errorf("no ready pods for service %s/%s", ns, svc)
}
```

### `cmd/cluster-ops/forward.go`

```go
package main

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "os/signal"
    "syscall"

    "github.com/dictybase/cluster-ops/internal/kube"
    "github.com/urfave/cli/v2"
)

func forwardRunCmd() *cli.Command {
    return &cli.Command{
        Name:  "run",
        Usage: "Port-forward to a service, run a command with FORWARD_PORT exported, tear down on exit",
        Flags: []cli.Flag{
            &cli.StringFlag{Name: "kubeconfig"},
            &cli.StringFlag{Name: "context"},
            &cli.StringFlag{Name: "namespace", Aliases: []string{"ns"}, Required: true},
            &cli.StringFlag{Name: "svc", Required: true},
            &cli.IntFlag{Name: "remote-port", Required: true},
        },
        Action: func(c *cli.Context) error {
            clients, err := kube.NewClients(c.String("kubeconfig"), c.String("context"))()
            if err != nil {
                return err
            }
            ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
            defer stop()

            opts := kube.ForwardOpts{
                Namespace:  c.String("namespace"),
                Service:    c.String("svc"),
                RemotePort: c.Int("remote-port"),
                LocalPort:  0, // free port
            }
            sess, err := kube.Forward(ctx, clients, opts)()
            if err != nil {
                return err
            }
            defer sess.Close()

            // Run child with FORWARD_PORT exported
            cmdArgs := c.Args().Slice()
            if len(cmdArgs) == 0 {
                return fmt.Errorf("no command specified after --")
            }
            cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
            cmd.Env = append(os.Environ(),
                fmt.Sprintf("FORWARD_PORT=%d", sess.LocalPort),
                fmt.Sprintf("FORWARD_ADDR=127.0.0.1:%d", sess.LocalPort),
            )
            cmd.Stdout = os.Stdout
            cmd.Stderr = os.Stderr
            if err := cmd.Run(); err != nil {
                if exitErr, ok := err.(*exec.ExitError); ok {
                    os.Exit(exitErr.ExitCode())
                }
                return err
            }
            return nil
        },
    }
}
```

## Phases

### Phase 1: Foundation

**Scope**: `internal/kube/portforward.go` (client-go forward with reconnect), `cmd/cluster-ops/forward.go` (CLI for run/hold), integration tests on k3d.

**Deliverables**:
1. `kube.Forward(ctx, clients, opts) IOE[*Session]` with reconnect (3 attempts, exponential backoff, pod re-resolution).
2. `cluster-ops forward run --svc <name> --ns <ns> --remote-port <n> -- <cmd>` (free port export, signal handling, exit code propagation).
3. `cluster-ops forward hold --svc <name> --ns <ns> --remote-port <n> [--local-port <n>]` (foreground, reconnect notices).
4. Integration tests (`//go:build integration`):
   - Deploy MinIO to k3d (via existing `minio/Pulumi.local.yaml`).
   - `forward run -- mc ls k3d-minio/` succeeds (free port).
   - Kill the MinIO pod mid-forward; assert reconnect.
   - `forward hold` with Ctrl-C shutdown (manual test).
5. Contract tests (shell, `scripts/test-portforward.sh`):
   - Mock `go` on PATH; `just k3d minio-forward` (phase 4 recipe) renders `go run ./cmd/cluster-ops forward hold …` (assert flags, no kubectl).
   - `FORWARD_PORT` / `FORWARD_ADDR` present in child env (unit test, not contract).

**Acceptance**:
- Integration test: kill target pod during `forward run -- sleep 30`; reconnect succeeds within 15s (3 attempts × 2s/4s/8s).
- Decoy listener on port 19000 (old minio fixed port) does not cause false pass (free port selection ignores it).
- `ps aux | grep forward` shows no passwords in argv.
- No orphaned kubectl processes after SIGINT (check with `pgrep kubectl` after test).

### Phase 2: Short HTTP probes

**Scope**: Migrate `check-backend-prereqs` (pulumi.justfile:710) to Go preflight check using `kube.OpenServiceTunnel` (terratest).

**Recipe change**:
```diff
-kubectl port-forward -n "${namespace}" "svc/{{ arango_service }}" "${local_port}:8529" > "${pf_log}" 2>&1 &
-pf_pid=$!
-# ... nc -z loop ...
-code=$(curl -s -o /dev/null -w '%{http_code}' -u "${dbuser}:${dbpass}" "http://localhost:${local_port}/_db/${app}/_api/database/current")
+go run ./cmd/cluster-ops preflight check-backend --folder {{ folder }} --stack {{ stack }} --arango-service {{ arango_service }}
```

**Go implementation**: `cmd/cluster-ops/preflight.go` (new check-backend subcommand) reads stack config (namespace, app, secret keys), calls `kube.OpenServiceTunnel`, makes HTTP request with `net/http` (credentials via `SetBasicAuth`), classifies response (200 OK, 401 Unauthorized, 404 NotFound).

**Deliverables**:
1. `cmd/cluster-ops/preflight.go` with `check-backend` subcommand.
2. Integration test: deploy arangodb-single to k3d, run check-backend, assert 200.
3. Contract test: `just pulumi check-backend-prereqs --folder modware-annotation --stack prod` renders `go run ./cmd/cluster-ops preflight check-backend …` (no kubectl port-forward).

**Acceptance**:
- `just pulumi check-backend-prereqs --folder modware-annotation --stack prod` exits 0 on green, non-zero with detailed error on missing DB.
- No kubectl process spawned (contract test asserts this).
- Credentials not in argv or logs (integration test inspects cmd.Args).

### Phase 3: Long data transfers

**Scope**: Migrate dump/restore recipes (arangodb, postgres, minio) to `forward run`.

**3.1: postgres dump/restore** (2 recipes, 3 port-forward sites: dump, restore open, restore reopen).

Changes:
```diff
 # dump-logical
-kubectl --kubeconfig "$SOURCE_KUBECONFIG" port-forward -n "$SOURCE_NS" "svc/$SOURCE_SERVICE" "$SOURCE_PORT:5432" >"$PF_LOG" 2>&1 &
-PF_PID=$!
-# ... nc -z loop ...
-PGPASSWORD="$SOURCE_PASSWORD" docker run --rm $DOCKER_NET_FLAGS -e PGPASSWORD "$CLIENT_IMAGE" pg_dump -h "$DB_HOST" -p "$SOURCE_PORT" ...
+go run ./cmd/cluster-ops forward run --kubeconfig "$SOURCE_KUBECONFIG" --ns "$SOURCE_NS" --svc "$SOURCE_SERVICE" --remote-port 5432 -- \
+  docker run --rm $DOCKER_NET_FLAGS -e PGPASSWORD="$SOURCE_PASSWORD" "$CLIENT_IMAGE" \
+    pg_dump -h "$DB_HOST" -p '$FORWARD_PORT' ...
```

**ANALYZE reconnect pattern** (postgres restore lines 905–918): DELETE the manual reopen. `forward run` holds the forward for the child's lifetime; pg_restore + ANALYZE are one child (`bash -c "pg_restore ... && psql ... -c ANALYZE"`), no reconnect needed.

**3.2: arangodb dump/restore** (2 recipes).

Credential fix: change `arangodump --server.password "$PASSWORD"` to env var `ARANGODB_PASSWORD="$PASSWORD" go run ./cmd/cluster-ops forward run ... -- docker run -e ARANGODB_PASSWORD ...`.

**3.3: minio mirror** (1 recipe).

mc alias: change from `mc alias set minio-dst "http://localhost:19000" "$USER_KEY" "$PASS_KEY"` to `MC_HOST_MINIO_DST="http://$USER_KEY:$PASS_KEY@localhost:\$FORWARD_PORT" mc mirror ...`.

**Deliverables**:
1. Updated recipes (6 files changed: postgres.justfile, arangodb.justfile, minio.justfile).
2. Integration tests:
   - Deploy postgres to k3d, dump with `forward run -- pg_dump`, assert .dump file non-empty.
   - Deploy arangodb to k3d, dump with `forward run -- arangodump`, assert archive.
   - Kill target pod mid-dump; assert reconnect + dump completion (or clean failure with exit code).
3. Contract tests:
   - `just postgres dump-logical` renders `go run ./cmd/cluster-ops forward run`.
   - No `kubectl port-forward` in rendered output.
   - No passwords in `go` argv (assert `--remote-port` present, no `--server.password`).

**Acceptance**:
- `just postgres dump-logical --source-cluster k3d-dev-cluster` produces a .dump file.
- `just postgres restore-logical` runs ANALYZE without manual forward reopen (deleted code).
- Kill postgres pod during dump; dump exits non-zero within 20s (reconnect exhausted).
- No zombie kubectl processes after recipe exit (integration test `pgrep kubectl` → empty).

### Phase 4: Interactive dev forwards

**Scope**: Migrate `k3d minio-forward` to `forward hold`.

Change:
```diff
-KUBECONFIG="$f" kubectl port-forward svc/minio {{ quote(local_port) }}:9000 -n {{ quote(namespace) }}
+KUBECONFIG="$f" go run ./cmd/cluster-ops forward hold --ns {{ quote(namespace) }} --svc minio --remote-port 9000 --local-port {{ local_port }}
```

**Deliverables**:
1. Updated `k3d.justfile:76`.
2. Manual test: `just k3d minio-forward`, kill pod, assert "Reconnecting…" notice, mc alias keeps working.
3. Contract test: rendered output has `forward hold`, not `kubectl port-forward`.

**Acceptance**:
- `just k3d minio-forward` prints `Forwarding from 127.0.0.1:9000 -> minio-<pod>:9000`.
- Kill minio pod; forward prints `Reconnecting…` and resumes within 10s.
- Ctrl-C exits cleanly (no orphaned processes).

## Acceptance criteria

Across all phases:

1. **No fixed ports**: `lsof -i :8529,9529,15432,16432,18529,19000` during parallel recipe runs → empty (free ports avoid conflicts).
2. **No kubectl orphans**: `pgrep kubectl` after recipe exit (success or failure) → empty.
3. **No credentials in argv**: `ps aux | grep 'go run.*forward'` during a dump → no password strings.
4. **Reconnect on pod restart**: integration test kills target pod mid-transfer; forward reconnects within 15s or fails with classified error (NotFound → fail, Unreachable → retry exhausted).
5. **Decoy listener resilience**: `nc -l 19000` running during `forward run` with free port → recipe ignores the decoy, uses a different port.
6. **Contract tests pass**: `just check` (all `scripts/test-*.sh`) exits 0 (ORDER assertions on `go run ./cmd/cluster-ops`, not kubectl).
7. **Integration tests pass**: `gotestsum --tags=integration -- -run TestPortForward` on k3d cluster exits 0 (deploy minio/arangodb/postgres, dump/restore with pod kill mid-flow).

## Test strategy

### Unit tests

- `kube.Forward`: fake clientset, mock Endpoints (no ready pods → Unreachable; 1 ready pod → success).
- `kube.resolvePod`: empty endpoints → error; multiple subsets → first ready pod returned.
- Port selection: `LocalPort: 0` → free port > 1024; `LocalPort: 8080` with conflict → error.

### Integration tests (`//go:build integration`)

Setup: k3d cluster with MinIO, ArangoDB, Postgres deployed via existing Pulumi.local.yaml stacks.

Tests:
1. `TestForwardRunMinio`: `forward run -- mc ls k3d-minio/test-bucket` → exit 0, output contains bucket.
2. `TestForwardRunReconnect`: start `forward run -- sleep 60`, kill minio pod at t=10s, assert forward reconnects, sleep completes.
3. `TestForwardHold`: start `forward hold` in goroutine, kill pod, assert reconnect notice in stderr.
4. `TestCheckBackendPrereqs`: deploy arangodb, run `preflight check-backend`, assert exit 0.
5. `TestPostgresDump`: run `forward run -- pg_dump`, assert .dump file size > 0.

### Contract tests (shell, `scripts/test-portforward.sh`)

Mock `go` on PATH → `echo "$@" >> calls.log`. Run recipes, assert:
- `grep 'forward run' calls.log` (phase 3).
- `! grep 'kubectl port-forward' calls.log` (no direct kubectl).
- `grep -- '--remote-port' calls.log` (flags present).
- `! grep -- '--server.password' calls.log` (no password flags).

## Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Terratest `NewTunnelWithLogger` internal `GetService` calls `t.Fatal` (panic) on error; our `runT` adapter recovers, but non-sentinel panics re-thrown | Medium: unexpected panics crash the process | Verify terratest v1.0.1 source; add integration test with missing Service (expect graceful error, not crash) |
| Client-go portforward API changes between k8s.io/client-go versions (spdy upgrader, port format) | Medium: builds break on client-go upgrade | Pin client-go to tested version (v0.28.x, matches cluster 1.28); add comment citing API surface |
| arangodump/mc don't support password via env (must use argv) | Medium: credentials stay in ps | Verify in Docker images; fallback: write password to tmpfile with `mktemp`, pass `--server.password-file` (arangodump 3.11+), `mc alias set --json < creds.json` |
| Free port race: `net.Listen(":0")` acquires a port, closes it, another process takes it before portforward dials | Low: rare on dev machines; fails fast with "address in use" | Document as known limitation; retry on conflict (not in phase 1) |
| Long pg_restore (30+ min) exhausts reconnect attempts (3 × backoff) on flaky cluster | Medium: restores fail mid-flow | Make backoff configurable via flag (`--reconnect-attempts`, `--reconnect-backoff`); phase 3.1 hardcodes 3 attempts, phase 3.2 adds flags |
| Integration tests require k3d cluster running; CI must provision it | Low: already handled by existing `just local-pulumi` flows | Document test setup in `docs/reference/testing.md`; CI job runs `just k3d create-cluster` before `gotestsum --tags=integration` |
| Contract tests can't intercept `go run` (client-go bypasses kubectl); ORDER assertions move to integration tests | Medium: shell contract tests lose coverage | Accept: behavior assertions in Go tests (kill pod → reconnect); ORDER assertions in contract tests (recipe renders `go run`, not `kubectl`) |

## Related plans

- `docs/plans/go-migration/checks-probes.md` — Gate checks (namespace, secret, DB exists). Shares `internal/kube`, `internal/preflight`.
- `docs/plans/go-migration/wait-poll.md` — Wait loops (pod ready, PVC bound). Uses `kube.Classify` error types.
- `docs/plans/go-migration/gcp-converge.md` — GCP describe-then-create (KMS key, service account, IAM binding).
- `docs/plans/go-migration/pulumi-automation.md` — Optional: Pulumi Automation API for stack lifecycle.
- `docs/plans/go-migration/checks-phase1-implementation.md` — **Dependency**: Phase-1 foundation (kube.NewClients, kube.Classify, preflight.Run). Port-forward plan executes after phase-1 lands.
