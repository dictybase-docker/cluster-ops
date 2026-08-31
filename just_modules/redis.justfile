# Recipes for standalone Redis on the stateful-db pool. Guide: docs/redis-deploy.md.
# Everything assumes the cluster-env sub-shell (just cluster-env) so
# PULUMI_STACK, PULUMI_BACKEND_URL, PULUMI_GCP_CREDENTIALS, PROJECT_ID and
# KUBECONFIG are set. No recipe falls back to a dev stack.

# ── private helpers ──────────────────────────────────────────────────────────

# Resolve the Pulumi stack name, or fail. Never falls back to "dev".
# Usage: STACK=$(just redis _require-stack [--stack <name>])
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

# Verify the stateful-db pool: node count, taint, Ready, zone spread.
# Usage: just redis check-pool [--pool <label>] [--node-count <n>]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('redis')]
[no-cd]
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    POOL="{{ pool }}"
    WANT_NODES="{{ node_count }}"
    failures=0

    ok()   { printf '\033[32mPASS\033[0m  %s\n' "$1"; }
    bad()  { printf '\033[31mFAIL\033[0m  %s\n' "$1"; failures=$((failures + 1)); }
    info() { printf '\033[34mINFO\033[0m  %s\n' "$1"; }

    nodes_json=$(kubectl get nodes -l "pool=$POOL" -o json)
    node_count=$(printf '%s\n' "$nodes_json" | jq '.items | length')

    if [[ "$node_count" -eq "$WANT_NODES" ]]; then
        ok "$node_count node(s) with pool=$POOL"
    else
        bad "$node_count node(s) with pool=$POOL, expected $WANT_NODES"
    fi

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
        printf '\033[32mAll pool checks passed.\033[0m Proceed to Install Redis.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/redis/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi

