# Dump a remote ArangoDB database to a local compressed file
# Usage: just arangodb dump-remote-db <db_name> [output_dir] [namespace] [service] [image_tag]
[no-cd]
[group('arangodb')]
dump-remote-db db_name output_dir="scratch" namespace="dev" service="arangodb" image_tag="3.11.6":
    #!/usr/bin/env bash
    set -euo pipefail

    # Configuration
    LOCAL_PORT=9255
    REMOTE_PORT=8529
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    DUMP_DIR="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}"
    ARCHIVE_FILE="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}.tar.gz"
    KUBECONFIG_FILE=$(mktemp /tmp/kubeconfig-XXXXXX.yaml)

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

    # Wait for port-forward
    echo "Waiting for connection to localhost:${LOCAL_PORT}..."
    for i in {1..30}; do
        if nc -z localhost $LOCAL_PORT 2>/dev/null; then
            echo "Port-forward established."
            break
        fi
        sleep 1
    done

    if ! nc -z localhost $LOCAL_PORT 2>/dev/null; then
        echo "Error: Failed to establish port-forward."
        exit 1
    fi

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
        --server.database "{{ db_name }}"

    echo "Compressing dump..."
    tar -czf "$ARCHIVE_FILE" -C "$(dirname "$DUMP_DIR")" "$(basename "$DUMP_DIR")"
    echo "Dump saved to $ARCHIVE_FILE"

    echo "Cleaning up temporary files..."
    rm -rf "$DUMP_DIR"

# List restic snapshots from GCS backup repository
# Usage: just arangodb list-restic-snapshots [bucket] [namespace] [latest=7]
[group('arangodb')]
list-restic-snapshots bucket="restic-arangodb-backup-dcr-experiments" namespace="dev" latest="7":
    #!/usr/bin/env bash
    set -euo pipefail

    KUBECONFIG_FILE=$(mktemp /tmp/kubeconfig-XXXXXX.yaml)
    GCS_CREDS_FILE=$(mktemp /tmp/gcs-creds-XXXXXX.json)

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
