# ── private helpers ──────────────────────────────────────────────────────────

# Waits for a TCP port to accept connections.
# Usage: just arangodb _wait-for-port --port <port> [--retries <n>]
[arg("port", long="port", short="p", help="TCP port to wait for")]
[arg("retries", long="retries", short="r", help="Number of retries")]
[group('arangodb')]
[no-cd]
_wait-for-port port retries="30":
    #!/usr/bin/env bash
    set -euo pipefail
    for i in $(seq 1 {{ retries }}); do
        if nc -z localhost {{ port }} 2>/dev/null; then
            echo "Port-forward established on localhost:{{ port }}."
            exit 0
        fi
        sleep 1
    done
    echo "Error: localhost:{{ port }} did not open after {{ retries }}s."
    exit 1

# Discovers the arangodb-single-* service name in a namespace
[group('arangodb')]
[no-cd]
_discover-arango-svc namespace="dev":
    #!/usr/bin/env bash
    set -euo pipefail
    SVC=$(kubectl get svc -n {{ namespace }} --no-headers \
        -o custom-columns=":metadata.name" \
        | grep "^arangodb-single-" | head -n1)
    if [[ -z "$SVC" ]]; then
        echo "Error: No service matching 'arangodb-single-*' found in namespace '{{ namespace }}'" >&2
        exit 1
    fi
    echo "$SVC"

# ── public recipes ────────────────────────────────────────────────────────────

# Dump a remote ArangoDB database to a local compressed file.
# Usage: just arangodb dump-remote-db --db-name <name> [--output-dir <dir>] [--namespace <ns>] [--service <svc>] [--image-tag <tag>]
[arg("db_name", long="db-name", short="d", help="Database name")]
[arg("service", long="service", short="s", help="ArangoDB service name")]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace")]
[arg("image_tag", long="image-tag", short="t", help="ArangoDB image tag")]
[arg("output_dir", long="output-dir", short="o", help="Output directory")]
[group('arangodb')]
[no-cd]
dump-remote-db db_name output_dir="scratch" namespace="dev" service="arangodb" image_tag="3.11.6":
    #!/usr/bin/env bash
    set -euo pipefail

    # Configuration
    LOCAL_PORT=9255
    REMOTE_PORT=8529
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    DUMP_DIR="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}"
    ARCHIVE_FILE="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}.tar.gz"
    KUBECONFIG_FILE=$(mktemp -t kubeconfig)

    # Fetch kubeconfig for the remote cluster from kops state store
    echo "Fetching kubeconfig from kops state store..."
    kops export kubeconfig --kubeconfig="$KUBECONFIG_FILE" --admin=24h

    # Cleanup function
    cleanup() {
        echo "Stopping port-forward (PID: $PF_PID)..."
        kill $PF_PID || true
        echo "Removing temporary kubeconfig..."
        rm -f "$KUBECONFIG_FILE"
    }
    trap cleanup EXIT

    # Validation
    echo "Validating kubectl connectivity..."
    if ! KUBECONFIG="$KUBECONFIG_FILE" kubectl cluster-info > /dev/null 2>&1; then
        echo "Error: kubectl is not working or cannot connect to the cluster."
        exit 1
    fi

    # Ensure output directory exists
    mkdir -p "{{ output_dir }}"

    echo "Fetching credentials from secret 'backend'..."
    DB_USER=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret backend -n {{ namespace }} -o jsonpath='{.data.user}' | base64 -d)
    PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret backend -n {{ namespace }} -o jsonpath='{.data.password}' | base64 -d)

    echo "Starting port-forward to service/{{ service }}..."
    # Kill any existing port-forward on this port to avoid conflicts
    lsof -ti:8529 | xargs kill -9 2>/dev/null || true

    KUBECONFIG="$KUBECONFIG_FILE" kubectl port-forward -n {{ namespace }} service/{{ service }} ${LOCAL_PORT}:${REMOTE_PORT} > /dev/null 2>&1 &
    PF_PID=$!

    just arangodb _wait-for-port --port ${LOCAL_PORT}

    echo "Starting dump of database '{{ db_name }}'..."
    mkdir -p "$DUMP_DIR"

    DOCKER_NET_FLAGS="--net=host"
    ENDPOINT="tcp://127.0.0.1:${LOCAL_PORT}"

    echo "Running arangodump in Docker container..."
    docker run --rm $DOCKER_NET_FLAGS \
        -v "${PWD}/${DUMP_DIR}:/dump" \
        arangodb/arangodb:{{ image_tag }} \
        arangodump \
        --server.endpoint "$ENDPOINT" \
        --server.username "$DB_USER" \
        --server.password "$PASSWORD" \
        --output-directory /dump \
        --server.database "{{ db_name }}" \
        --include-system-collections

    echo "Compressing dump..."
    tar -czf "$ARCHIVE_FILE" -C "$(dirname "$DUMP_DIR")" "$(basename "$DUMP_DIR")"
    echo "Dump saved to $ARCHIVE_FILE"

    echo "Cleaning up temporary files..."
    rm -rf "$DUMP_DIR"

