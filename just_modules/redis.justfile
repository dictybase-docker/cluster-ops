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

    set +e
    out=$(go run ./cmd/cluster-ops preflight redis \
        --namespace {{ quote(namespace) }} --name {{ quote(name) }} \
        --pool {{ quote(pool) }} --node-count {{ quote(node_count) }})
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: $code check(s) failed. See reference/redis/troubleshooting.md." >&2
        exit "$code"
    fi
    echo "All checks passed."

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

    # Key converge is idempotent: a live key file whose private_key_id matches
    # an upstream key is reused; a missing or dead one mints exactly once
    # (written 0600). Prune accumulated keys with:
    #   gcloud iam service-accounts keys list --iam-account $SA_EMAIL --project $PROJECT
    go run ./cmd/cluster-ops sa create-key \
        --name "$SA_NAME" --project "$PROJECT" --output-file "$KEY_FILE"
    echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."

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

    SOURCE_SECRET="redis-backup-source"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    for key in resticSecret bucketSecret projectSecret; do
        configured=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path "properties.${key}.name" 2>/dev/null || true)
        if [[ "$configured" == "$SOURCE_SECRET" ]]; then
            echo "Error: properties.${key}.name is '$SOURCE_SECRET' on $FOLDER stack '$STACK'." >&2
            echo "That Secret is the read-only cross-project BOOTSTRAP identity and must never drive backups." >&2
            echo "Fix: set it back to 'redis-backup-auth' (re-run configure-backup-secrets if needed)." >&2
            exit 1
        fi
    done

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

# ── restore and cross-cluster bootstrap ──────────────────────────────────────

# Print a Secret value from the active (source) cluster environment.
# Defaults to the restic repository password in Secret `redis-backup-auth`.
# Usage: just redis source-secret-value [--namespace <ns>] [--secret <name>] [--key <key>]
[arg("namespace", long="namespace", short="n", help="Namespace containing the Secret (default prod)")]
[arg("secret", long="secret", short="s", help="Secret name (default redis-backup-auth)")]
[arg("key", long="key", short="k", help="Secret data key (default resticPass)")]
[group('redis')]
[no-cd]
source-secret-value namespace="prod" secret="redis-backup-auth" key="resticPass":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SECRET={{ quote(secret) }}
    KEY={{ quote(key) }}
    if [[ -z "$NS" || -z "$SECRET" || -z "$KEY" ]]; then
        echo "Error: --namespace, --secret and --key must be non-empty." >&2
        exit 1
    fi
    if [[ -z "${CLUSTER_ENV:-}" || -z "${CLUSTER_NAME:-}" || \
          -z "${KUBECONFIG:-}" || ! -r "$KUBECONFIG" ]]; then
        echo "Error: enter the source cluster with 'just cluster-env' before reading its Secret." >&2
        exit 1
    fi
    if ! secret_json=$(kubectl get secret "$SECRET" -n "$NS" -o json 2>&1); then
        echo "Error: unable to read Secret '$SECRET' in namespace '$NS': $secret_json" >&2
        exit 1
    fi
    if ! value_b64=$(printf '%s\n' "$secret_json" | jq -er --arg key "$KEY" '.data[$key] | strings' 2>/dev/null); then
        echo "Error: Secret '$SECRET' in '$NS' is missing a valid '$KEY' key." >&2
        exit 1
    fi
    if ! value=$(printf '%s' "$value_b64" | base64 -d 2>/dev/null) || [[ -z "$value" ]]; then
        echo "Error: Secret '$SECRET' in '$NS' has an invalid or empty '$KEY' value." >&2
        exit 1
    fi
    printf '%s\n' "$value"

