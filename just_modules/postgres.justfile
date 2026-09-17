# Recipes for PostgreSQL on CloudNativePG. Guide: docs/postgres-deploy.md.
# Everything assumes the cluster-env sub-shell (just cluster-env) so
# PULUMI_STACK, PULUMI_BACKEND_URL, PULUMI_GCP_CREDENTIALS, PROJECT_ID and
# KUBECONFIG are set. No recipe falls back to a dev stack.

# ── private helpers ──────────────────────────────────────────────────────────

# Resolve the Pulumi stack name, or fail. Never falls back to "dev" — every
# production recipe routes through this so a missing cluster env cannot
# silently target the lab stack.
# Usage: STACK=$(just postgres _require-stack [--stack <name>])
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

# Resolve the GCP project id, or fail.
# Usage: PROJECT=$(just postgres _require-project [--project <id>])
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID)")]
[no-cd]
_require-project project="":
    #!/usr/bin/env bash
    set -euo pipefail
    PROJECT="{{ project }}"
    if [[ -z "$PROJECT" ]]; then
        PROJECT="${PROJECT_ID:-}"
    fi
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi
    echo "$PROJECT"

# Wait until at least <count> pods matching a selector are Running with every
# container ready, or time out.
# Usage: just postgres _wait-ready --namespace <ns> --selector <sel> [--count <n>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace to watch")]
[arg("selector", long="selector", short="l", help="Label selector for the pods")]
[arg("count", long="count", short="c", help="How many ready pods to wait for")]
[arg("retries", long="retries", short="r", help="Probe attempts before failing")]
[arg("interval", long="interval", short="i", help="Seconds between probes")]
[no-cd]
_wait-ready namespace selector count="1" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    SEL="{{ selector }}"
    WANT="{{ count }}"
    RETRIES="{{ retries }}"
    INTERVAL="{{ interval }}"

    for i in $(seq 1 "$RETRIES"); do
        set +e
        out=$(kubectl get pods -n "$NS" -l "$SEL" -o json 2>&1)
        status=$?
        set -e
        if [[ $status -ne 0 ]]; then
            echo "$out" >&2
            echo "Error: kubectl get pods -n $NS -l $SEL failed." >&2
            exit 1
        fi
        ready=$(printf '%s\n' "$out" | jq '[.items[]
            | select(.status.phase == "Running")
            | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)]
            | length')
        if [[ "$ready" -ge "$WANT" ]]; then
            echo "$ready/$WANT pod(s) ready in $NS ($SEL)."
            exit 0
        fi
        echo "Waiting for pods in $NS ($SEL): $ready/$WANT ready (try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done

    echo "Error: only $ready/$WANT pod(s) ready in $NS ($SEL) after $((RETRIES * INTERVAL))s." >&2
    kubectl get pods -n "$NS" -l "$SEL" -o wide || true
    exit 1

# ── public recipes ───────────────────────────────────────────────────────────

# Verify the stateful-db pool: node count, taint, Ready, zone spread.
# Usage: just postgres check-pool [--pool <label>] [--node-count <n>]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('postgres')]
[no-cd]
check-pool pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    POOL="{{ pool }}"
    WANT_NODES="{{ node_count }}"
    failures=0

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"

    echo "Checking stateful-db pool prerequisites..."
    echo

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

