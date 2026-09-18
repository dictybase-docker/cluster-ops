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

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"

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
        printf '\033[32mAll pool checks passed.\033[0m Next: just redis deploy.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/redis/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi

# Deploy standalone Redis 8, then wait for the pod.
# One command for: ensure-stack, preview, apply, readiness. The server runs
# unauthenticated — no password to supply.
# Usage: just redis deploy [--name <name>] [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("name", long="name", help="Deployment/Service name (default redis)")]
[arg("namespace", long="namespace", short="n", help="Namespace Redis is installed into")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('redis')]
[no-cd]
deploy name="redis" namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-standalone"
    NS="{{ namespace }}"
    NAME="{{ name }}"

    STACK=$(just redis _require-stack --stack "{{ stack }}")

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
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

# Post-install check: pool, pod, PVC, Service, PING handshake.
# Exits non-zero on failure. The server runs unauthenticated — no password.
# Usage: just redis verify [--name <name>] [--namespace <ns>] [--pool <label>] [--node-count <n>]
[arg("name", long="name", help="Deployment/Service name (default redis)")]
[arg("namespace", long="namespace", short="n", help="Namespace Redis runs in")]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('redis')]
[no-cd]
verify name="redis" namespace="prod" pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    NAME="{{ name }}"
    NS="{{ namespace }}"
    POOL="{{ pool }}"
    WANT_NODES="{{ node_count }}"
    failures=0

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_COLOR=0

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

    # PING handshake: the server is unauthenticated, so a bare PING must
    # return PONG — a failure is a real connectivity problem.
    if [[ "$pods_ready" -ge 1 ]]; then
        POD=$(kubectl get pods -n "$NS" -l "app=${NAME}" --no-headers -o custom-columns=":metadata.name" | head -n1)
        pong=$(kubectl exec -n "$NS" "$POD" -- sh -c \
            "redis-cli PING" 2>/dev/null || echo "")
        if [[ "$pong" == "PONG" ]]; then
            ok "PING returned PONG"
        else
            bad "PING failed (got: ${pong:-<no output>})"
        fi
    fi

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d check(s) failed. See reference/redis/troubleshooting.md.\n' "$failures"
        exit 1
    fi
    printf 'All checks passed.\n'

# Destroy the Redis stack. Removes the Deployment and Service; the data PVC
# is deleted too when --delete-pvc yes is passed.
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

# ── backup ───────────────────────────────────────────────────────────────────