# Create a read-only service account in the SOURCE GCP project and grant it
# objectViewer on the source Redis restic bucket.
# Usage: just redis grant-source-bucket-reader [--bucket <name>] [--source-project <id>] [--sa-name <n>] [--key-file <path>] [--stack <name>]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (optional; inferred from redis-backup stack config)")]
[arg("source_project", long="source-project", short="p", help="SOURCE GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<source-project>/<sa-name>.json)")]
[arg("stack", long="stack", short="s", help="Pulumi stack for bucket inference (defaults to PULUMI_STACK)")]
[group('redis')]
[no-cd]
grant-source-bucket-reader bucket="" source_project="" sa_name="redis-restic-reader" key_file="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    SOURCE_BUCKET={{ quote(bucket) }}
    SOURCE_PROJECT={{ quote(source_project) }}
    [ -z "${SOURCE_PROJECT}" ] && SOURCE_PROJECT="${PROJECT_ID:-}"
    SA_NAME={{ quote(sa_name) }}
    KEY_FILE={{ quote(key_file) }}

    if [[ -z "$SOURCE_PROJECT" ]]; then
        echo "Error: --source-project (or PROJECT_ID) is required." >&2
        exit 1
    fi

    if [[ -z "$SOURCE_BUCKET" ]]; then
        STACK=$(just redis _require-stack --stack {{ quote(stack) }}) || exit 1
        STACK_YAML="redis-backup/Pulumi.${STACK}.yaml"
        if [[ -f "$STACK_YAML" ]]; then
            SOURCE_BUCKET=$(yq -r '.config."redis-backup:properties".bucket // ""' "$STACK_YAML" 2>/dev/null || true)
        fi
        if [[ -z "$SOURCE_BUCKET" ]]; then
            SOURCE_BUCKET="restic-redis-backup-${SOURCE_PROJECT}"
        fi
        echo "Using source bucket: $SOURCE_BUCKET"
    fi

    if ! gcloud storage buckets describe "gs://${SOURCE_BUCKET}" \
            --project "${SOURCE_PROJECT}" --format="value(name)" >/dev/null 2>&1; then
        echo "Error: bucket 'gs://${SOURCE_BUCKET}' not found in project '${SOURCE_PROJECT}' (or you lack access to it)." >&2
        exit 1
    fi

    SA_EMAIL="${SA_NAME}@${SOURCE_PROJECT}.iam.gserviceaccount.com"
    KEY_FILE="${KEY_FILE:-credentials/${SOURCE_PROJECT}/${SA_NAME}.json}"

    echo "Source project : ${SOURCE_PROJECT}"
    echo "Source bucket  : gs://${SOURCE_BUCKET}"
    echo "Service account: ${SA_EMAIL}"
    echo "Key file       : ${KEY_FILE}"
    echo

    go run ./cmd/cluster-ops sa setup-reader \
        --source-project "$SOURCE_PROJECT" --name "$SA_NAME" \
        --display-name "Redis restic cross-project reader" \
        --bucket "$SOURCE_BUCKET" --role roles/storage.objectViewer \
        --key-file "$KEY_FILE"
    echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."

    echo
    echo "Next: just redis configure-source-secrets --restic-password '<source pw>' --gcs-project '$SOURCE_PROJECT' --gcs-key-file '$KEY_FILE'"