# Create the backup service account + key, grant it the backup bucket, store
# the backup-secret config on the cluster stack, and deploy the Barman Cloud
# CNPG-I plugin (composite tail). Creates no namespaces — the
# namespace-bootstrap stack owns them (just gcp-pulumi apply-namespaces).
# One command for: SA create (idempotent), IAM condition, key mint (skips if
# the file exists), ensure-stack, config set, plugin deploy.
# Usage: just postgres configure-backup [--bucket <name>] [--project <id>] [--stack <name>]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket name (default cloudnative-pg-backup-<project-id>; created later by the cluster stack)")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID from the cluster env)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('postgres')]
[no-cd]
configure-backup bucket="" project="" sa_name="postgres-backup-sa" key_file="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cloudnative-pg-cluster"
    PROJECT=$(just postgres _require-project --project "{{ project }}")
    BUCKET="{{ bucket }}"
    BUCKET="${BUCKET:-cloudnative-pg-backup-${PROJECT}}"
    SA_NAME="{{ sa_name }}"
    KEY_FILE="{{ key_file }}"
    # Relative paths break at `pulumi -C` time: pulumi changes the working
    # directory to the stack folder, so os.ReadFile of a repo-relative path
    # fails with ENOENT. Default to an absolute path from the repo root.
    KEY_FILE="${KEY_FILE:-{{ justfile_directory() }}/credentials/${PROJECT}/${SA_NAME}.json}"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    SA_EMAIL="${SA_NAME}@${PROJECT}.iam.gserviceaccount.com"

    echo "Project        : ${PROJECT}"
    echo "Backup bucket  : gs://${BUCKET}"
    echo "Service account: ${SA_EMAIL}"
    echo "Key file       : ${KEY_FILE}"
    echo

    # Idempotent: create only when missing. Describe-probe mirrors
    # cluster.justfile / sa.justfile; `create || echo` would spew a red
    # ERROR on every re-run of an existing SA.
    if gcloud iam service-accounts describe "$SA_EMAIL" --project "$PROJECT" >/dev/null 2>&1; then
        echo "Service account already exists — continuing."
    else
        gcloud iam service-accounts create "$SA_NAME" \
            --project "$PROJECT" \
            --display-name "CloudNativePG backup GCS writer"
    fi

    # Least privilege: project-level bindings with an IAM condition pinning the
    # grants to the backup bucket. A plain bucket-level binding is impossible
    # here — the cluster stack creates the bucket AFTER this key must exist.
    # Additive and idempotent on re-run.
    #
    # Two roles are required, not one: `objectAdmin` covers object writes but
    # NOT bucket-metadata reads. barman-cloud's destination check does a
    # GET on the bucket (storage.buckets.get), so without `bucketViewer` WAL
    # archiving and backups fail with 403 (verified on dcr-kube1 2026-09-16).
    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectAdmin \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=postgres-backup-bucket-writer,description=Object admin limited to the CloudNativePG backup bucket"
    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.bucketViewer \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=postgres-backup-bucket-reader,description=Bucket metadata reader for the CloudNativePG backup bucket"

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

    # Namespaces are owned by the namespace-bootstrap stack — applied once
    # during cluster setup (just gcp-pulumi apply-namespaces), never here.

    # Store the backup wiring on the cluster stack. The bucket name and key
    # path are project-derived, so they are set here, not hardcoded in
    # Pulumi.<cluster>.yaml.
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.clusters[0].cluster.backup.bucket' --value "$BUCKET"
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.backupSecret.filepath' --value "$KEY_FILE" \
        --plaintext yes

    # Composite mode: also deploy the Barman Cloud CNPG-I plugin — the
    # cluster stack probes its stack and archives WAL through it, so the
    # plugin must exist before deploy-cluster. Idempotent.
    just postgres deploy-backup-plugin --stack "$STACK"

    echo
    echo "Next: just postgres deploy-cluster --app-password '<app-password>'"
    echo "      (importing another cluster's data? run 'just postgres configure-source' first)"