# List restic snapshots from GCS backup repository.
# Usage: just arangodb list-restic-snapshots [--bucket <b>] [--namespace <ns>] [--latest <n>]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket")]
[arg("latest", long="latest", short="l", help="Number of recent snapshots to show")]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace")]
[group('arangodb')]
[no-cd]
list-restic-snapshots bucket="restic-arangodb-backup-dcr-experiments" namespace="dev" latest="7":
    #!/usr/bin/env bash
    set -euo pipefail

    KUBECONFIG_FILE=$(mktemp -t kubeconfig)
    GCS_CREDS_FILE=$(mktemp -t gcs-creds)

    # Fetch kubeconfig for the remote cluster from kops state store
    echo "Fetching kubeconfig from kops state store..."
    kops export kubeconfig --kubeconfig="$KUBECONFIG_FILE" --admin=24h

    # Cleanup function
    cleanup() {
        echo "Removing temporary files..."
        rm -f "$KUBECONFIG_FILE" "$GCS_CREDS_FILE"
    }
    trap cleanup EXIT

    echo "Fetching restic password from cluster..."
    RESTIC_PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ namespace }} -o jsonpath='{.data.resticPass}' | base64 -d)

    echo "Fetching GCS credentials from cluster..."
    KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ namespace }} -o jsonpath='{.data.gcsCredentials}' | base64 -d > "$GCS_CREDS_FILE"

    echo "Listing restic snapshots in gs://{{ bucket }}..."
    if [[ -n "{{ latest }}" ]]; then
        RESTIC_REPOSITORY="gs:{{ bucket }}:/" \
        RESTIC_PASSWORD="$RESTIC_PASSWORD" \
        GOOGLE_APPLICATION_CREDENTIALS="$GCS_CREDS_FILE" \
        restic snapshots | { head -2; tail -n $(( {{ latest }} + 1 )); }
    else
        RESTIC_REPOSITORY="gs:{{ bucket }}:/" \
        RESTIC_PASSWORD="$RESTIC_PASSWORD" \
        GOOGLE_APPLICATION_CREDENTIALS="$GCS_CREDS_FILE" \
        restic snapshots
    fi

# Restore the most recent restic snapshot to the scratch folder.
# Usage: just arangodb restore-latest-snapshot [--bucket <b>] [--namespace <ns>] [--output-dir <dir>]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket")]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace")]
[arg("output_dir", long="output-dir", short="o", help="Output directory")]
[group('arangodb')]
[no-cd]
restore-latest-snapshot bucket="restic-arangodb-backup-dcr-experiments" namespace="dev" output_dir="scratch":
    #!/usr/bin/env bash
    set -euo pipefail

    KUBECONFIG_FILE=$(mktemp -t kubeconfig)
    GCS_CREDS_FILE=$(mktemp -t gcs-creds)

    # Fetch kubeconfig for the remote cluster from kops state store
    echo "Fetching kubeconfig from kops state store..."
    kops export kubeconfig --kubeconfig="$KUBECONFIG_FILE" --admin=24h

    # Cleanup function
    cleanup() {
        echo "Removing temporary files..."
        rm -f "$KUBECONFIG_FILE" "$GCS_CREDS_FILE"
    }
    trap cleanup EXIT

    echo "Fetching restic password from cluster..."
    RESTIC_PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ namespace }} -o jsonpath='{.data.resticPass}' | base64 -d)

    echo "Fetching GCS credentials from cluster..."
    KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ namespace }} -o jsonpath='{.data.gcsCredentials}' | base64 -d > "$GCS_CREDS_FILE"

    mkdir -p "{{ output_dir }}"

    echo "Restoring latest snapshot to {{ output_dir }}..."
    RESTIC_REPOSITORY="gs:{{ bucket }}:/" \
    RESTIC_PASSWORD="$RESTIC_PASSWORD" \
    GOOGLE_APPLICATION_CREDENTIALS="$GCS_CREDS_FILE" \
    restic restore latest --target "{{ output_dir }}"

    echo "Restore complete. Data available in {{ output_dir }}"