# Deploy standalone Redis 8, then wait for the pod.
# One command for: ensure-stack, password secret, preview, apply, readiness.
# The password is never generated or defaulted — you supply it.
# Usage: just redis deploy --password <pw> [--name <name>] [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("password", long="password", short="p", help="Redis --requirepass password (required; stored encrypted as properties.auth.password)")]
[arg("name", long="name", help="Deployment/Service name (default redis)")]
[arg("namespace", long="namespace", short="n", help="Namespace Redis is installed into")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('redis')]
[no-cd]
deploy password name="redis" namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-standalone"
    NS="{{ namespace }}"
    NAME="{{ name }}"
    PASSWORD="{{ password }}"

    if [[ -z "$PASSWORD" ]]; then
        echo "Error: --password is required; this recipe never invents a password." >&2
        exit 1
    fi

    STACK=$(just redis _require-stack --stack "{{ stack }}")

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key properties.auth.password --value "$PASSWORD"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    ready=0
    for i in $(seq 1 "{{ retries }}"); do
        ready=$(kubectl get pods -n "$NS" -l "app=${NAME}" -o json 2>/dev/null \
            | jq '[.items[] | select(.status.phase == "Running")
                | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
        if [[ "${ready:-0}" -ge 1 ]]; then
            echo "Redis pod ready in $NS."
            break
        fi
        echo "Waiting for Redis pod in $NS (try $i/{{ retries }})..."
        sleep "{{ interval }}"
    done
    if [[ "${ready:-0}" -lt 1 ]]; then
        echo "Error: Redis pod not ready in $NS." >&2
        kubectl get pods -n "$NS" -l "app=${NAME}" -o wide >&2 || true
        exit 1
    fi

    echo
    kubectl get svc -n "$NS" "$NAME"

# Post-install check: pool, pod, PVC, Service, Secret, auth handshake.
# Exits non-zero on failure.
# Usage: just redis verify [--name <name>] [--namespace <ns>] [--secret <name>] [--pool <label>] [--node-count <n>]
[arg("name", long="name", help="Deployment/Service name (default redis)")]
[arg("namespace", long="namespace", short="n", help="Namespace Redis runs in")]
[arg("secret", long="secret", short="e", help="Auth Secret name (default redis-auth)")]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('redis')]
[no-cd]
verify name="redis" namespace="prod" secret="redis-auth" pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    NAME="{{ name }}"
    NS="{{ namespace }}"
    SECRET="{{ secret }}"
    POOL="{{ pool }}"
    WANT_NODES="{{ node_count }}"
    failures=0

    ok()  { printf 'PASS  %s\n' "$1"; }
    bad() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }

    nodes_json=$(kubectl get nodes -l "pool=$POOL" -o json)
    node_count=$(printf '%s\n' "$nodes_json" | jq '.items | length')
    if [[ "$node_count" -eq "$WANT_NODES" ]]; then
        ok "$node_count node(s) with pool=$POOL"
    else
        bad "$node_count node(s) with pool=$POOL, expected $WANT_NODES"
    fi

    pods_ready=$(kubectl get pods -n "$NS" -l "app=${NAME}" -o json \
        | jq '[.items[] | select(.status.phase == "Running")
        | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
    if [[ "$pods_ready" -ge 1 ]]; then
        ok "$pods_ready Redis pod(s) Running and ready"
    else
        bad "no ready Redis pod in $NS"
    fi

    pvc_bound=$(kubectl get pvc -n "$NS" "${NAME}-data" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ "$pvc_bound" == "Bound" ]]; then
        ok "PVC ${NAME}-data Bound"
    else
        bad "PVC ${NAME}-data not Bound (status: ${pvc_bound:-missing})"
    fi

    port=$(kubectl get svc -n "$NS" "$NAME" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
    if [[ "$port" == "6379" ]]; then
        ok "Service $NAME exposes 6379"
    else
        bad "Service $NAME missing or not on 6379"
    fi

    if kubectl get secret -n "$NS" "$SECRET" >/dev/null 2>&1; then
        ok "Secret $SECRET present"
    else
        bad "Secret $SECRET missing in $NS"
    fi

    # Auth handshake: PING with the password from the Secret must return PONG.
    if [[ "$pods_ready" -ge 1 ]]; then
        POD=$(kubectl get pods -n "$NS" -l "app=${NAME}" --no-headers -o custom-columns=":metadata.name" | head -n1)
        PASSWORD=$(kubectl get secret -n "$NS" "$SECRET" -o jsonpath='{.data.password}' | base64 -d)
        pong=$(kubectl exec -n "$NS" "$POD" -- sh -c \
            "redis-cli -a '$PASSWORD' --no-auth-warning PING" 2>/dev/null || echo "")
        if [[ "$pong" == "PONG" ]]; then
            ok "authenticated PING returned PONG"
        else
            bad "authenticated PING failed (got: ${pong:-<no output>})"
        fi
    fi

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d check(s) failed. See reference/redis/troubleshooting.md.\n' "$failures"
        exit 1
    fi
    printf 'All checks passed.\n'

# Destroy the Redis stack. Removes the Deployment, Service and Secret; the
# data PVC is deleted too when --delete-pvc yes is passed.
# Usage: just redis teardown --namespace <ns> --name <name> --delete-pvc yes [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Namespace Redis runs in")]
[arg("name", long="name", help="Deployment name (default redis)")]
[arg("delete_pvc", long="delete-pvc", help="Must be 'yes' — deletes the data PVC (all Redis data)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('redis')]
[no-cd]
teardown namespace name="redis" delete_pvc="no" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    NAME="{{ name }}"
    DELETE_PVC="{{ delete_pvc }}"
    STACK=$(just redis _require-stack --stack "{{ stack }}")

    if [[ "$DELETE_PVC" != "yes" ]]; then
        echo "Refusing teardown." >&2
        echo "This destroys the redis-standalone stack '$STACK' and deletes the" >&2
        echo "data PVC (ALL Redis data). Re-run with --delete-pvc yes." >&2
        exit 1
    fi

    echo "Destroying redis-standalone stack '$STACK'..."
    just gcp-pulumi remove-resource --folder redis-standalone --stack "$STACK"

    echo "Deleting leftover PVC ${NAME}-data in '$NS'..."
    kubectl delete pvc -n "$NS" "${NAME}-data" --ignore-not-found

    echo "Teardown complete."