# Wire a source cluster's CloudNativePG backup for import: creates/reuses a
# reader service account in the SOURCE project, grants it read on the source
# bucket, mints/reuses its key, and stores the recovery source on the cluster
# stack. Run AFTER configure-backup, BEFORE deploy-cluster.
# Usage: just postgres configure-source --source-cluster <cluster-name>
#   The source identity is standardized: given the source cluster's name (the
#   `.env.<env>.<name>` suffix, e.g. dcr-experiments), the recipe probes that
#   env file for the source PROJECT_ID, falls back to the backup bucket name in
#   cloudnative-pg-cluster/Pulumi.<name>.yaml (prod stacks share the cluster
#   name, lab stacks use the env name — both probed), and defaults the CNPG
#   cluster name and bucket path to logto — the values every stack in this
#   repo uses. Source-side IAM (reader SA, bucket grant, key mint) runs with
#   the source env file's GOOGLE_APPLICATION_CREDENTIALS when probed; else
#   the current identity must hold SA-admin + bucket-admin in the source
#   project. A source not managed from this checkout needs --source-project
#   <id> or --source-env-file <path>. Every inferred value has an override.
[arg("source_cluster", long="source-cluster", short="c", help="Source cluster name — the .env.<env>.<name> suffix; probed for the source PROJECT_ID and stack config")]
[arg("source_cnpg_cluster", long="source-cnpg-cluster", help="CNPG Cluster name in the source project = backup folder name (default logto; override only if the source Cluster CR is not named logto)")]
[arg("bucket", long="bucket", short="b", help="Source GCS bucket holding the barman backups (default from the source stack config, else cloudnative-pg-backup-<source-project>)")]
[arg("source_project", long="source-project", short="g", help="SOURCE GCP project id owning the source bucket (default from --source-cluster / --source-env-file probing)")]
[arg("bucket_path", long="bucket-path", short="p", help="Path inside the bucket (source cluster's backup.bucketPath; default logto)")]
[arg("target_time", long="target-time", short="t", help="Optional RFC3339 PITR timestamp; default = latest available")]
[arg("sa_name", long="sa-name", short="a", help="Reader service account short name to create/reuse in the source project")]
[arg("key_file", long="key-file", short="f", help="Where to write the reader key (default credentials/<source-project>/<sa-name>.json)")]
[arg("source_env_file", long="source-env-file", short="e", help="Env file with the source PROJECT_ID (and manager credential for source-side IAM); default: probe .env.*.<source-cluster>")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('postgres')]
[no-cd]
configure-source source_cluster="" source_cnpg_cluster="" bucket="" source_project="" bucket_path="logto" target_time="" sa_name="postgres-source-reader" key_file="" source_env_file="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cloudnative-pg-cluster"
    REPO="{{ justfile_directory() }}"
    SRC_CLUSTER="{{ source_cluster }}"
    SRC_CNPG_CLUSTER="{{ source_cnpg_cluster }}"
    BUCKET="{{ bucket }}"
    SRC_PROJECT="{{ source_project }}"
    BUCKET_PATH="{{ bucket_path }}"
    TARGET_TIME="{{ target_time }}"
    SA_NAME="{{ sa_name }}"
    KEY_FILE="{{ key_file }}"
    ENV_FILE="{{ source_env_file }}"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")
    NOTES=""

    # --- Source GCP project -------------------------------------------------
    # Precedence: --source-project > --source-env-file > probe
    # .env.*.<source-cluster> > bucket-name suffix in the source stack config.
    # Probing works only when the source cluster is managed from this
    # checkout; a cross-project source usually needs --source-project.
    if [[ -z "$SRC_PROJECT" && -n "$ENV_FILE" ]]; then
        if [[ ! -f "$ENV_FILE" ]]; then
            echo "Error: --source-env-file '$ENV_FILE' not found." >&2
            exit 1
        fi
        SRC_PROJECT=$(sed -n 's/^PROJECT_ID=//p' "$ENV_FILE" | tail -1)
        NOTES="project from env file ${ENV_FILE#$REPO/}"
    fi
    if [[ -z "$SRC_PROJECT" && -n "$SRC_CLUSTER" ]]; then
        ENV_HIT=$( (ls "$REPO"/.env.*."$SRC_CLUSTER" 2>/dev/null || true) | head -1)
        if [[ -n "$ENV_HIT" ]]; then
            SRC_PROJECT=$(sed -n 's/^PROJECT_ID=//p' "$ENV_HIT" | tail -1)
            NOTES="project from env file ${ENV_HIT#$REPO/}"
        fi
    fi
    # Source stack config candidates: prod stacks are named after the cluster
    # (Pulumi.dcr-kube1.yaml), lab stacks after the env (cluster dcr-experiments
    # -> Pulumi.experiments.yaml) — try the exact name, then the dcr- prefix
    # stripped.
    CFG_FILE=""
    for cand in "$SRC_CLUSTER" "${SRC_CLUSTER#dcr-}"; do
        if [[ -n "$cand" && -f "$REPO/$FOLDER/Pulumi.$cand.yaml" ]]; then
            CFG_FILE="$REPO/$FOLDER/Pulumi.$cand.yaml"
            CFG_NAME="Pulumi.$cand.yaml"
            break
        fi
    done
    if [[ -z "$SRC_PROJECT" && -n "$CFG_FILE" ]]; then
        CFG_BUCKET=$(sed -n 's/^[[:space:]]*bucket:[[:space:]]*//p' "$CFG_FILE" | head -1)
        CFG_BUCKET="${CFG_BUCKET#gs://}"
        if [[ "$CFG_BUCKET" == cloudnative-pg-backup-* ]]; then
            SRC_PROJECT="${CFG_BUCKET#cloudnative-pg-backup-}"
            NOTES="project from bucket name in $CFG_NAME"
        fi
    fi
    # Strip optional quotes around the value (PROJECT_ID="x").
    SRC_PROJECT="${SRC_PROJECT%\"}"; SRC_PROJECT="${SRC_PROJECT#\"}"
    SRC_PROJECT="${SRC_PROJECT%\'}"; SRC_PROJECT="${SRC_PROJECT#\'}"
    if [[ -z "$SRC_PROJECT" ]]; then
        echo "Error: cannot resolve the SOURCE GCP project id." >&2
        echo "  Pass --source-project <id>, or --source-cluster/--source-env-file" >&2
        echo "  naming an env file that contains PROJECT_ID=<id>." >&2
        exit 1
    fi

    # --- Source-side identity ------------------------------------------------
    # SA creation, bucket IAM, and key minting run against the SOURCE project.
    # When the source cluster's env file is available (probed or explicit),
    # scope those commands to its GOOGLE_APPLICATION_CREDENTIALS — the source
    # cluster's own manager identity. Otherwise the current identity must
    # already hold SA-admin + bucket-admin there; GCP names the missing role
    # if it does not.
    ENV_FILE="${ENV_FILE:-${ENV_HIT:-}}"
    SOURCE_CRED=""
    if [[ -n "$ENV_FILE" && -f "$ENV_FILE" ]]; then
        SOURCE_CRED=$(sed -n 's/^GOOGLE_APPLICATION_CREDENTIALS=//p' "$ENV_FILE" | tail -1)
        SOURCE_CRED="${SOURCE_CRED%\"}"; SOURCE_CRED="${SOURCE_CRED#\"}"
        SOURCE_CRED="${SOURCE_CRED%\'}"; SOURCE_CRED="${SOURCE_CRED#\'}"
        SOURCE_CRED="${SOURCE_CRED//\$\{PWD\}/$REPO}"
        SOURCE_CRED="${SOURCE_CRED//\$PWD/$REPO}"
    fi
    _SRC_NOTE_PRINTED=""
    src_gcloud() {
        if [[ -n "$SOURCE_CRED" && -f "$SOURCE_CRED" ]]; then
            CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE="$SOURCE_CRED" \
                GOOGLE_APPLICATION_CREDENTIALS="$SOURCE_CRED" "$@"
        else
            if [[ -z "$_SRC_NOTE_PRINTED" ]]; then
                echo "Note: no source env credential — running source-side IAM with the" >&2
                echo "current identity; it must hold SA-admin + bucket-admin in $SRC_PROJECT." >&2
                _SRC_NOTE_PRINTED=1
            fi
            "$@"
        fi
    }
    [[ -n "$SOURCE_CRED" && -f "$SOURCE_CRED" ]] \
        && NOTES="$NOTES; source IAM via ${SOURCE_CRED#$REPO/}"

    # --- Source bucket --------------------------------------------------------
    # Precedence: --bucket > source stack config (backup.bucket) >
    # cloudnative-pg-backup-<source-project> — the name every cluster stack
    # in this repo creates for its own backups.
    if [[ -z "$BUCKET" && -n "$CFG_FILE" ]]; then
        BUCKET=$(sed -n 's/^[[:space:]]*bucket:[[:space:]]*//p' "$CFG_FILE" | head -1)
        BUCKET="${BUCKET#gs://}"
        [[ -n "$BUCKET" ]] && NOTES="$NOTES; bucket from $CFG_NAME"
    fi
    if [[ -z "$BUCKET" ]]; then
        BUCKET="cloudnative-pg-backup-${SRC_PROJECT}"
    fi

    # --- CNPG cluster name = backup folder name -------------------------------
    # Every stack in this repo names its Cluster CR logto, so the default is
    # right unless the source overrode the cluster name at deploy time.
    if [[ -z "$SRC_CNPG_CLUSTER" ]]; then
        SRC_CNPG_CLUSTER="logto"
    fi

    # Absolute default — `pulumi -C` changes the working directory, so a
    # repo-relative path would not resolve when the stack reads it.
    KEY_FILE="${KEY_FILE:-$REPO/credentials/${SRC_PROJECT}/${SA_NAME}.json}"

    SA_EMAIL="${SA_NAME}@${SRC_PROJECT}.iam.gserviceaccount.com"

    echo "Source project : ${SRC_PROJECT}"
    echo "Source cluster : ${SRC_CLUSTER}"
    echo "CNPG Cluster   : ${SRC_CNPG_CLUSTER}   (backup folder)"
    echo "Source bucket  : gs://${BUCKET}/${BUCKET_PATH}"
    echo "Reader SA      : ${SA_EMAIL}"
    echo "Key file       : ${KEY_FILE}"
    [[ -n "$TARGET_TIME" ]] && echo "PITR target    : ${TARGET_TIME}"
    [[ -n "$NOTES" ]] && echo "Inferred       : ${NOTES}"
    echo

    # Idempotent: "already exists" is the expected result on a re-run.
    src_gcloud gcloud iam service-accounts create "$SA_NAME" \
        --project "$SRC_PROJECT" \
        --display-name "CloudNativePG cross-project backup reader" \
        || echo "Service account already exists — continuing."

    # Read-only, pinned to the source bucket. The bucket already exists here
    # (the source cluster's backups live in it), so a bucket-level binding
    # works — no project-level condition needed.
    src_gcloud gcloud storage buckets add-iam-policy-binding "gs://${BUCKET}" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectViewer \
        --project "$SRC_PROJECT"

    # Key creation is NOT idempotent — reuse an existing key file.
    if [[ -f "$KEY_FILE" ]]; then
        echo "Key file '$KEY_FILE' already exists — NOT creating another key."
    else
        mkdir -p "$(dirname "$KEY_FILE")"
        src_gcloud gcloud iam service-accounts keys create "$KEY_FILE" \
            --iam-account "$SA_EMAIL" \
            --project "$SRC_PROJECT"
        chmod 600 "$KEY_FILE"
        echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."
    fi

    # Store the recovery source on the cluster stack. deploy-cluster
    # re-applies with this set and the bootstrap becomes a recovery.
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.clusters[0].cluster.bootstrap.recovery.sourceCluster' --value "$SRC_CNPG_CLUSTER"
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.clusters[0].cluster.bootstrap.recovery.bucket' --value "$BUCKET"
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.clusters[0].cluster.bootstrap.recovery.bucketPath' --value "$BUCKET_PATH"
    if [[ -n "$TARGET_TIME" ]]; then
        just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
            --key 'properties.clusters[0].cluster.bootstrap.recovery.targetTime' --value "$TARGET_TIME"
    else
        # Omitting --target-time means "latest available". Drop any stale
        # PITR timestamp from a previous run — a leftover would silently PITR.
        GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}" \
            pulumi -C "$FOLDER" -s "$STACK" config rm --path \
            'properties.clusters[0].cluster.bootstrap.recovery.targetTime' >/dev/null 2>&1 || true
    fi
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.sourceSecret.name' --value 'postgres-source-credentials' \
        --plaintext yes
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.sourceSecret.key' --value 'gcsCredentials' \
        --plaintext yes
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.sourceSecret.filepath' --value "$KEY_FILE" \
        --plaintext yes

    echo
    echo "Next: just postgres deploy-cluster --app-password '<app-password>'"
    echo
    echo "Sanity check the source backup is readable with the new key:"
    echo "  GOOGLE_APPLICATION_CREDENTIALS=$KEY_FILE gcloud storage ls gs://$BUCKET/$BUCKET_PATH/$SRC_CNPG_CLUSTER/"