# Restore arangodump databases into the local k3d ArangoDB instance.
# Discovers the ArangoDB service name dynamically (pattern: arangodb-single-<id>),
# port-forwards to it, then runs arangorestore via Docker.
# Root password is read from the 'arangodb-root' k8s secret; after restore the operator JWT resets it back.
# Usage: just arangodb restore-local-arangodb [--input-dir <dir>] [--namespace <ns>] [--image-tag <tag>] [--cluster-name <name>]
[arg("input_dir", long="input-dir", short="i", help="Directory with arangodump data")]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace")]
[arg("image_tag", long="image-tag", short="t", help="ArangoDB image tag")]
[arg("cluster_name", long="cluster-name", short="c", help="k3d cluster name")]
[group('arangodb')]
[no-cd]
restore-local-arangodb input_dir="scratch/arangodump" namespace="dev" image_tag="3.11.6" cluster_name=`echo ${K3D_CLUSTER_NAME:-k3d-dev-cluster}`:
    #!/usr/bin/env bash
    set -euo pipefail

    LOCAL_PORT=9529
    REMOTE_PORT=8529
    KUBECONFIG_FILE=$(mktemp -t k3d-kubeconfig)
    JWT_FILE=$(mktemp -t arangodb-jwt)
    DUMP_DIR="${PWD}/{{ input_dir }}"
    LOST_FOUND_TMP=""
    PF_PID=""

    # Single cleanup covers all resources — no trap override needed
    cleanup() {
        [[ -n "$PF_PID" ]] && { echo "Stopping port-forward (PID: $PF_PID)..."; kill "$PF_PID" 2>/dev/null || true; }
        if [[ -n "$LOST_FOUND_TMP" ]]; then
            mv "$LOST_FOUND_TMP/lost+found" "$DUMP_DIR/lost+found" 2>/dev/null || true
            rm -rf "$LOST_FOUND_TMP"
        fi
        rm -f "$KUBECONFIG_FILE" "$JWT_FILE"
    }
    trap cleanup EXIT

    echo "Exporting k3d kubeconfig..."
    just k3d export-kubeconfig --name {{ cluster_name }} --output "$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"

    echo "Reading desired root password from 'arangodb-pass' secret..."
    NEW_ROOT_PASS=$(kubectl get secret arangodb-pass -n {{ namespace }} \
        -o jsonpath='{.data.password}' | base64 -d)
    if [[ -z "$NEW_ROOT_PASS" ]]; then
        echo "Error: 'arangodb-pass' secret has an empty password field."
        exit 1
    fi

    echo "Discovering ArangoDB service in namespace '{{ namespace }}'..."
    ARANGO_SVC=$(just arangodb _discover-arango-svc {{ namespace }})
    echo "Found service: $ARANGO_SVC"

    lsof -ti:${LOCAL_PORT} | xargs kill -9 2>/dev/null || true

    echo "Starting port-forward to service/$ARANGO_SVC..."
    kubectl port-forward -n {{ namespace }} "service/$ARANGO_SVC" ${LOCAL_PORT}:${REMOTE_PORT} > /dev/null 2>&1 &
    PF_PID=$!

    just arangodb _wait-for-port --port ${LOCAL_PORT}

    # Docker networking set by just os() at parse time — no runtime uname needed
    DOCKER_NET_FLAGS="{{ if os() == "macos" { "--add-host=host.docker.internal:host-gateway" } else { "--net=host" } }}"
    ENDPOINT="tcp://{{ if os() == "macos" { "host.docker.internal" } else { "127.0.0.1" } }}:${LOCAL_PORT}"

    # Temporarily move lost+found — it's a filesystem artifact ArangoDB rejects
    if [[ -d "$DUMP_DIR/lost+found" ]]; then
        LOST_FOUND_TMP=$(mktemp -d /tmp/lost+found-XXXXXX)
        mv "$DUMP_DIR/lost+found" "$LOST_FOUND_TMP/lost+found"
    fi

    echo "Fetching operator JWT secret for superuser access..."
    kubectl get secret arangodb-jwt -n {{ namespace }} \
        -o jsonpath='{.data.token}' | base64 -d > "$JWT_FILE"

    echo "Restoring databases from '{{ input_dir }}'..."
    docker run --rm $DOCKER_NET_FLAGS \
        -v "${DUMP_DIR}:/dump" \
        -v "${JWT_FILE}:/jwt.secret:ro" \
        arangodb/arangodb:{{ image_tag }} \
        arangorestore \
        --server.endpoint "$ENDPOINT" \
        --server.jwt-secret-keyfile /jwt.secret \
        --input-directory /dump \
        --all-databases true \
        --include-system-collections \
        --create-database true

    echo "Restore complete."

    echo "Resetting root password to local value from 'arangodb-root' secret..."
    docker run --rm $DOCKER_NET_FLAGS \
        -v "${JWT_FILE}:/jwt.secret:ro" \
        -e "ARANGO_NEW_PASS=${NEW_ROOT_PASS}" \
        arangodb/arangodb:{{ image_tag }} \
        arangosh \
        --server.endpoint "$ENDPOINT" \
        --server.jwt-secret-keyfile /jwt.secret \
        --javascript.execute-string \
            "require('@arangodb/users').update('root', process.env.ARANGO_NEW_PASS); print('Root password reset successfully.');"

    echo "Root password has been reset to the local value."

