# Dump a remote ArangoDB database to a local compressed file

# Usage: just arangodb dump-remote-db <db_name> [output_dir] [namespace] [service] [image_tag]
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
[no-cd]
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

# Restore the most recent restic snapshot to the scratch folder

# Usage: just arangodb restore-latest-snapshot [bucket] [namespace] [output_dir]
[group('arangodb')]
[no-cd]
restore-latest-snapshot bucket="restic-arangodb-backup-dcr-experiments" namespace="dev" output_dir="scratch":
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

    mkdir -p "{{ output_dir }}"

    echo "Restoring latest snapshot to {{ output_dir }}..."
    RESTIC_REPOSITORY="gs:{{ bucket }}:/" \
    RESTIC_PASSWORD="$RESTIC_PASSWORD" \
    GOOGLE_APPLICATION_CREDENTIALS="$GCS_CREDS_FILE" \
    restic restore latest --target "{{ output_dir }}"

    echo "Restore complete. Data available in {{ output_dir }}"

# Deploy ArangoDB operator and single instance to local k3d cluster

# Usage: just arangodb deploy-local-arangodb [stack] [storage_size] [cluster_name] [pass_entry] [root_pass_entry]
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

    KUBECONFIG_FILE=$(mktemp /tmp/k3d-kubeconfig-XXXXXX.yaml)
    cleanup() {
        rm -f "$KUBECONFIG_FILE"
    }
    trap cleanup EXIT

    echo "Exporting k3d kubeconfig..."
    just k3d export-kubeconfig {{ cluster_name }} "$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    echo "Creating local stacks..."
    just local-pulumi new-stack arangodb-operator {{ stack }} {{ pass_entry }}
    just local-pulumi new-stack arangodb-single {{ stack }} {{ pass_entry }}

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
    just local-pulumi create-resource arangodb-operator {{ stack }} {{ pass_entry }}

    echo "Deploying arangodb-single..."
    just local-pulumi create-resource arangodb-single {{ stack }} {{ pass_entry }}