# Reset a RUNNING cluster's data and re-import from the configured recovery
# source. Deletes the Cluster CR and its data PVCs only — operator, backup
# bucket and its backups, Secrets, and ScheduledBackup all stay — refreshes
# Pulumi state so the next apply recreates the Cluster CR, whose first
# instance bootstraps with bootstrap.recovery (replays the source base
# backup + WAL instead of initdb). The surgical alternative to
# teardown+redeploy: existing backups survive.
# Run AFTER configure-source — the recovery source must already be on the
# stack, and this recipe aborts if it is not (a reset without it would
# bootstrap EMPTY, not re-import). Requires --reset-data yes so the data
# loss is never silent.
# Usage: just postgres reset-cluster --reset-data yes [--cluster <name>] [--namespace <ns>] [--app-password '<pw>'] [--stack <name>]
[arg("reset_data", long="reset-data", help="Must be 'yes' — deletes the Cluster CR and every data PVC in it")]
[arg("cluster", long="cluster", short="c", help="Cluster name — must match properties.clusters[0].cluster.name (default logto)")]
[arg("namespace", long="namespace", short="n", help="Namespace holding the Cluster")]
[arg("app_password", long="app-password", short="p", help="Chain deploy-cluster with this app password after the reset (otherwise run deploy-cluster by hand)")]
[arg("retries", long="retries", short="r", help="Instance pod removal probe attempts (default 30)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('postgres')]
[no-cd]
reset-cluster reset_data="no" cluster="logto" namespace="prod" app_password="" retries="30" interval="10" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cloudnative-pg-cluster"
    NS="{{ namespace }}"
    CLUSTER="{{ cluster }}"
    APP_PASSWORD="{{ app_password }}"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    if [[ "{{ reset_data }}" != "yes" ]]; then
        echo "Refusing reset." >&2
        echo "This deletes the Cluster CR '$CLUSTER' in '$NS' and every data PVC:" >&2
        echo "  - all current database files are destroyed" >&2
        echo "  - the next deploy-cluster re-imports from the recovery source" >&2
        echo "Re-run with --reset-data yes to proceed." >&2
        exit 1
    fi

    # The recovery source must already be on the stack — a reset without it
    # would silently bootstrap EMPTY, not re-import.
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    SRC_CLUSTER=$(pulumi -C "$FOLDER" -s "$STACK" config get --path \
        'properties.clusters[0].cluster.bootstrap.recovery.sourceCluster' 2>/dev/null) || {
        echo "Error: no recovery source on stack '$STACK' — bootstrap would be EMPTY." >&2
        echo "Run 'just postgres configure-source --source-cluster <name>' first." >&2
        exit 1
    }
    SRC_BUCKET=$(pulumi -C "$FOLDER" -s "$STACK" config get --path \
        'properties.clusters[0].cluster.bootstrap.recovery.bucket' 2>/dev/null || true)
    SRC_BUCKET_PATH=$(pulumi -C "$FOLDER" -s "$STACK" config get --path \
        'properties.clusters[0].cluster.bootstrap.recovery.bucketPath' 2>/dev/null || true)
    TARGET_TIME=$(pulumi -C "$FOLDER" -s "$STACK" config get --path \
        'properties.clusters[0].cluster.bootstrap.recovery.targetTime' 2>/dev/null || true)

    if ! kubectl get cluster "$CLUSTER" -n "$NS" >/dev/null 2>&1; then
        echo "Error: Cluster CR '$CLUSTER' not found in '$NS' — nothing to reset." >&2
        echo "For a fresh deploy use 'just postgres deploy-cluster' instead." >&2
        exit 1
    fi

    echo "Resetting cluster '$CLUSTER' in '$NS' (stack '$STACK')..."
    echo "  Destroyed : all current database files (Cluster CR + data PVCs)"
    echo "  Kept      : operator, backup bucket + backups, Secrets, ScheduledBackup"
    echo "  Re-import : gs://${SRC_BUCKET:-<unset>}/${SRC_BUCKET_PATH}/$SRC_CLUSTER/"
    [[ -n "$TARGET_TIME" ]] && echo "  PITR      : $TARGET_TIME"
    echo

    echo "Deleting Cluster CR '$CLUSTER' (operator removes the instance pods)..."
    if ! kubectl delete cluster "$CLUSTER" -n "$NS" --wait --timeout=300s; then
        echo "Error: Cluster deletion timed out — a finalizer may be stuck." >&2
        echo "Inspect: kubectl get cluster $CLUSTER -n $NS -o jsonpath='{.metadata.finalizers}'" >&2
        exit 1
    fi

    # Belt-and-braces: no instance pods must remain before touching PVCs.
    pods_gone() {
        [[ -z "$(kubectl get pods -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" -o name 2>/dev/null)" ]]
    }
    for ((i = 1; i <= {{ retries }}; i++)); do
        pods_gone && break
        sleep "{{ interval }}"
    done
    if ! pods_gone; then
        echo "Error: instance pods still present after {{ retries }} probes — PVCs left in place." >&2
        kubectl get pods -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" >&2 || true
        exit 1
    fi

    echo "Deleting data PVCs..."
    kubectl delete pvc -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" --ignore-not-found

    echo "Refreshing Pulumi state so the next apply recreates the Cluster CR..."
    pulumi -C "$FOLDER" -s "$STACK" refresh --yes

    if [[ -n "$APP_PASSWORD" ]]; then
        just postgres deploy-cluster --app-password "$APP_PASSWORD" \
            --cluster "$CLUSTER" --namespace "$NS" --stack "$STACK"
    else
        echo
        echo "Reset complete. Re-import with:"
        echo "  just postgres deploy-cluster --app-password '<app-password>' --cluster $CLUSTER --namespace $NS"
    fi

# Deploy the CloudNativePG operator stack, then wait until it is serving.
# One command for: ensure-stack, preview, apply, operator pod ready, CRD present.
# Usage: just postgres deploy-operator [--stack <name>] [--namespace <ns>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("namespace", long="namespace", short="n", help="Namespace the operator is installed into")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('postgres')]
[no-cd]
deploy-operator stack="" namespace="operators" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cloudnative-pg-operator"
    NS="{{ namespace }}"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    # The program takes the release namespace from the namespace-bootstrap
    # stack's operatorsNamespace export — refuse to wait on anything else.
    BOOT_NS=$(pulumi -C namespace-bootstrap stack output operatorsNamespace --stack "$STACK")
    if [[ "$NS" != "$BOOT_NS" ]]; then
        echo "Error: --namespace '$NS' does not match the namespace-bootstrap export '$BOOT_NS' — the operator cannot deploy there." >&2
        exit 1
    fi

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just postgres _wait-ready --namespace "$NS" \
        --selector app.kubernetes.io/name=cloudnative-pg \
        --count 1 --retries "{{ retries }}" --interval "{{ interval }}"

    echo "Checking the Cluster CRD is registered..."
    kubectl get crd clusters.postgresql.cnpg.io

    echo "Operator ready in namespace '$NS'."

# Deploy the Barman Cloud CNPG-I backup plugin, then wait for the rollout.
# Runs standalone, or as the composite tail of configure-backup. Requires
# namespace-bootstrap (operators ns), cert-manager (pre-configured by the
# kops bootstrap), and a RUNNING CNPG operator — install order is
# deploy-operator BEFORE this recipe. The cluster stack probes this stack —
# apply it BEFORE deploy-cluster.
# Usage: just postgres deploy-backup-plugin [--stack <name>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('postgres')]
[no-cd]
deploy-backup-plugin stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cnpg-backup-plugin"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    # Guard: the operator namespace must exist (namespace-bootstrap stack).
    if ! kubectl get namespace operators >/dev/null 2>&1; then
        echo "Error: namespace 'operators' does not exist — run 'just gcp-pulumi apply-namespaces' (pulumi setup §5) first." >&2
        exit 1
    fi

    # The plugin requires a RUNNING CNPG operator (>= 1.26) and cert-manager.
    # deploy-operator installs the former; the kops bootstrap pre-configures
    # the latter.
    if ! kubectl get crd clusters.postgresql.cnpg.io >/dev/null 2>&1; then
        echo "Error: CNPG operator not installed — run 'just postgres deploy-operator' first (the plugin requires a running operator)." >&2
        exit 1
    fi
    if ! kubectl get crd certificaterequests.cert-manager.io >/dev/null 2>&1; then
        echo "Error: cert-manager not installed — the plugin needs it for CNPG-I TLS certificates. It is pre-configured by the kops bootstrap; check docs/reference/kops/bootstrap.md." >&2
        exit 1
    fi

    echo "Deploying $FOLDER (stack '$STACK') into namespace 'operators'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just postgres _wait-ready --namespace operators \
        --selector app.kubernetes.io/name=plugin-barman-cloud \
        --count 1 --retries "{{ retries }}" --interval "{{ interval }}"

    echo "Checking the ObjectStore CRD is registered..."
    kubectl get crd objectstores.barmancloud.cnpg.io

    echo "Barman Cloud plugin ready."

# Deploy the PostgreSQL 16 Cluster, then wait for every instance pod.
# One command for: ensure-stack, app-password secret, preview, apply, readiness.
# The app user password is never generated or defaulted — you supply it.
# Usage: just postgres deploy-cluster --app-password <pw> [--cluster <name>] [--namespace <ns>] [--stack <name>] [--instances <n>] [--retries <n>] [--interval <s>]
[arg("app_password", long="app-password", short="p", help="Password for the bootstrap database owner (required; stored encrypted)")]
[arg("cluster", long="cluster", short="c", help="Cluster name — must match properties.clusters[0].cluster.name (default logto)")]
[arg("namespace", long="namespace", short="n", help="Namespace holding the Cluster")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("instances", long="instances", short="i", help="Expected instance pod count (default 1)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 90)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('postgres')]
[no-cd]
deploy-cluster app_password cluster="logto" namespace="prod" stack="" instances="1" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="cloudnative-pg-cluster"
    NS="{{ namespace }}"
    CLUSTER="{{ cluster }}"
    APP_PASSWORD={{ quote(app_password) }}

    if [[ -z "$APP_PASSWORD" ]]; then
        echo "Error: --app-password is required; this recipe never invents a password." >&2
        exit 1
    fi

    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key 'properties.clusters[0].cluster.bootstrap.userSecret.password' \
        --value "$APP_PASSWORD"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just postgres _wait-ready --namespace "$NS" \
        --selector "cnpg.io/cluster=${CLUSTER}" \
        --count "{{ instances }}" --retries "{{ retries }}" --interval "{{ interval }}"

    echo "Waiting for Cluster '$CLUSTER' to report healthy..."
    healthy=""
    for i in $(seq 1 "{{ retries }}"); do
        phase=$(kubectl get cluster.postgresql.cnpg.io "$CLUSTER" -n "$NS" \
            -o jsonpath='{.status.phase}' 2>/dev/null || true)
        if [[ "$phase" == "Cluster in healthy state" ]]; then
            echo "Cluster phase: $phase"
            healthy="yes"
            break
        fi
        echo "Cluster phase: ${phase:-<missing>} (try $i/{{ retries }})..."
        sleep "{{ interval }}"
    done
    if [[ -z "$healthy" ]]; then
        echo "Error: Cluster '$CLUSTER' never reached 'Cluster in healthy state'." >&2
        kubectl describe cluster.postgresql.cnpg.io "$CLUSTER" -n "$NS" >&2 || true
        exit 1
    fi

    echo
    echo "PostgreSQL Services in $NS:"
    kubectl get svc -n "$NS" -l "cnpg.io/cluster=${CLUSTER}"

# Full post-install check: pool, operator, storage, Cluster CR, pods, PVCs,
# Services, ScheduledBackup. Exits non-zero if any required check fails.
# Usage: just postgres verify [--cluster <name>] [--namespace <ns>] [--operator-namespace <ns>] [--instances <n>] [--pool <label>] [--node-count <n>]
[arg("cluster", long="cluster", short="c", help="Cluster name (default logto)")]
[arg("namespace", long="namespace", short="n", help="Namespace holding the Cluster")]
[arg("operator_namespace", long="operator-namespace", help="Namespace holding the operator")]
[arg("instances", long="instances", short="i", help="Expected instance pod count (default 1)")]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", help="Expected node count in that pool (default 3)")]
[group('postgres')]
[no-cd]
verify cluster="logto" namespace="prod" operator_namespace="operators" instances="1" pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    CLUSTER="{{ cluster }}"
    NS="{{ namespace }}"
    OP_NS="{{ operator_namespace }}"
    WANT_INSTANCES="{{ instances }}"
    POOL="{{ pool }}"
    WANT_NODES="{{ node_count }}"
    failures=0

    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_COLOR=0

    nodes_json=$(kubectl get nodes -l "pool=$POOL" -o json)
    node_count=$(printf '%s\n' "$nodes_json" | jq '.items | length')
    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$node_count" -eq "$WANT_NODES" ]]; then
        ok "$node_count node(s) with pool=$POOL"
    else
        bad "$node_count node(s) with pool=$POOL, expected $WANT_NODES"
    fi
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    operator=$(kubectl get pods -n "$OP_NS" -l app.kubernetes.io/name=cloudnative-pg -o json 2>/dev/null \
        | jq '[.items[] | select(.status.phase == "Running")] | length' || echo 0)
    if [[ "$operator" -ge 1 ]]; then
        ok "operator Running in $OP_NS"
    else
        bad "no Running operator pod in $OP_NS"
    fi

    for sc in dictycr-balanced dictycr-ssd; do
        if kubectl get sc "$sc" >/dev/null 2>&1; then
            ok "StorageClass $sc present"
        else
            bad "StorageClass $sc missing"
        fi
    done

    if kubectl get cluster.postgresql.cnpg.io "$CLUSTER" -n "$NS" >/dev/null 2>&1; then
        ok "Cluster $CLUSTER present in $NS"
        phase=$(kubectl get cluster.postgresql.cnpg.io "$CLUSTER" -n "$NS" -o jsonpath='{.status.phase}')
        if [[ "$phase" == "Cluster in healthy state" ]]; then
            ok "Cluster phase: $phase"
        else
            bad "Cluster phase: $phase (expected 'Cluster in healthy state')"
        fi
    else
        bad "Cluster $CLUSTER missing in $NS"
    fi

    pods_ready=$(kubectl get pods -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" -o json \
        | jq '[.items[] | select(.status.phase == "Running")
        | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
    if [[ "$pods_ready" -eq "$WANT_INSTANCES" ]]; then
        ok "$pods_ready/$WANT_INSTANCES instance pods Running and ready"
    else
        bad "$pods_ready/$WANT_INSTANCES instance pods Running and ready"
    fi

    pvc_bound=$(kubectl get pvc -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" -o json \
        | jq '[.items[] | select(.status.phase == "Bound")] | length')
    if [[ "$pvc_bound" -eq "$WANT_INSTANCES" ]]; then
        ok "$pvc_bound Bound data PVC(s)"
    else
        bad "$pvc_bound Bound data PVC(s), expected $WANT_INSTANCES"
    fi

    for suffix in rw ro r; do
        svc="${CLUSTER}-${suffix}"
        port=$(kubectl get svc -n "$NS" "$svc" -o jsonpath='{.spec.ports[0].port}' 2>/dev/null || echo "")
        if [[ "$port" == "5432" ]]; then
            ok "Service $svc exposes 5432"
        else
            bad "Service $svc missing or not on 5432"
        fi
    done

    scheduled=$(kubectl get scheduledbackup.postgresql.cnpg.io -n "$NS" --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [[ "$scheduled" -ge 1 ]]; then
        ok "$scheduled ScheduledBackup(s) present"
    else
        bad "no ScheduledBackup found — the production stack always creates one"
    fi

    echo
    if [[ "$failures" -ne 0 ]]; then
        printf '%d check(s) failed. See reference/postgres/troubleshooting.md.\n' "$failures"
        exit 1
    fi
    printf 'All checks passed.\n'

# Destroy the PostgreSQL cluster stack. Removes the Cluster CR, Secrets and —
# because the backup bucket lives in the same stack with ForceDestroy: true —
# ALL backups in the bucket. Requires BOTH --delete-pvcs yes and
# --delete-backups yes so neither loss is silent.
# Usage: just postgres teardown --namespace <ns> --cluster <name> --delete-pvcs yes --delete-backups yes [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Namespace holding the Cluster")]
[arg("cluster", long="cluster", short="c", help="Cluster name (default logto)")]
[arg("delete_pvcs", long="delete-pvcs", help="Must be 'yes' — deletes the data PVCs left after the Cluster CR is gone")]
[arg("delete_backups", long="delete-backups", help="Must be 'yes' — the stack destroy deletes the backup bucket with every backup in it")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('postgres')]
[no-cd]
teardown namespace cluster="logto" delete_pvcs="no" delete_backups="no" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    CLUSTER="{{ cluster }}"
    DELETE_PVCS="{{ delete_pvcs }}"
    DELETE_BACKUPS="{{ delete_backups }}"
    STACK=$(just postgres _require-stack --stack "{{ stack }}")

    if [[ "$DELETE_PVCS" != "yes" || "$DELETE_BACKUPS" != "yes" ]]; then
        echo "Refusing teardown." >&2
        echo "This destroys the cloudnative-pg-cluster stack '$STACK':" >&2
        echo "  - the Cluster CR and its Secrets in namespace '$NS'" >&2
        echo "  - the data PVCs (all database files)" >&2
        echo "  - the backup bucket (ForceDestroy: true — ALL backups)" >&2
        echo "Re-run with --delete-pvcs yes --delete-backups yes to proceed." >&2
        exit 1
    fi

    echo "Destroying cloudnative-pg-cluster stack '$STACK'..."
    just gcp-pulumi remove-resource --folder cloudnative-pg-cluster --stack "$STACK"

    echo "Deleting leftover PVCs in '$NS'..."
    kubectl delete pvc -n "$NS" -l "cnpg.io/cluster=${CLUSTER}" --ignore-not-found

    echo "Teardown complete. Operator stack left in place — destroy it by hand with:"
    echo "  just gcp-pulumi remove-resource --folder cloudnative-pg-operator --stack $STACK"
