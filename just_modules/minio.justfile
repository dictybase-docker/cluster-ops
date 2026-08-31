# Recipes for standalone MinIO on the stateful-db pool. Guide: docs/minio-deploy.md.
# Everything assumes the cluster-env sub-shell (just cluster-env) so
# PULUMI_STACK, PULUMI_BACKEND_URL, PULUMI_GCP_CREDENTIALS, PROJECT_ID and
# KUBECONFIG are set. No recipe falls back to a dev stack.

# ── private helpers ──────────────────────────────────────────────────────────

# Resolve the Pulumi stack name, or fail. Never falls back to "dev".
# Usage: STACK=$(just minio _require-stack [--stack <name>])
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
# Usage: just minio check-pool [--pool <label>] [--node-count <n>]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('minio')]
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
        printf '\033[32mAll pool checks passed.\033[0m Proceed to Install MinIO.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/minio/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi

# Deploy standalone MinIO, then wait for the pod.
# One command for: ensure-stack, root credentials secret, preview, apply, readiness.
# The root credentials are never generated or defaulted — you supply them.
# Usage: just minio deploy --root-user <user> --root-password <pw> [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("root_user", long="root-user", short="u", help="MinIO root user (required; stored encrypted as properties.secret.userName)")]
[arg("root_password", long="root-password", short="p", help="MinIO root password (required; stored encrypted as properties.secret.password)")]
[arg("namespace", long="namespace", short="n", help="Namespace MinIO is installed into")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('minio')]
[no-cd]
deploy root_user root_password namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio"
    NS="{{ namespace }}"
    ROOT_USER="{{ root_user }}"
    ROOT_PASSWORD="{{ root_password }}"

    if [[ -z "$ROOT_USER" || -z "$ROOT_PASSWORD" ]]; then
        echo "Error: --root-user and --root-password are both required; this recipe never invents credentials." >&2
        exit 1
    fi

    STACK=$(just minio _require-stack --stack "{{ stack }}")

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key properties.secret.userName --value "$ROOT_USER"
    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key properties.secret.password --value "$ROOT_PASSWORD"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    for i in $(seq 1 "{{ retries }}"); do
        ready=$(kubectl get pods -n "$NS" -l app.kubernetes.io/name=minio -o json 2>/dev/null \
            | jq '[.items[] | select(.status.phase == "Running")
                | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
        if [[ "${ready:-0}" -ge 1 ]]; then
            echo "MinIO pod ready in $NS."
            break
        fi
        echo "Waiting for MinIO pod in $NS (try $i/{{ retries }})..."
        sleep "{{ interval }}"
    done
    if [[ "${ready:-0}" -lt 1 ]]; then
        echo "Error: MinIO pod not ready in $NS." >&2
        kubectl get pods -n "$NS" -l app.kubernetes.io/name=minio -o wide >&2 || true
        exit 1
    fi

    echo
    kubectl get svc -n "$NS" minio

# Mirror one bucket from a source MinIO/S3 endpoint into this MinIO.
# Source credentials are explicit flags; target credentials are read from the
# in-cluster Secret. Re-runnable — mc mirror only copies what changed.
# Usage: just minio import-bucket --bucket <name> --source-url <url> --source-user <user> --source-password <pw> [--namespace <ns>] [--secret <name>] [--remove]
[arg("bucket", long="bucket", short="b", help="Bucket name (must exist at the source; created here if missing)")]
[arg("source_url", long="source-url", short="s", help="Source S3 endpoint URL, e.g. https://storage.source.example:9000")]
[arg("source_user", long="source-user", short="u", help="Source access key")]
[arg("source_password", long="source-password", short="p", help="Source secret key")]
[arg("namespace", long="namespace", short="n", help="Namespace MinIO runs in")]
[arg("secret", long="secret", short="e", help="Secret holding this MinIO's root credentials")]
[arg("remove", long="remove", help="Pass --remove to mc mirror (deletes target objects missing at source; default OFF)")]
[group('minio')]
[no-cd]
import-bucket bucket source_url source_user source_password namespace="prod" secret="minio-root" remove="no":
    #!/usr/bin/env bash
    set -euo pipefail

    BUCKET="{{ bucket }}"
    SRC_URL="{{ source_url }}"
    SRC_USER="{{ source_user }}"
    SRC_PASSWORD="{{ source_password }}"
    NS="{{ namespace }}"
    SECRET="{{ secret }}"

    for v in "$BUCKET" "$SRC_URL" "$SRC_USER" "$SRC_PASSWORD"; do
        if [[ -z "$v" ]]; then
            echo "Error: --bucket, --source-url, --source-user and --source-password are all required." >&2
            exit 1
        fi
    done

    USER_KEY=$(kubectl get secret "$SECRET" -n "$NS" -o jsonpath='{.data.rootUser}' | base64 -d)
    PASS_KEY=$(kubectl get secret "$SECRET" -n "$NS" -o jsonpath='{.data.rootPassword}' | base64 -d)
    if [[ -z "$USER_KEY" || -z "$PASS_KEY" ]]; then
        echo "Error: could not read root credentials from Secret '$SECRET' in '$NS'." >&2
        exit 1
    fi

    # Reach the cluster-internal API through a port-forward.
    kubectl port-forward -n "$NS" svc/minio 19000:9000 >/dev/null 2>&1 &
    PF_PID=$!
    trap 'kill $PF_PID 2>/dev/null || true' EXIT
    for i in $(seq 1 30); do
        nc -z localhost 19000 2>/dev/null && break
        sleep 1
    done

    mc alias set minio-src "$SRC_URL" "$SRC_USER" "$SRC_PASSWORD" --quiet
    mc alias set minio-dst "http://localhost:19000" "$USER_KEY" "$PASS_KEY" --quiet

    echo "Ensuring bucket '$BUCKET' exists on the target..."
    mc mb --ignore-existing "minio-dst/$BUCKET"

    REMOVE_FLAG=""
    [[ "{{ remove }}" == "yes" ]] && REMOVE_FLAG="--remove"

    echo "Mirroring minio-src/$BUCKET -> minio-dst/$BUCKET ..."
    mc mirror --preserve --overwrite $REMOVE_FLAG "minio-src/$BUCKET" "minio-dst/$BUCKET"

    echo
    echo "Done. Object counts:"
    mc ls --recursive "minio-dst/$BUCKET" | wc -l

# Post-install check: pool, pod, PVC, Service, Secret. Exits non-zero on failure.
# Usage: just minio verify [--namespace <ns>] [--secret <name>] [--pool <label>] [--node-count <n>]
[arg("namespace", long="namespace", short="n", help="Namespace MinIO runs in")]
[arg("secret", long="secret", short="e", help="Root credentials Secret name")]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('minio')]
[no-cd]
verify namespace="prod" secret="minio-root" pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

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

    pods_ready=$(kubectl get pods -n "$NS" -l app.kubernetes.io/name=minio -o json \
        | jq '[.items[] | select(.status.phase == "Running")
        | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
    if [[ "$pods_ready" -ge 1 ]]; then
        ok "$pods_ready MinIO pod(s) Running and ready"
    else
        bad "no ready MinIO pod in $NS"
    fi

    pvc_bound=$(kubectl get pvc -n "$NS" -o json \
        | jq '[.items[] | select(.status.phase == "Bound")
        | select(.metadata.name | startswith("minio"))] | length')
    if [[ "$pvc_bound" -ge 1 ]]; then
        ok "$pvc_bound Bound minio PVC(s)"
    else
        bad "no Bound minio PVC in $NS"
    fi

    port=$(kubectl get svc -n "$NS" minio -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
    if [[ "$port" == "9000" ]]; then
        ok "Service minio exposes 9000"
    else
        bad "Service minio missing or not on 9000"
    fi

    if kubectl get secret -n "$NS" "$SECRET" >/dev/null 2>&1; then
        ok "Secret $SECRET present"
    else
        bad "Secret $SECRET missing in $NS"
    fi

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d check(s) failed. See reference/minio/troubleshooting.md.\n' "$failures"
        exit 1
    fi
    printf 'All checks passed.\n'

# Destroy the MinIO stack. Removes the Helm release and Secret; the data PVC
# is deleted too when --delete-pvc yes is passed.
# Usage: just minio teardown --namespace <ns> --delete-pvc yes [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Namespace MinIO runs in")]
[arg("delete_pvc", long="delete-pvc", help="Must be 'yes' — deletes the data PVC (all objects)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('minio')]
[no-cd]
teardown namespace delete_pvc="no" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    DELETE_PVC="{{ delete_pvc }}"
    STACK=$(just minio _require-stack --stack "{{ stack }}")

    if [[ "$DELETE_PVC" != "yes" ]]; then
        echo "Refusing teardown." >&2
        echo "This destroys the minio stack '$STACK' and deletes the data PVC" >&2
        echo "(ALL objects in every bucket). Re-run with --delete-pvc yes." >&2
        exit 1
    fi

    echo "Destroying minio stack '$STACK'..."
    just gcp-pulumi remove-resource --folder minio --stack "$STACK"

    echo "Deleting leftover minio PVCs in '$NS'..."
    kubectl delete pvc -n "$NS" -l app.kubernetes.io/name=minio --ignore-not-found

    echo "Teardown complete."
