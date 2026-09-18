# Recipes for NATS (core pub/sub, stateless) on the general nodes instance
# group. Guide: docs/nats-deploy.md. Everything assumes the cluster-env
# sub-shell (just cluster-env) so PULUMI_STACK, PULUMI_BACKEND_URL,
# PULUMI_GCP_CREDENTIALS, PROJECT_ID and KUBECONFIG are set. No recipe falls
# back to a dev stack.

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

# Deploy NATS via the nats Helm chart, then wait for the server pod.
# One command for: ensure-stack, auth token secret, preview, apply, readiness.
# The token is never generated or defaulted — you supply it.
# Usage: just nats deploy --token <token> [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("token", long="token", short="t", help="NATS auth token (required; stored encrypted as properties.auth.token)")]
[arg("namespace", long="namespace", short="n", help="Namespace the release deploys into; must match properties.namespace in the stack config")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('nats')]
[no-cd]
deploy token namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="nats"
    NS="{{ namespace }}"
    TOKEN={{ quote(token) }}

    if [[ -z "$TOKEN" ]]; then
        echo "Error: --token is required; this recipe never invents a token." >&2
        exit 1
    fi

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

    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key properties.auth.token --value "$TOKEN"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    # The token is injected as a Secret-backed env variable, which a running
    # pod never refreshes — restart the server so it resolves $TOKEN at
    # startup (the NATS server expands config variables from its env).
    if ! kubectl get sts -n "$NS" nats >/dev/null 2>&1; then
        echo "Error: statefulset/nats not found in '$NS' after apply." >&2
        kubectl get sts,deploy -n "$NS" >&2 || true
        exit 1
    fi
    kubectl -n "$NS" rollout restart statefulset/nats

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

# Post-install check: server pod, Service, Secret, auth handshake.
# Exits non-zero on failure.
# Usage: just nats verify [--namespace <ns>] [--secret <name>]
[arg("namespace", long="namespace", short="n", help="Namespace NATS runs in")]
[arg("secret", long="secret", short="e", help="Auth Secret name (default nats-auth)")]
[group('nats')]
[no-cd]
verify namespace="prod" secret="nats-auth":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    SECRET="{{ secret }}"
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

    secret_ok=0
    if kubectl get secret -n "$NS" "$SECRET" >/dev/null 2>&1; then
        ok "Secret $SECRET present"
        secret_ok=1
    else
        bad "Secret $SECRET missing in $NS"
    fi

    # Auth handshake via the chart's nats-box (carries the nats CLI):
    # an authenticated rtt must succeed; an unauthenticated one must fail
    # with an authorization violation — any other failure is a real problem,
    # not proof that auth is on. The token is passed as an exec argument,
    # never interpolated into a shell string. Only runs when both the server
    # and the Secret checks passed.
    if [[ "${sts_ready:-0}" -ge 1 && "$secret_ok" -eq 1 ]]; then
        if ! kubectl get deployment -n "$NS" nats-box >/dev/null 2>&1; then
            bad "nats-box deployment missing in $NS (auth handshake skipped)"
        else
            TOKEN=$(kubectl get secret -n "$NS" "$SECRET" -o jsonpath='{.data.token}' | base64 -d)
            if kubectl exec -n "$NS" deploy/nats-box -- \
                nats --server "nats://nats.$NS.svc.cluster.local:4222" --token "$TOKEN" rtt >/dev/null 2>&1; then
                ok "authenticated rtt succeeded"
            else
                bad "authenticated rtt failed — check Secret $SECRET against the server token"
            fi
            denied=$(kubectl exec -n "$NS" deploy/nats-box -- \
                nats --server "nats://nats.$NS.svc.cluster.local:4222" rtt 2>&1 || true)
            if [[ "${denied,,}" == *"authorization"* ]]; then
                ok "unauthenticated rtt rejected (authorization violation)"
            else
                bad "unauthenticated rtt did not fail with an authorization violation (got: ${denied:-<no output>}) — auth is off or unreachable"
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
# auth Secret. Core pub/sub keeps no message persistence, so nothing else
# survives to delete.
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
