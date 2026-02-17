# Dump a remote ArangoDB database to a local compressed file
# Usage: just arangodb dump-remote-db <db_name> [output_dir] [namespace] [service] [image_tag]
[group('arangodb')]
dump-remote-db db_name output_dir="scratch" namespace="dev" service="arangodb" image_tag="3.11.6":
    #!/usr/bin/env bash
    set -euo pipefail

    # Configuration
    LOCAL_PORT=8529
    REMOTE_PORT=8529
    TIMESTAMP=$(date +%Y%m%d-%H%M%S)
    DUMP_DIR="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}"
    ARCHIVE_FILE="{{ output_dir }}/{{ db_name }}-${TIMESTAMP}.tar.gz"

    # Validation
    echo "Validating kubectl connectivity..."
    if ! kubectl cluster-info > /dev/null 2>&1; then
        echo "Error: kubectl is not working or cannot connect to the cluster."
        exit 1
    fi

    # Ensure output directory exists
    mkdir -p "{{ output_dir }}"

    echo "Fetching credentials from secret 'backend'..."
    DB_USER=$(kubectl get secret backend -n {{ namespace }} -o jsonpath='{.data.user}' | base64 -d)
    PASSWORD=$(kubectl get secret backend -n {{ namespace }} -o jsonpath='{.data.password}' | base64 -d)

    echo "Starting port-forward to service/{{ service }}..."
    # Kill any existing port-forward on this port to avoid conflicts
    lsof -ti:8529 | xargs kill -9 2>/dev/null || true
    
    kubectl port-forward -n {{ namespace }} service/{{ service }} ${LOCAL_PORT}:${REMOTE_PORT} > /dev/null 2>&1 &
    PF_PID=$!

    # Cleanup function
    cleanup() {
        echo "Stopping port-forward (PID: $PF_PID)..."
        kill $PF_PID || true
    }
    trap cleanup EXIT

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

    # Determine Docker network flags based on OS
    if [[ "$OSTYPE" == "darwin"* ]]; then
        DOCKER_NET_FLAGS="--add-host=host.docker.internal:host-gateway"
        ENDPOINT="tcp://host.docker.internal:${LOCAL_PORT}"
    else
        DOCKER_NET_FLAGS="--net=host"
        ENDPOINT="tcp://127.0.0.1:${LOCAL_PORT}"
    fi

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