# Deploy ArangoDB operator and single instance to local k3d cluster.
# Usage: just arangodb deploy-local-arangodb [--stack <name>] [--storage-size <size>] [--cluster-name <name>] [--pass-entry <entry>] [--root-pass-entry <entry>]
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for stack passphrase")]
[arg("storage_size", long="storage-size", short="z", help="Storage size for the DB")]
[arg("cluster_name", long="cluster-name", short="c", help="k3d cluster name")]
[arg("root_pass_entry", long="root-pass-entry", short="r", help="pass entry for root password")]
[group('arangodb')]
[no-cd]
deploy-local-arangodb stack="local" storage_size="20Gi" cluster_name=`echo ${K3D_CLUSTER_NAME:-k3d-dev-cluster}` pass_entry="pulumi/local-passphrase" root_pass_entry="arangodb/local-root":
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    if ! ROOT_PASSWORD=$(pass show "{{ root_pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve root password from '{{ root_pass_entry }}'"
        exit 1
    fi

    KUBECONFIG_FILE=$(mktemp -t k3d-kubeconfig)
    cleanup() {
        rm -f "$KUBECONFIG_FILE"
    }
    trap cleanup EXIT

    echo "Exporting k3d kubeconfig..."
    just k3d export-kubeconfig --name {{ cluster_name }} --output "$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    echo "Creating local stacks..."
    just local-pulumi new-stack --folder arangodb-operator --stack {{ stack }} --pass-entry {{ pass_entry }}
    just local-pulumi new-stack --folder arangodb-single --stack {{ stack }} --pass-entry {{ pass_entry }}

    echo "Setting arangodb-operator config..."
    pulumi -C arangodb-operator config set-all --stack "{{ stack }}" --path \
        --plaintext "arangodb-operator:properties.namespace=dev" \
        --plaintext "arangodb-operator:properties.deploymentReplication=false" \
        --plaintext "arangodb-operator:properties.chart.name=kube-arangodb" \
        --plaintext "arangodb-operator:properties.chart.version=1.2.42" \
        --plaintext "arangodb-operator:properties.chart.repository=https://arangodb.github.io/kube-arangodb"

    echo "Setting arangodb-single config..."
    pulumi -C arangodb-single config set-all --stack "{{ stack }}" --path \
        --plaintext "arangodb-single:properties.namespace=dev" \
        --plaintext "arangodb-single:properties.version=3.11.6" \
        --plaintext "arangodb-single:properties.storage.class=dictycr-balanced" \
        --plaintext "arangodb-single:properties.storage.size={{ storage_size }}" \
        --plaintext "arangodb-single:properties.secret.name=arangodb-root" \
        --secret "arangodb-single:properties.secret.password=$ROOT_PASSWORD"

    echo "Deploying arangodb-operator..."
    just local-pulumi create-resource --folder arangodb-operator --stack {{ stack }} --pass-entry {{ pass_entry }}

    echo "Deploying arangodb-single..."
    just local-pulumi create-resource --folder arangodb-single --stack {{ stack }} --pass-entry {{ pass_entry }}

# Configure the arangodb-restore stack in one non-interactive command.
# Resolves every value the restore Job needs, then runs ensure-stack plus one
# pulumi config set-all against the arangodb-restore project.
#   --namespace   required; no safe default exists for a restore target.
#   --server      omitted: discovered from Services labelled arango_deployment=arangodb
#                 in that namespace (member/-int/-ea Services filtered out);
#                 falls back to "arangodb" with a warning if nothing is found.
#   --restore-id  omitted: "drill-<UTC YYYYMMDD-HHMMSS>" (DNS-1123 safe, unique per run).
#   --snapshot    "latest" or a specific restic snapshot id.
# confirmTarget is always computed as "<namespace>/<server>/<restoreId>" — the
# safety gate in arangodb-restore is unchanged, only the retyping is gone.
# Usage: just arangodb configure-restore --namespace <ns> [--server <svc>] [--restore-id <id>] [--snapshot <snap>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("server", long="server", short="s", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: drill-<UTC timestamp>)")]
[arg("snapshot", long="snapshot", short="p", help="restic snapshot id or 'latest'")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('arangodb')]
[no-cd]
configure-restore namespace server="" restore_id="" snapshot="latest" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    NAMESPACE="{{ namespace }}"
    SERVER="{{ server }}"
    RESTORE_ID="{{ restore_id }}"
    SNAPSHOT="{{ snapshot }}"
    STACK="{{ stack }}"

    # Job name is "arangodb-restore-<restoreId>" and must stay a <=63 char
    # DNS-1123 subdomain — same limit arangodb-restore/types.go enforces.
    MAX_RESTORE_ID_LEN=46

    if [[ -z "$NAMESPACE" ]]; then
        echo "Error: --namespace is required; there is no safe default restore target." >&2
        exit 1
    fi

    if [[ -z "$SNAPSHOT" ]]; then
        echo "Error: --snapshot cannot be empty; use 'latest' or a restic snapshot id." >&2
        exit 1
    fi

    if [[ -z "$SERVER" ]]; then
        echo "Discovering coordinator Service in namespace '$NAMESPACE' (label arango_deployment=arangodb)..."
        SERVER=$(kubectl get svc -n "$NAMESPACE" -l arango_deployment=arangodb \
            -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null \
            | grep -v -E -- '-(int|ea)$' \
            | grep -v -E -- '-(agnt|crdn|prmr|sngl)-' \
            | head -n1 || true)
        if [[ -z "$SERVER" ]]; then
            SERVER="arangodb"
            echo "Warning: no coordinator Service found (or kubectl unreachable) — falling back to '$SERVER'. Override with --server." >&2
        else
            echo "Discovered coordinator Service: $SERVER"
        fi
    fi

    if [[ -z "$RESTORE_ID" ]]; then
        RESTORE_ID="drill-$(date -u +%Y%m%d-%H%M%S)"
    fi

    if [[ ! "$RESTORE_ID" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]]; then
        echo "Error: restore id '$RESTORE_ID' must be a DNS-1123 label: lowercase alphanumeric and hyphens, not starting or ending with a hyphen." >&2
        exit 1
    fi

    if (( ${#RESTORE_ID} > MAX_RESTORE_ID_LEN )); then
        echo "Error: restore id '$RESTORE_ID' is ${#RESTORE_ID} chars, must be at most ${MAX_RESTORE_ID_LEN} so the Job name stays under the 63-char limit." >&2
        exit 1
    fi

    CONFIRM_TARGET="${NAMESPACE}/${SERVER}/${RESTORE_ID}"

    echo
    echo "arangodb-restore configuration"
    echo "  namespace     : ${NAMESPACE}"
    echo "  server        : ${SERVER}"
    echo "  restoreId     : ${RESTORE_ID}"
    echo "  snapshot      : ${SNAPSHOT}"
    echo "  confirmTarget : ${CONFIRM_TARGET}"
    echo "  job name      : arangodb-restore-${RESTORE_ID}"
    echo

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    STACK="${STACK:-${PULUMI_STACK:-dev}}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "arangodb-restore:properties.namespace=$NAMESPACE" \
        --plaintext "arangodb-restore:properties.server=$SERVER" \
        --plaintext "arangodb-restore:properties.restoreId=$RESTORE_ID" \
        --plaintext "arangodb-restore:properties.snapshot=$SNAPSHOT" \
        --plaintext "arangodb-restore:properties.confirmTarget=$CONFIRM_TARGET"

    echo
    echo "Config written. Next:"
    echo "  just gcp-pulumi preview --folder arangodb-restore"
    echo "  just gcp-pulumi create-resource --folder arangodb-restore"
