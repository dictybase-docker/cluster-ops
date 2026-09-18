# Recipes for NATS (core pub/sub, unauthenticated, stateless) on the general
# nodes instance group. Guide: docs/nats-deploy.md. Everything assumes the
# cluster-env sub-shell (just cluster-env) so PULUMI_STACK,
# PULUMI_BACKEND_URL, PULUMI_GCP_CREDENTIALS, PROJECT_ID and KUBECONFIG are
# set. No recipe falls back to a dev stack.

# ── private helpers ──────────────────────────────────────────────────────────

# Resolve the Pulumi stack name, or fail. Never falls back to "dev".
# Usage: STACK=$(just nats _require-stack [--stack <name>])
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[no-cd]
_require-stack stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    STACK="{{ stack }}"
    if [[ -z "$STACK" ]]; then
        STACK="${PULUMI_STACK:-}"
    fi
    if [[ -z "$STACK" ]]; then
        echo "Error: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    echo "$STACK"

# ── public recipes ───────────────────────────────────────────────────────────

# Deploy NATS via the nats Helm chart, then wait for the server pod.
# One command for: ensure-stack, preview, apply, readiness.
# Usage: just nats deploy [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Namespace the release deploys into; must match properties.namespace in the stack config")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('nats')]
[no-cd]
deploy namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="nats"
    NS="{{ namespace }}"

    STACK=$(just nats _require-stack --stack "{{ stack }}")
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: namespace '$NS' does not exist — run 'just gcp-pulumi apply-namespaces' (pulumi setup §5) first." >&2
        exit 1
    fi

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"

    # Validate the readiness target against the stack BEFORE touching state:
    # the Helm release deploys into properties.namespace, so --namespace must
    # match it.
    CFG_NS=$(pulumi -C "$FOLDER" -s "$STACK" config get --path properties.namespace 2>/dev/null || echo "")
    if [[ -n "$CFG_NS" && "$CFG_NS" != "$NS" ]]; then
        echo "Error: --namespace '$NS' does not match properties.namespace '$CFG_NS' on stack '$STACK'." >&2
        exit 1
    fi

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    if ! kubectl get sts -n "$NS" nats >/dev/null 2>&1; then
        echo "Error: statefulset/nats not found in '$NS' after apply." >&2
        kubectl get sts,deploy -n "$NS" >&2 || true
        exit 1
    fi

    ready=0
    for i in $(seq 1 "{{ retries }}"); do
        ready=$(kubectl get sts -n "$NS" nats -o json 2>/dev/null \
            | jq '.status.readyReplicas // 0')
        if [[ "${ready:-0}" -ge 1 ]]; then
            echo "NATS server pod ready in $NS."
            break
        fi
        echo "Waiting for NATS server pod in $NS (try $i/{{ retries }})..."
        sleep "{{ interval }}"
    done
    if [[ "${ready:-0}" -lt 1 ]]; then
        echo "Error: NATS server pod not ready in $NS." >&2
        kubectl get pods -n "$NS" -l app.kubernetes.io/name=nats -o wide >&2 || true
        exit 1
    fi

    echo
    kubectl get svc -n "$NS" nats

# Post-install check: server pod, Service, client handshake. Exits non-zero
# on failure. The server runs unauthenticated, so the handshake needs no
# credentials — a failed rtt is a real connectivity problem.
# Usage: just nats verify [--namespace <ns>]
[arg("namespace", long="namespace", short="n", help="Namespace NATS runs in")]
[group('nats')]
[no-cd]
verify namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    failures=0

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_COLOR=0

    sts_ready=$(kubectl get sts -n "$NS" nats -o json 2>/dev/null \
        | jq '.status.readyReplicas // 0' || echo 0)
    if [[ "${sts_ready:-0}" -ge 1 ]]; then
        ok "NATS server StatefulSet has ${sts_ready:-0} ready replica(s)"
    else
        bad "no ready NATS server replica in $NS"
    fi

    port=$(kubectl get svc -n "$NS" nats -o jsonpath='{.spec.ports[?(@.name=="nats")].port}' 2>/dev/null | head -n1 || true)
    if [[ "$port" == "4222" ]]; then
        ok "Service nats exposes 4222"
    else
        bad "Service nats missing or client port not 4222"
    fi

    # Client handshake via the chart's nats-box (carries the nats CLI):
    # the server is unauthenticated, so a successful rtt proves the wire end
    # to end. Only runs when the server is ready.
    if [[ "${sts_ready:-0}" -ge 1 ]]; then
        if ! kubectl get deployment -n "$NS" nats-box >/dev/null 2>&1; then
            bad "nats-box deployment missing in $NS (handshake skipped)"
        else
            if kubectl exec -n "$NS" deploy/nats-box -- \
                nats --server "nats://nats.$NS.svc.cluster.local:4222" rtt >/dev/null 2>&1; then
                ok "client rtt succeeded"
            else
                bad "client rtt failed — see reference/nats/troubleshooting.md"
            fi
        fi
    fi

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d check(s) failed. See reference/nats/troubleshooting.md.\n' "$failures"
        exit 1
    fi
    printf 'All checks passed.\n'

# Destroy the NATS stack: the Helm release with the server, nats-box, and the
# config. Core pub/sub keeps no message persistence, so nothing else survives
# to delete.
# Usage: just nats teardown [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('nats')]
[no-cd]
teardown stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    STACK=$(just nats _require-stack --stack "{{ stack }}")

    echo "Destroying nats stack '$STACK'..."
    just gcp-pulumi remove-resource --folder nats --stack "$STACK"

    echo "Teardown complete. Shared namespaces and instance groups are untouched."