# Wait for a Kubernetes Job to complete; tolerates the "not yet created"
# window and the TTL cleanup window.
# Usage: just redis _wait-job --namespace <ns> --job <name> [--retries <n>] [--interval <s>]
[private]
[arg("namespace", long="namespace", short="n", help="Namespace the Job runs in")]
[arg("job", long="job", short="j", help="Job name")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[no-cd]
_wait-job namespace job retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    JOB="{{ job }}"
    RETRIES="{{ retries }}"
    INTERVAL="{{ interval }}"
    seen=0

    for i in $(seq 1 "$RETRIES"); do
        set +e
        out=$(kubectl get job -n "$NS" "$JOB" -o json 2>&1)
        status=$?
        set -e
        if [[ $status -ne 0 ]]; then
            if [[ $seen -eq 1 ]]; then
                echo "Job $JOB is gone from $NS — it finished and its TTL removed it."
                exit 0
            fi
            if echo "$out" | grep -qi "not found"; then
                echo "Waiting for job $JOB to appear in $NS (try $i/$RETRIES)..."
                sleep "$INTERVAL"
                continue
            fi
            echo "$out" >&2
            echo "Error: kubectl get job $JOB -n $NS failed." >&2
            exit 1
        fi
        seen=1
        succeeded=$(printf '%s\n' "$out" | jq -r '.status.succeeded // 0')
        failed=$(printf '%s\n' "$out" | jq -r '.status.failed // 0')
        if [[ "$succeeded" -ge 1 ]]; then
            echo "Job $JOB completed in $NS."
            exit 0
        fi
        if [[ "$failed" -ge 1 ]]; then
            echo "Error: job $JOB in $NS failed. Last 100 log lines:" >&2
            kubectl logs -n "$NS" "job/$JOB" --all-containers --tail=100 || true
            exit 1
        fi
        echo "Waiting for job $JOB in $NS (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done

    echo "Error: job $JOB in $NS did not finish after $((RETRIES * INTERVAL))s." >&2
    kubectl get job -n "$NS" "$JOB" || true
    exit 1

# Create the backup writer service account in THIS cluster's GCP project and
# mint credentials/<project-id>/redis-backup-sa.json — the key file
# configure-backup-secrets reads at pulumi up time. Grants
# roles/storage.objectAdmin pinned to the backup bucket by an IAM condition,
# so it works BEFORE deploy-backup creates the bucket. Run once per project;
# safe to re-run (key creation is skipped when the file already exists).
# Usage: just redis setup-backup-sa [--bucket <name>] [--project <id>] [--sa-name <n>] [--key-file <path>]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[group('redis')]
[no-cd]
setup-backup-sa bucket="restic-redis-backup-dcr-kube1" project="" sa_name="redis-backup-sa" key_file="":
    just redis _setup-backup-sa \
        --bucket "{{ bucket }}" \
        --project "{{ project }}" \
        --sa-name "{{ sa_name }}" \
        --key-file "{{ key_file }}"

# Shared body of setup-backup-sa, called by configure-backup-secrets too. Not
# meant to be invoked directly — use either of those two instead.
[private]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[no-cd]
_setup-backup-sa bucket="restic-redis-backup-dcr-kube1" project="" sa_name="redis-backup-sa" key_file="":
    #!/usr/bin/env bash
    set -euo pipefail

    BUCKET="{{ bucket }}"
    PROJECT="{{ project }}"
    [ -z "${PROJECT}" ] && PROJECT="${PROJECT_ID:-}"
    SA_NAME="{{ sa_name }}"
    KEY_FILE="{{ key_file }}"

    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi

    SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
    KEY_FILE="${KEY_FILE:-credentials/${PROJECT}/${SA_NAME}.json}"

    echo "Project        : ${PROJECT}"
    echo "Backup bucket  : gs://${BUCKET}"
    echo "Service account: ${SA_EMAIL}"
    echo "Key file       : ${KEY_FILE}"
    echo

    # Idempotent: probe first so a re-run does not spew a conflict ERROR.
    if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT" >/dev/null 2>&1; then
        echo "Service account already exists — continuing."
    else
        gcloud iam service-accounts create "$SA_NAME" \
            --project "$PROJECT" \
            --display-name "Redis backup GCS writer"
    fi

    # Least privilege, ordering-safe: a project-level binding with an IAM
    # condition pinning it to the backup bucket. A plain bucket-level binding
    # is impossible here — deploy-backup creates the bucket AFTER this key
    # must already exist. Additive and idempotent on re-run.
    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectAdmin \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=redis-backup-bucket-writer,description=Object admin limited to the Redis restic backup bucket"

    # Key creation is NOT idempotent — every run mints a new key and old ones
    # keep working until deleted. Reuse an existing key file when present.
    if [[ -f "$KEY_FILE" ]]; then
        echo
        echo "Key file '$KEY_FILE' already exists — NOT creating another key."
        echo "Delete it first if you really want to mint a new one, and prune the old key with:"
        echo "  gcloud iam service-accounts keys list --iam-account $SA_EMAIL --project $PROJECT"
    else
        mkdir -p "$(dirname "$KEY_FILE")"
        gcloud iam service-accounts keys create "$KEY_FILE" \
            --iam-account "$SA_EMAIL" \
            --project "$PROJECT"
        chmod 600 "$KEY_FILE"
        echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."
    fi

    echo
    echo "Next: just redis configure-backup-secrets --restic-password '<restic-pass>'"

# Create the `redis-backup-auth` backup Secret. Creates no namespaces — the
# namespace-bootstrap stack owns prod/operators (just gcp-pulumi
# apply-namespaces, docs/pulumi-setup.md §5).
# One command for: ensure SA + key, ensure-stack, all four secret values,
# preview, apply, verify. Apply this FIRST — the backup jobs read the Secret
# at apply time.
# Usage: just redis configure-backup-secrets --restic-password <pw> [--no-setup-sa] [--gcs-project <id>] [--gcs-key-file <path>] [--key-name <k>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="restic repository password (required)")]
[arg("setup_sa", long="setup-sa", help="Create/refresh the redis-backup-sa service account + key first (disable with --no-setup-sa when pointing at your own key)")]
[arg("gcs_project", long="gcs-project", short="g", help="GCP project id that owns the backup bucket (defaults to PROJECT_ID from the cluster env)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to a GCS-capable service account JSON key (defaults to credentials/<project-id>/redis-backup-sa.json; read at pulumi up time)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("namespace", long="namespace", short="n", help="Optional guard: must equal the namespace-bootstrap appNamespace export (default: no override)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('redis')]
[no-cd]
configure-backup-secrets restic_password setup_sa="true" gcs_project="" gcs_key_file="" key_name="gcsCredentials" namespace="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-backup-secrets"
    NS="{{ namespace }}"
    RESTIC_PASSWORD={{ quote(restic_password) }}
    SETUP_SA="{{ setup_sa }}"
    GCS_PROJECT="{{ gcs_project }}"
    KEY_FILE="{{ gcs_key_file }}"
    KEY_NAME="{{ key_name }}"

    # Both GCS arguments default off the active cluster env: `just cluster-env`
    # sources .env.<env>.<cluster>, which exports PROJECT_ID.
    [[ -z "$GCS_PROJECT" ]] && GCS_PROJECT="${PROJECT_ID:-}"
    [[ -z "$KEY_FILE" && -n "$GCS_PROJECT" ]] && KEY_FILE="credentials/${GCS_PROJECT}/redis-backup-sa.json"

    if [[ -z "$RESTIC_PASSWORD" ]]; then
        echo "Error: --restic-password is required." >&2
        exit 1
    fi
    if [[ -z "$GCS_PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --gcs-project." >&2
        exit 1
    fi
    if [[ -z "$KEY_FILE" ]]; then
        echo "Error: --gcs-key-file is required." >&2
        exit 1
    fi

    # The default key layout is produced by the SA setup helper. Pointing at a
    # custom key (--gcs-key-file) skips it; force it back on with --setup-sa
    # or opt a default-layout run out with --no-setup-sa.
    DEFAULT_KEY_FILE="credentials/${GCS_PROJECT}/redis-backup-sa.json"
    if [[ "$SETUP_SA" == "true" && "$KEY_FILE" == "$DEFAULT_KEY_FILE" ]]; then
        just redis _setup-backup-sa --project "$GCS_PROJECT"
    elif [[ "$SETUP_SA" == "true" ]]; then
        echo "Custom --gcs-key-file given — skipping SA setup; verifying the key file exists."
    fi

    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Error: service account key '$KEY_FILE' does not exist on this machine." >&2
        exit 1
    fi
    # Absolute path: redis-backup-secrets/main.go reads this file with
    # os.ReadFile at `pulumi up` time, and pulumi -C changes the working
    # directory, so a relative path would resolve against the project dir,
    # not your shell.
    KEY_FILE=$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")

    STACK=$(just redis _require-stack --stack "{{ stack }}")

    # The Secret's namespace is NOT configured here — the redis-backup-secrets
    # program takes it from the namespace-bootstrap stack's appNamespace
    # export. An explicit --namespace must match it or the run stops.
    BOOT_NS=$(pulumi -C namespace-bootstrap stack output appNamespace --stack "$STACK")
    if [[ -n "$NS" && "$NS" != "$BOOT_NS" ]]; then
        echo "Error: --namespace '$NS' does not match the namespace-bootstrap export '$BOOT_NS' — the Secret cannot live there." >&2
        exit 1
    fi
    NS="$BOOT_NS"

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --secret "$FOLDER:properties.secret.resticPass=$RESTIC_PASSWORD" \
        --secret "$FOLDER:properties.secret.gcsProject=$GCS_PROJECT" \
        --secret "$FOLDER:properties.secret.serviceAccount.keyname=$KEY_NAME" \
        --secret "$FOLDER:properties.secret.serviceAccount.filepath=$KEY_FILE"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    SECRET_NAME=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.secret.name 2>/dev/null || echo "redis-backup-auth")

    # Namespace comes from the namespace-bootstrap stack; verify it is live.
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: namespace '$NS' does not exist — run 'just gcp-pulumi apply-namespaces' (pulumi setup §5) first." >&2
        exit 1
    fi
    echo
    kubectl get namespace "$NS"
    kubectl get secret "$SECRET_NAME" -n "$NS"
    echo "Keys in $SECRET_NAME:"
    kubectl get secret "$SECRET_NAME" -n "$NS" -o json | jq -r '.data | keys[] | "  " + .'

# Deploy the backup bucket, CronJob and immediate Job, then wait for that Job.
# One command for: gcp:project pin, ensure-stack, preview, apply, Job wait,
# log tail, CronJob check.
# Usage: just redis deploy-backup [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Namespace the backup Job runs in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('redis')]
[no-cd]
deploy-backup namespace="prod" stack="" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-backup"
    NS="{{ namespace }}"
    JOB="redis-immediate-backup-job"
    CRONJOB="redis-backup-cronjob"

    STACK=$(just redis _require-stack --stack "{{ stack }}")

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"

    # Pin the GCP project for the pulumi-gcp provider. Without it, the provider
    # falls back to the GOOGLE_CLOUD_PROJECT env var, which can carry a stale
    # value from another cluster env — bucket creates then target the wrong
    # project and fail 403. The env file also sets GOOGLE_CLOUD_PROJECT, but
    # the stack config wins and is immune to shell leaks.
    PROJECT="${PROJECT_ID:-}"
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'gcp:project' --value "$PROJECT"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just redis _wait-job --namespace "$NS" --job "$JOB" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    echo "Backup log tail (restic summary should be at the end):"
    kubectl logs -n "$NS" "job/$JOB" --tail=30 || \
        echo "Job already removed by its 15-minute TTL — nothing left to tail."

    echo
    kubectl get cronjob -n "$NS" "$CRONJOB"