# Create Secret `redis-backup-source` in the target cluster for cross-project bootstrap.
# Usage: just redis configure-source-secrets --restic-password <pw> --gcs-project <source-id> --gcs-key-file <path> [--key-name <k>] [--secret-name <n>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="SOURCE restic repository password (required)")]
[arg("gcs_project", long="gcs-project", short="g", help="SOURCE GCP project id that owns the source bucket (required)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to the SOURCE service account JSON key (required)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("secret_name", long="secret-name", short="e", help="Name of the source Secret")]
[arg("namespace", long="namespace", short="n", help="Namespace the Secret is created in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('redis')]
[no-cd]
configure-source-secrets restic_password gcs_project gcs_key_file key_name="gcsCredentials" secret_name="redis-backup-source" namespace="prod" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-backup-secrets"
    NS={{ quote(namespace) }}
    RESTIC_PASSWORD={{ quote(restic_password) }}
    GCS_PROJECT={{ quote(gcs_project) }}
    KEY_FILE={{ quote(gcs_key_file) }}
    KEY_NAME={{ quote(key_name) }}
    SECRET_NAME={{ quote(secret_name) }}

    if [[ -z "$RESTIC_PASSWORD" || -z "$GCS_PROJECT" || -z "$KEY_FILE" ]]; then
        echo "Error: --restic-password, --gcs-project and --gcs-key-file are all required." >&2
        exit 1
    fi
    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Error: source service account key '$KEY_FILE' does not exist on this machine." >&2
        exit 1
    fi
    KEY_FILE=$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")

    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: namespace '$NS' does not exist." >&2
        exit 1
    fi

    STACK=$(just redis _require-stack --stack {{ quote(stack) }})

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.sourceSecret.name=$SECRET_NAME" \
        --secret "$FOLDER:properties.sourceSecret.resticPass=$RESTIC_PASSWORD" \
        --secret "$FOLDER:properties.sourceSecret.gcsProject=$GCS_PROJECT" \
        --secret "$FOLDER:properties.sourceSecret.serviceAccount.keyname=$KEY_NAME" \
        --secret "$FOLDER:properties.sourceSecret.serviceAccount.filepath=$KEY_FILE"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    echo
    kubectl get secret "$SECRET_NAME" -n "$NS"
    echo "Reminder: $SECRET_NAME is READ-ONLY bootstrap identity. Never point deploy-backup at it."

# Configure the redis-restore stack for a same-cluster DR restore.
# Usage: just redis configure-restore [--namespace <ns>] [--server <svc>] [--restore-id <id>] [--snapshot <snap>] [--overwrite <yes|no>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace (default prod)")]
[arg("server", long="server", short="v", help="Redis Service name (default redis)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default drill-<UTC timestamp>)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("overwrite", long="overwrite", short="w", help="Flush target Redis before migrating keys: yes|no (default yes)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('redis')]
[no-cd]
configure-restore namespace="prod" server="redis" restore_id="" snapshot="latest" overwrite="yes" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-restore"
    NAMESPACE={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    RESTORE_ID={{ quote(restore_id) }}
    SNAPSHOT={{ quote(snapshot) }}
    OVERWRITE={{ quote(overwrite) }}
    MAX_RESTORE_ID_LEN=49

    case "$OVERWRITE" in
        yes|true) OVERWRITE_VALUE="true" ;;
        no|false) OVERWRITE_VALUE="false" ;;
        *)
            echo "Error: --overwrite takes yes or no; got '$OVERWRITE'." >&2
            exit 1
            ;;
    esac

    STACK=$(just redis _require-stack --stack {{ quote(stack) }})

    if [[ -z "$RESTORE_ID" ]]; then
        RESTORE_ID="drill-$(date -u +%Y%m%d-%H%M%S)"
    fi
    if [[ ! "$RESTORE_ID" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || (( ${#RESTORE_ID} > MAX_RESTORE_ID_LEN )); then
        echo "Error: restore id '$RESTORE_ID' must be a valid DNS-1123 label of at most ${MAX_RESTORE_ID_LEN} chars." >&2
        exit 1
    fi

    CONFIRM_TARGET="${NAMESPACE}/${SERVER}/${RESTORE_ID}"

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "redis-restore:properties.namespace=$NAMESPACE" \
        --plaintext "redis-restore:properties.server=$SERVER" \
        --plaintext "redis-restore:properties.restoreId=$RESTORE_ID" \
        --plaintext "redis-restore:properties.snapshot=$SNAPSHOT" \
        --plaintext "redis-restore:properties.overwrite=$OVERWRITE_VALUE" \
        --plaintext "redis-restore:properties.confirmTarget=$CONFIRM_TARGET"

    echo "redis-restore configured for ${CONFIRM_TARGET} (snapshot: ${SNAPSHOT})."
    echo "Next: just redis apply-restore"

# Run the redis-restore Job configured by configure-restore or configure-bootstrap.
# Usage: just redis apply-restore [--stack <name>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('redis')]
[no-cd]
apply-restore stack="" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-restore"
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    STACK=$(just redis _require-stack --stack {{ quote(stack) }})
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    NS=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.namespace)
    RESTORE_ID=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.restoreId)
    SERVER=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.server)
    SNAPSHOT=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.snapshot)
    JOB="redis-restore-${RESTORE_ID}"

    echo "Restoring Redis into ${NS}/${SERVER} as job ${JOB} from snapshot '${SNAPSHOT}'..."
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    for i in $(seq 1 "$RETRIES"); do
        phase=$(kubectl get pods -n "$NS" -l "restore-id=$RESTORE_ID" \
            -o jsonpath='{.items[0].status.initContainerStatuses[0].state.terminated.reason}' 2>/dev/null || true)
        code=$(kubectl get pods -n "$NS" -l "restore-id=$RESTORE_ID" \
            -o jsonpath='{.items[0].status.initContainerStatuses[0].state.terminated.exitCode}' 2>/dev/null || true)
        if [[ "$phase" == "Completed" ]]; then
            echo "restic-restore completed."
            break
        fi
        if [[ -n "$phase" && "$code" != "0" && -n "$code" ]]; then
            echo "Error: restic-restore terminated with reason '$phase' (exit $code)." >&2
            kubectl logs -n "$NS" "job/$JOB" -c restic-restore --tail=100 || true
            exit 1
        fi
        echo "Waiting for restic-restore (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done

    echo "--- restic-restore log ---"
    kubectl logs -n "$NS" "job/$JOB" -c restic-restore --tail=50 || true

    just redis _wait-job --namespace "$NS" --job "$JOB" \
        --retries "$RETRIES" --interval "$INTERVAL"

    echo "--- redis-restore log ---"
    kubectl logs -n "$NS" "job/$JOB" -c redis-restore --tail=50 || true
    echo "Next: just redis verify"

# Point the redis-restore stack at the SOURCE project's restic repository.
# Usage: just redis configure-bootstrap --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("server", long="server", short="v", help="Redis Service name (default redis)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('redis')]
[no-cd]
configure-bootstrap namespace bucket snapshot="latest" server="redis" restore_id="" secret="redis-backup-source" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-restore"
    NAMESPACE={{ quote(namespace) }}
    BUCKET={{ quote(bucket) }}
    SNAPSHOT={{ quote(snapshot) }}
    SERVER={{ quote(server) }}
    RESTORE_ID={{ quote(restore_id) }}
    SECRET={{ quote(secret) }}
    MAX_RESTORE_ID_LEN=49

    if [[ -z "$NAMESPACE" || -z "$BUCKET" ]]; then
        echo "Error: --namespace and --bucket are required." >&2
        exit 1
    fi
    if [[ -z "$SNAPSHOT" ]]; then
        SNAPSHOT="latest"
    fi

    STACK=$(just redis _require-stack --stack {{ quote(stack) }})

    if [[ -z "$RESTORE_ID" ]]; then
        RESTORE_ID="bootstrap-$(date -u +%Y%m%d-%H%M%S)"
    fi
    if [[ ! "$RESTORE_ID" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || (( ${#RESTORE_ID} > MAX_RESTORE_ID_LEN )); then
        echo "Error: restore id '$RESTORE_ID' must be a valid DNS-1123 label of at most ${MAX_RESTORE_ID_LEN} chars." >&2
        exit 1
    fi

    CONFIRM_TARGET="${NAMESPACE}/${SERVER}/${RESTORE_ID}"

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "redis-restore:properties.namespace=$NAMESPACE" \
        --plaintext "redis-restore:properties.server=$SERVER" \
        --plaintext "redis-restore:properties.restoreId=$RESTORE_ID" \
        --plaintext "redis-restore:properties.snapshot=$SNAPSHOT" \
        --plaintext "redis-restore:properties.confirmTarget=$CONFIRM_TARGET" \
        --plaintext "redis-restore:properties.bucket=$BUCKET" \
        --plaintext "redis-restore:properties.resticSecret.name=$SECRET" \
        --plaintext "redis-restore:properties.resticSecret.key=resticPass" \
        --plaintext "redis-restore:properties.bucketSecret.name=$SECRET" \
        --plaintext "redis-restore:properties.bucketSecret.key=gcsCredentials" \
        --plaintext "redis-restore:properties.projectSecret.name=$SECRET" \
        --plaintext "redis-restore:properties.projectSecret.key=gcsProject" \
        --plaintext "redis-restore:properties.noLock=true" \
        --plaintext "redis-restore:properties.overwrite=true"

    echo "redis-restore bootstrap config written for ${CONFIRM_TARGET} from gs://${BUCKET}."

# Reset the redis-restore stack to this cluster's DR defaults after bootstrap.
# Usage: just redis reset-restore-config [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('redis')]
[no-cd]
reset-restore-config stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="redis-restore"
    STACK=$(just redis _require-stack --stack {{ quote(stack) }})
    PROJECT="${PROJECT_ID:-$STACK}"
    PROD_BUCKET="restic-redis-backup-${PROJECT}"
    PROD_SECRET="redis-backup-auth"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "redis-restore:properties.bucket=$PROD_BUCKET" \
        --plaintext "redis-restore:properties.resticSecret.name=$PROD_SECRET" \
        --plaintext "redis-restore:properties.bucketSecret.name=$PROD_SECRET" \
        --plaintext "redis-restore:properties.projectSecret.name=$PROD_SECRET" \
        --plaintext "redis-restore:properties.noLock=false"

    for key in snapshot restoreId confirmTarget; do
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
    done

    echo "redis-restore reset to DR defaults (bucket: $PROD_BUCKET, secret: $PROD_SECRET)."

# One command for cross-project Redis bootstrap from a source restic bucket:
# configure-bootstrap -> apply-restore -> reset-restore-config via EXIT trap.
# Usage: just redis bootstrap-from-snapshot --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("server", long="server", short="v", help="Redis Service name (default redis)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 90)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('redis')]
[no-cd]
bootstrap-from-snapshot namespace bucket snapshot="latest" server="redis" restore_id="" secret="redis-backup-source" stack="" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    STACK=$(just redis _require-stack --stack {{ quote(stack) }})
    trap 'just redis reset-restore-config --stack "$STACK" || true' EXIT

    just redis configure-bootstrap \
        --namespace {{ quote(namespace) }} \
        --bucket {{ quote(bucket) }} \
        --snapshot {{ quote(snapshot) }} \
        --server {{ quote(server) }} \
        --restore-id {{ quote(restore_id) }} \
        --secret {{ quote(secret) }} \
        --stack "$STACK"

    just redis apply-restore \
        --stack "$STACK" \
        --retries {{ quote(retries) }} \
        --interval {{ quote(interval) }}

    echo "Redis bootstrap restore complete. Next: just redis deploy-backup"
