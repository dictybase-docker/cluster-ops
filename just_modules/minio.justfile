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
    ROOT_USER={{ quote(root_user) }}
    ROOT_PASSWORD={{ quote(root_password) }}

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

# Mirror one bucket from a source MinIO/S3 endpoint into this MinIO, including
# its anonymous bucket policy (mc anonymous get-json / set-json).
# Source credentials are explicit flags; target credentials are read from the
# in-cluster Secret. Re-runnable — mc mirror only copies what changed.
# Usage: just minio import-bucket --bucket <name> --source-url <url> --source-user <user> --source-password <pw> [--namespace <ns>] [--secret <name>] [--remove] [--policy-only]
[arg("bucket", long="bucket", short="b", help="Bucket name (must exist at the source; created here if missing)")]
[arg("source_url", long="source-url", short="s", help="Source S3 endpoint URL, e.g. https://storage.source.example:9000")]
[arg("source_user", long="source-user", short="u", help="Source access key")]
[arg("source_password", long="source-password", short="p", help="Source secret key")]
[arg("namespace", long="namespace", short="n", help="Namespace MinIO runs in")]
[arg("secret", long="secret", short="e", help="Secret holding this MinIO's root credentials")]
[arg("remove", long="remove", help="Pass --remove to mc mirror (deletes target objects missing at source; default OFF)")]
[arg("retries", long="retries", short="r", help="Mirror attempts before giving up (default 5); each attempt resumes where the last stopped")]
[arg("policy_only", long="policy-only", help="Pass 'yes' to copy only the anonymous bucket policy when objects are already mirrored")]
[group('minio')]
[no-cd]
import-bucket bucket source_url source_user source_password namespace="prod" secret="minio-root" remove="no" retries="5" policy_only="no":
    #!/usr/bin/env bash
    set -euo pipefail

    BUCKET={{ quote(bucket) }}
    SRC_URL={{ quote(source_url) }}
    SRC_USER={{ quote(source_user) }}
    SRC_PASSWORD={{ quote(source_password) }}
    NS={{ quote(namespace) }}
    SECRET={{ quote(secret) }}

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

    # Percent-encode for the URL userinfo: strong root passwords carry /, @,
    # :, #, %… which would otherwise corrupt the target alias URL.
    pct() {
        local s="$1" out="" i c
        for ((i = 0; i < ${#s}; i++)); do
            c=${s:i:1}
            case "$c" in
                [A-Za-z0-9._~-]) out+="$c" ;;
                *) printf -v c '%%%02X' "'$c"; out+="$c" ;;
            esac
        done
        printf '%s' "$out"
    }
    USER_ENC=$(pct "$USER_KEY")
    PASS_ENC=$(pct "$PASS_KEY")

    # Reach the cluster-internal API through a forward-held child: the Go
    # parent picks a free local port, exports FORWARD_PORT, and reconnects
    # across pod restarts. FORWARD_PORT exists only inside the child, so the
    # child builds the target alias (MC_HOST_minio_dst — the suffix is the
    # alias name, case-sensitive) from the exported root credentials: env,
    # never argv. Source credentials come from the caller's own flags and
    # land only in a throwaway MC_CONFIG_DIR (removed on exit), never in ~/.mc.
    MC_CONFIG_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mc-config.XXXXXX")"
    export MC_CONFIG_DIR
    trap 'rm -rf "$MC_CONFIG_DIR"' EXIT
    mc alias set minio-src "$SRC_URL" "$SRC_USER" "$SRC_PASSWORD" --quiet
    RETRIES={{ quote(retries) }}
    [[ "$RETRIES" =~ ^[1-9][0-9]*$ ]] || { echo "Error: --retries must be a positive integer." >&2; exit 1; }

    export USER_ENC PASS_ENC BUCKET RETRIES REMOVE_FLAG="" POLICY_ONLY={{ quote(policy_only) }}
    [[ {{ quote(remove) }} == "yes" ]] && REMOVE_FLAG="--remove"
    echo "Ensuring bucket '$BUCKET' exists on the target..."
    go run ./cmd/cluster-ops forward run --ns "$NS" --svc minio --remote-port 9000 -- \
        bash -c '
            export MC_HOST_minio_dst="http://${USER_ENC}:${PASS_ENC}@localhost:${FORWARD_PORT}"
            mc mb --ignore-existing "minio_dst/$BUCKET"

            # HTTPS load balancers (GCP classic LB among them) reset long-lived
            # HTTP/2 streams mid-transfer; mc is a Go client and negotiates
            # HTTP/2 by default. Force HTTP/1.1 for the source reads — the
            # reset storms disappear. Harmless for plain-http endpoints.
            export GODEBUG=http2client=0

            if [[ "$POLICY_ONLY" != "yes" ]]; then
                ok=0
                for attempt in $(seq 1 "$RETRIES"); do
                    # --retry retries failed objects in place (transient resets);
                    # --summary prints copied/failed counts per attempt.
                    if mc mirror --preserve --overwrite --retry --summary $REMOVE_FLAG \
                        "minio-src/$BUCKET" "minio_dst/$BUCKET"; then
                        ok=1
                        break
                    fi
                    echo "Mirror attempt $attempt/$RETRIES failed — retrying (resume skips objects already copied)..." >&2
                    sleep $((attempt * 5))
                done
                [[ "$ok" == "1" ]] || { echo "Error: mirror still failing after $RETRIES attempts." >&2; exit 1; }
            fi

            echo "Syncing anonymous bucket policy from source..."
            policy_file="${MC_CONFIG_DIR}/policy.json"
            mc anonymous get-json "minio-src/$BUCKET" > "$policy_file"
            if jq -e ".Statement | length > 0" "$policy_file" >/dev/null 2>&1; then
                mc anonymous set-json "$policy_file" "minio_dst/$BUCKET"
            else
                mc anonymous set none "minio_dst/$BUCKET"
            fi
            mc anonymous get "minio_dst/$BUCKET"

            if [[ "$POLICY_ONLY" != "yes" ]]; then
                echo
                echo "Done. Object counts:"
                mc ls --recursive "minio_dst/$BUCKET" | wc -l
            fi
        '
    unset USER_ENC PASS_ENC BUCKET REMOVE_FLAG RETRIES POLICY_ONLY

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

    set +e
    out=$(go run ./cmd/cluster-ops preflight minio \
        --namespace {{ quote(namespace) }} --secret {{ quote(secret) }} \
        --pool {{ quote(pool) }} --node-count {{ quote(node_count) }})
    code=$?
    set -e
    printf '%s\n' "$out"
    if [ "$code" -ne 0 ]; then
        echo "ERROR: $code check(s) failed. See reference/minio/troubleshooting.md." >&2
        exit "$code"
    fi
    echo "All checks passed."

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

# ── backup ───────────────────────────────────────────────────────────────────

# Wait for a Kubernetes Job to complete; tolerates the "not yet created"
# window and the TTL cleanup window.
# Usage: just minio _wait-job --namespace <ns> --job <name> [--retries <n>] [--interval <s>]
[private]
[arg("namespace", long="namespace", short="n", help="Namespace the Job runs in")]
[arg("job", long="job", short="j", help="Job name")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[no-cd]
_wait-job namespace job retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    JOB={{ quote(job) }}
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
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
# mint credentials/<project-id>/minio-backup-sa.json.
# Usage: just minio setup-backup-sa [--bucket <name>] [--project <id>] [--sa-name <n>] [--key-file <path>]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[group('minio')]
[no-cd]
setup-backup-sa bucket="" project="" sa_name="minio-backup-sa" key_file="":
    just minio _setup-backup-sa \
        --bucket {{ quote(bucket) }} \
        --project {{ quote(project) }} \
        --sa-name {{ quote(sa_name) }} \
        --key-file {{ quote(key_file) }}

[private]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[no-cd]
_setup-backup-sa bucket="" project="" sa_name="minio-backup-sa" key_file="":
    #!/usr/bin/env bash
    set -euo pipefail

    BUCKET={{ quote(bucket) }}
    PROJECT={{ quote(project) }}
    [ -z "${PROJECT}" ] && PROJECT="${PROJECT_ID:-}"
    SA_NAME={{ quote(sa_name) }}
    KEY_FILE={{ quote(key_file) }}

    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi
    [ -z "${BUCKET}" ] && BUCKET="restic-minio-backup-${PROJECT}"

    SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"
    KEY_FILE="${KEY_FILE:-credentials/${PROJECT}/${SA_NAME}.json}"

    echo "Project        : ${PROJECT}"
    echo "Backup bucket  : gs://${BUCKET}"
    echo "Service account: ${SA_EMAIL}"
    echo "Key file       : ${KEY_FILE}"
    echo

    if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT" >/dev/null 2>&1; then
        echo "Service account already exists — continuing."
    else
        gcloud iam service-accounts create "$SA_NAME" \
            --project "$PROJECT" \
            --display-name "MinIO backup GCS writer"
    fi

    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectAdmin \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=minio-backup-bucket-writer,description=Object admin limited to the MinIO restic backup bucket"

    go run ./cmd/cluster-ops sa create-key \
        --name "$SA_NAME" --project "$PROJECT" --output-file "$KEY_FILE"
    echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."

    echo
    echo "Next: just minio configure-backup-secrets --restic-password '<restic-pass>'"

# Create the `minio-backup-auth` backup Secret in the app namespace.
# Usage: just minio configure-backup-secrets --restic-password <pw> [--no-setup-sa] [--gcs-project <id>] [--gcs-key-file <path>] [--key-name <k>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="restic repository password (required)")]
[arg("setup_sa", long="setup-sa", help="Create/refresh the minio-backup-sa service account + key first")]
[arg("gcs_project", long="gcs-project", short="g", help="GCP project id that owns the backup bucket (defaults to PROJECT_ID)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to the service account JSON key (defaults to credentials/<project-id>/minio-backup-sa.json)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("namespace", long="namespace", short="n", help="Optional guard: must equal the namespace-bootstrap appNamespace export")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
configure-backup-secrets restic_password setup_sa="true" gcs_project="" gcs_key_file="" key_name="gcsCredentials" namespace="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-backup-secrets"
    NS={{ quote(namespace) }}
    RESTIC_PASSWORD={{ quote(restic_password) }}
    SETUP_SA={{ quote(setup_sa) }}
    GCS_PROJECT={{ quote(gcs_project) }}
    KEY_FILE={{ quote(gcs_key_file) }}
    KEY_NAME={{ quote(key_name) }}

    [[ -z "$GCS_PROJECT" ]] && GCS_PROJECT="${PROJECT_ID:-}"
    [[ -z "$KEY_FILE" && -n "$GCS_PROJECT" ]] && KEY_FILE="credentials/${GCS_PROJECT}/minio-backup-sa.json"

    if [[ -z "$RESTIC_PASSWORD" ]]; then
        echo "Error: --restic-password is required." >&2
        exit 1
    fi
    if [[ -z "$GCS_PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --gcs-project." >&2
        exit 1
    fi

    DEFAULT_KEY_FILE="credentials/${GCS_PROJECT}/minio-backup-sa.json"
    if [[ "$SETUP_SA" == "true" && "$KEY_FILE" == "$DEFAULT_KEY_FILE" ]]; then
        just minio _setup-backup-sa --project "$GCS_PROJECT"
    elif [[ "$SETUP_SA" == "true" ]]; then
        echo "Custom --gcs-key-file given — skipping SA setup; verifying the key file exists."
    fi

    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Error: service account key '$KEY_FILE' does not exist on this machine." >&2
        exit 1
    fi
    KEY_FILE=$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})

    BOOT_NS=$(pulumi -C namespace-bootstrap stack output appNamespace --stack "$STACK")
    if [[ -n "$NS" && "$NS" != "$BOOT_NS" ]]; then
        echo "Error: --namespace '$NS' does not match the namespace-bootstrap export '$BOOT_NS'." >&2
        exit 1
    fi
    NS="$BOOT_NS"

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.secret.name=minio-backup-auth" \
        --secret "$FOLDER:properties.secret.resticPass=$RESTIC_PASSWORD" \
        --secret "$FOLDER:properties.secret.gcsProject=$GCS_PROJECT" \
        --secret "$FOLDER:properties.secret.serviceAccount.keyname=$KEY_NAME" \
        --secret "$FOLDER:properties.secret.serviceAccount.filepath=$KEY_FILE"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    echo
    kubectl get secret minio-backup-auth -n "$NS"
    echo "Next: just minio deploy-backup"

# Deploy the MinIO backup bucket, daily CronJob (01:30), and immediate Job.
# Usage: just minio deploy-backup [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Namespace the backup Job runs in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('minio')]
[no-cd]
deploy-backup namespace="prod" stack="" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-backup"
    NS={{ quote(namespace) }}
    JOB="minio-immediate-backup-job"
    CRONJOB="minio-backup-cronjob"
    SOURCE_SECRET="minio-backup-source"

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"

    PROJECT="${PROJECT_ID:-}"
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'gcp:project' --value "$PROJECT"

    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    for key in resticSecret bucketSecret projectSecret; do
        configured=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path "properties.${key}.name" 2>/dev/null || true)
        if [[ "$configured" == "$SOURCE_SECRET" ]]; then
            echo "Error: properties.${key}.name is '$SOURCE_SECRET' on $FOLDER stack '$STACK'." >&2
            echo "That Secret is the read-only cross-project BOOTSTRAP identity and must never drive backups." >&2
            echo "Fix: set it back to 'minio-backup-auth'." >&2
            exit 1
        fi
    done

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just minio _wait-job --namespace "$NS" --job "$JOB" \
        --retries {{ quote(retries) }} --interval {{ quote(interval) }}

    echo
    echo "Backup log tail (restic summary should be at the end):"
    kubectl logs -n "$NS" "job/$JOB" -c restic-backup --tail=30 || \
        echo "Job already removed by its 15-minute TTL — nothing left to tail."

    echo
    kubectl get cronjob -n "$NS" "$CRONJOB"

# ── restore and cross-cluster bootstrap ──────────────────────────────────────

# Print a Secret value from the active (source) cluster environment.
# Defaults to the restic repository password in Secret `minio-backup-auth`.
# Usage: just minio source-secret-value [--namespace <ns>] [--secret <name>] [--key <key>]
[arg("namespace", long="namespace", short="n", help="Namespace containing the Secret (default prod)")]
[arg("secret", long="secret", short="s", help="Secret name (default minio-backup-auth)")]
[arg("key", long="key", short="k", help="Secret data key (default resticPass)")]
[group('minio')]
[no-cd]
source-secret-value namespace="prod" secret="minio-backup-auth" key="resticPass":
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
# objectViewer on the source MinIO restic bucket.
# Usage: just minio grant-source-bucket-reader [--bucket <name>] [--source-project <id>] [--sa-name <n>] [--key-file <path>] [--stack <name>]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (optional; inferred from minio-backup stack config)")]
[arg("source_project", long="source-project", short="p", help="SOURCE GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<source-project>/<sa-name>.json)")]
[arg("stack", long="stack", short="s", help="Pulumi stack for bucket inference (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
grant-source-bucket-reader bucket="" source_project="" sa_name="minio-restic-reader" key_file="" stack="":
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
        STACK=$(just minio _require-stack --stack {{ quote(stack) }}) || exit 1
        STACK_YAML="minio-backup/Pulumi.${STACK}.yaml"
        if [[ -f "$STACK_YAML" ]]; then
            SOURCE_BUCKET=$(yq -r '.config."minio-backup:properties".bucket // ""' "$STACK_YAML" 2>/dev/null || true)
        fi
        if [[ -z "$SOURCE_BUCKET" ]]; then
            SOURCE_BUCKET="restic-minio-backup-${SOURCE_PROJECT}"
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
        --display-name "MinIO restic cross-project reader" \
        --bucket "$SOURCE_BUCKET" --role roles/storage.objectViewer \
        --key-file "$KEY_FILE"
    echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."

    echo
    echo "Next: just minio configure-source-secrets --restic-password '<source pw>' --gcs-project '$SOURCE_PROJECT' --gcs-key-file '$KEY_FILE'"

# Create Secret `minio-backup-source` in the target cluster for cross-project bootstrap.
# Usage: just minio configure-source-secrets --restic-password <pw> --gcs-project <source-id> --gcs-key-file <path> [--key-name <k>] [--secret-name <n>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="SOURCE restic repository password (required)")]
[arg("gcs_project", long="gcs-project", short="g", help="SOURCE GCP project id that owns the source bucket (required)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to the SOURCE service account JSON key (required)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("secret_name", long="secret-name", short="e", help="Name of the source Secret")]
[arg("namespace", long="namespace", short="n", help="Namespace the Secret is created in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
configure-source-secrets restic_password gcs_project gcs_key_file key_name="gcsCredentials" secret_name="minio-backup-source" namespace="prod" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-backup-secrets"
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

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})

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

# Configure the minio-restore stack for a same-cluster DR restore.
# Usage: just minio configure-restore [--namespace <ns>] [--server <svc>] [--restore-id <id>] [--snapshot <snap>] [--s3-bucket <name>] [--overwrite <yes|no>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace (default prod)")]
[arg("server", long="server", short="v", help="MinIO Service name (default minio)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default drill-<UTC timestamp>)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("s3_bucket", long="s3-bucket", short="b", help="Optional single MinIO bucket to restore (default: all buckets)")]
[arg("overwrite", long="overwrite", short="w", help="Overwrite existing objects in target MinIO: yes|no (default yes)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
configure-restore namespace="prod" server="minio" restore_id="" snapshot="latest" s3_bucket="" overwrite="yes" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-restore"
    NAMESPACE={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    RESTORE_ID={{ quote(restore_id) }}
    SNAPSHOT={{ quote(snapshot) }}
    S3_BUCKET={{ quote(s3_bucket) }}
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

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})

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

    SET_ARGS=(
        --path
        --plaintext "minio-restore:properties.namespace=$NAMESPACE"
        --plaintext "minio-restore:properties.server=$SERVER"
        --plaintext "minio-restore:properties.restoreId=$RESTORE_ID"
        --plaintext "minio-restore:properties.snapshot=$SNAPSHOT"
        --plaintext "minio-restore:properties.overwrite=$OVERWRITE_VALUE"
        --plaintext "minio-restore:properties.confirmTarget=$CONFIRM_TARGET"
    )
    if [[ -n "$S3_BUCKET" ]]; then
        SET_ARGS+=(--plaintext "minio-restore:properties.s3Bucket=$S3_BUCKET")
    fi
    pulumi -C "$FOLDER" config set-all --stack "$STACK" "${SET_ARGS[@]}"

    if [[ -z "$S3_BUCKET" ]]; then
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path properties.s3Bucket 2>/dev/null || true
    fi

    echo "minio-restore configured for ${CONFIRM_TARGET} (snapshot: ${SNAPSHOT})."
    echo "Next: just minio apply-restore"

# Run the minio-restore Job configured by configure-restore or configure-bootstrap.
# Usage: just minio apply-restore [--stack <name>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('minio')]
[no-cd]
apply-restore stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-restore"
    RETRIES={{ quote(retries) }}
    INTERVAL={{ quote(interval) }}
    STACK=$(just minio _require-stack --stack {{ quote(stack) }})
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    NS=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.namespace)
    RESTORE_ID=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.restoreId)
    SERVER=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.server)
    SNAPSHOT=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.snapshot)
    JOB="minio-restore-${RESTORE_ID}"

    echo "Restoring MinIO into ${NS}/${SERVER} as job ${JOB} from snapshot '${SNAPSHOT}'..."
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

    just minio _wait-job --namespace "$NS" --job "$JOB" \
        --retries "$RETRIES" --interval "$INTERVAL"

    echo "--- mc-restore log ---"
    kubectl logs -n "$NS" "job/$JOB" -c mc-restore --tail=50 || true
    echo "Next: just minio verify"

# Point the minio-restore stack at the SOURCE project's restic repository.
# Usage: just minio configure-bootstrap --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--s3-bucket <name>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("s3_bucket", long="s3-bucket", short="m", help="Optional single MinIO bucket to restore (default: all buckets)")]
[arg("server", long="server", short="v", help="MinIO Service name (default minio)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
configure-bootstrap namespace bucket snapshot="latest" s3_bucket="" server="minio" restore_id="" secret="minio-backup-source" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-restore"
    NAMESPACE={{ quote(namespace) }}
    BUCKET={{ quote(bucket) }}
    SNAPSHOT={{ quote(snapshot) }}
    S3_BUCKET={{ quote(s3_bucket) }}
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

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})

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

    SET_ARGS=(
        --path
        --plaintext "minio-restore:properties.namespace=$NAMESPACE"
        --plaintext "minio-restore:properties.server=$SERVER"
        --plaintext "minio-restore:properties.restoreId=$RESTORE_ID"
        --plaintext "minio-restore:properties.snapshot=$SNAPSHOT"
        --plaintext "minio-restore:properties.confirmTarget=$CONFIRM_TARGET"
        --plaintext "minio-restore:properties.bucket=$BUCKET"
        --plaintext "minio-restore:properties.resticSecret.name=$SECRET"
        --plaintext "minio-restore:properties.resticSecret.key=resticPass"
        --plaintext "minio-restore:properties.bucketSecret.name=$SECRET"
        --plaintext "minio-restore:properties.bucketSecret.key=gcsCredentials"
        --plaintext "minio-restore:properties.projectSecret.name=$SECRET"
        --plaintext "minio-restore:properties.projectSecret.key=gcsProject"
        --plaintext "minio-restore:properties.noLock=true"
        --plaintext "minio-restore:properties.overwrite=true"
    )
    if [[ -n "$S3_BUCKET" ]]; then
        SET_ARGS+=(--plaintext "minio-restore:properties.s3Bucket=$S3_BUCKET")
    fi
    pulumi -C "$FOLDER" config set-all --stack "$STACK" "${SET_ARGS[@]}"

    if [[ -z "$S3_BUCKET" ]]; then
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path properties.s3Bucket 2>/dev/null || true
    fi

    echo "minio-restore bootstrap config written for ${CONFIRM_TARGET} from gs://${BUCKET}."

# Reset the minio-restore stack to this cluster's DR defaults after bootstrap.
# Usage: just minio reset-restore-config [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('minio')]
[no-cd]
reset-restore-config stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="minio-restore"
    STACK=$(just minio _require-stack --stack {{ quote(stack) }})
    PROJECT="${PROJECT_ID:-$STACK}"
    PROD_BUCKET="restic-minio-backup-${PROJECT}"
    PROD_SECRET="minio-backup-auth"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "minio-restore:properties.bucket=$PROD_BUCKET" \
        --plaintext "minio-restore:properties.resticSecret.name=$PROD_SECRET" \
        --plaintext "minio-restore:properties.bucketSecret.name=$PROD_SECRET" \
        --plaintext "minio-restore:properties.projectSecret.name=$PROD_SECRET" \
        --plaintext "minio-restore:properties.noLock=false"

    for key in snapshot restoreId confirmTarget s3Bucket; do
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
    done

    echo "minio-restore reset to DR defaults (bucket: $PROD_BUCKET, secret: $PROD_SECRET)."

# One command for cross-project MinIO bootstrap from a source restic bucket:
# configure-bootstrap -> apply-restore -> reset-restore-config via EXIT trap.
# Usage: just minio bootstrap-from-snapshot --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--s3-bucket <name>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id or 'latest' (default latest)")]
[arg("s3_bucket", long="s3-bucket", short="m", help="Optional single MinIO bucket to restore (default: all buckets)")]
[arg("server", long="server", short="v", help="MinIO Service name (default minio)")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('minio')]
[no-cd]
bootstrap-from-snapshot namespace bucket snapshot="latest" s3_bucket="" server="minio" restore_id="" secret="minio-backup-source" stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    STACK=$(just minio _require-stack --stack {{ quote(stack) }})
    trap 'just minio reset-restore-config --stack "$STACK" || true' EXIT

    just minio configure-bootstrap \
        --namespace {{ quote(namespace) }} \
        --bucket {{ quote(bucket) }} \
        --snapshot {{ quote(snapshot) }} \
        --s3-bucket {{ quote(s3_bucket) }} \
        --server {{ quote(server) }} \
        --restore-id {{ quote(restore_id) }} \
        --secret {{ quote(secret) }} \
        --stack "$STACK"

    just minio apply-restore \
        --stack "$STACK" \
        --retries {{ quote(retries) }} \
        --interval {{ quote(interval) }}

    echo "MinIO bootstrap restore complete. Next: just minio deploy-backup"
