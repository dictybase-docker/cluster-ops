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
    for i in $(seq 1 {{ quote(retries) }}); do
        if nc -z localhost {{ quote(port) }} 2>/dev/null; then
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
    SVC=$(kubectl get svc -n {{ quote(namespace) }} --no-headers \
        -o custom-columns=":metadata.name" \
        | grep "^arangodb-single-" | head -n1)
    if [[ -z "$SVC" ]]; then
        echo "Error: No service matching 'arangodb-single-*' found in namespace '{{ namespace }}'" >&2
        exit 1
    fi
    echo "$SVC"

# Resolve the Pulumi stack name, or fail. Never falls back to "dev" — every
# production recipe routes through this so a missing cluster env cannot
# silently target the lab stack.
# Usage: STACK=$(just arangodb _require-stack [--stack <name>])
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('arangodb')]
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

# Wait until at least <count> pods matching a selector are Running with every
# container ready, or time out.
# Usage: just arangodb _wait-ready --namespace <ns> --selector <sel> [--count <n>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace to watch")]
[arg("selector", long="selector", short="l", help="Label selector for the pods")]
[arg("count", long="count", short="c", help="How many ready pods to wait for")]
[arg("retries", long="retries", short="r", help="Probe attempts before failing")]
[arg("interval", long="interval", short="i", help="Seconds between probes")]
[group('arangodb')]
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

# Read-only guard: allow replacing queued/terminated Jobs, but never interrupt
# a pod with a running container.
[arg("namespace", long="namespace", short="n", help="Namespace containing Jobs")]
[arg("selector", long="selector", short="l", help="Job label selector")]
[group('arangodb')]
[private]
[no-cd]
_assert-no-running-jobs namespace selector:
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SELECTOR={{ quote(selector) }}
    jobs=$(kubectl get jobs -n "$NS" -l "$SELECTOR" -o json | jq -r '.items[]?.metadata.name')
    while IFS= read -r job; do
        [[ -z "$job" ]] && continue
        pods=$(kubectl get pods -n "$NS" -l "job-name=$job" -o json)
        running=$(printf '%s\n' "$pods" | jq '[.items[] | .status.initContainerStatuses[]?.state.running, .status.containerStatuses[]?.state.running] | map(select(. != null)) | length > 0')
        if [[ "$running" == "true" ]]; then
            echo "Error: Job '$job' in '$NS' still has a running container; wait before replacing it." >&2
            exit 1
        fi
    done <<< "$jobs"

# Reconcile prior Pulumi Job state, remove only Jobs with no running containers,
# then let the next update create unique per-run Job resource.
[arg("folder", long="folder", short="f", help="Pulumi project folder owning the Job")]
[arg("namespace", long="namespace", short="n", help="Namespace containing matching Jobs")]
[arg("selector", long="selector", short="l", help="Label selector for the Job family")]
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[group('arangodb')]
[private]
[no-cd]
_refresh-before-job folder namespace selector stack:
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER={{ quote(folder) }}
    NS={{ quote(namespace) }}
    SELECTOR={{ quote(selector) }}
    STACK={{ quote(stack) }}

    if [[ -z "${PULUMI_GCP_CREDENTIALS:-}" || ! -r "$PULUMI_GCP_CREDENTIALS" ]]; then
        echo "Error: PULUMI_GCP_CREDENTIALS must point to a readable credential file." >&2
        exit 1
    fi
    just arangodb _assert-no-running-jobs --namespace "$NS" --selector "$SELECTOR"
    jobs=$(kubectl get jobs -n "$NS" -l "$SELECTOR" -o json | jq -r '.items[]?.metadata.name')
    while IFS= read -r job; do
        [[ -z "$job" ]] && continue
        kubectl delete job -n "$NS" "$job" --wait=true
    done <<< "$jobs"

    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" refresh --stack "$STACK" --yes

# Wait for a Job to succeed. Fails fast (and tails logs) if the Job fails.
# A Job that disappears after we have already seen it counts as done —
# `reset-root-password` and `create-arangodb-databases` Jobs retain state until
# the next run; restore Jobs retain their TTL cleanup.
# Usage: just arangodb _wait-job --namespace <ns> --job <name> [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace holding the Job")]
[arg("job", long="job", short="j", help="Job name")]
[arg("retries", long="retries", short="r", help="Probe attempts before failing")]
[arg("interval", long="interval", short="i", help="Seconds between probes")]
[group('arangodb')]
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
    KUBECONFIG_FILE=$(mktemp -t kubeconfig-XXXXXX)

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
    DB_USER=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret backend -n {{ quote(namespace) }} -o jsonpath='{.data.user}' | base64 -d)
    PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret backend -n {{ quote(namespace) }} -o jsonpath='{.data.password}' | base64 -d)

    echo "Starting port-forward to service/{{ service }}..."
    # Kill any existing port-forward on this port to avoid conflicts
    lsof -ti:8529 | xargs kill -9 2>/dev/null || true

    KUBECONFIG="$KUBECONFIG_FILE" kubectl port-forward -n {{ quote(namespace) }} service/{{ quote(service) }} ${LOCAL_PORT}:${REMOTE_PORT} > /dev/null 2>&1 &
    PF_PID=$!

    just arangodb _wait-for-port --port ${LOCAL_PORT}

    echo "Starting dump of database '{{ db_name }}'..."
    mkdir -p "$DUMP_DIR"

    DOCKER_NET_FLAGS="--net=host"
    ENDPOINT="tcp://127.0.0.1:${LOCAL_PORT}"

    echo "Running arangodump in Docker container..."
    docker run --rm $DOCKER_NET_FLAGS \
        -v "${PWD}/${DUMP_DIR}:/dump" \
        arangodb:{{ quote(image_tag) }} \
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

    KUBECONFIG_FILE=$(mktemp -t kubeconfig-XXXXXX)
    GCS_CREDS_FILE=$(mktemp -t gcs-creds-XXXXXX)

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
    RESTIC_PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ quote(namespace) }} -o jsonpath='{.data.resticPass}' | base64 -d)

    echo "Fetching GCS credentials from cluster..."
    KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ quote(namespace) }} -o jsonpath='{.data.gcsCredentials}' | base64 -d > "$GCS_CREDS_FILE"

    echo "Listing restic snapshots in gs://{{ bucket }}..."
    if [[ -n "{{ latest }}" ]]; then
        RESTIC_REPOSITORY="gs:{{ bucket }}:/" \
        RESTIC_PASSWORD="$RESTIC_PASSWORD" \
        GOOGLE_APPLICATION_CREDENTIALS="$GCS_CREDS_FILE" \
        restic snapshots | { head -2; tail -n $(( {{ latest }} + 1 )); }  # lint-recipes:allow-unquoted-interp (arithmetic context)
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

    KUBECONFIG_FILE=$(mktemp -t kubeconfig-XXXXXX)
    GCS_CREDS_FILE=$(mktemp -t gcs-creds-XXXXXX)

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
    RESTIC_PASSWORD=$(KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ quote(namespace) }} -o jsonpath='{.data.resticPass}' | base64 -d)

    echo "Fetching GCS credentials from cluster..."
    KUBECONFIG="$KUBECONFIG_FILE" kubectl get secret dictycr -n {{ quote(namespace) }} -o jsonpath='{.data.gcsCredentials}' | base64 -d > "$GCS_CREDS_FILE"

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
    KUBECONFIG_FILE=$(mktemp -t k3d-kubeconfig-XXXXXX)
    JWT_FILE=$(mktemp -t arangodb-jwt-XXXXXX)
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
    just k3d export-kubeconfig --name {{ quote(cluster_name) }} --output "$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"

    echo "Reading desired root password from 'arangodb-pass' secret..."
    NEW_ROOT_PASS=$(kubectl get secret arangodb-pass -n {{ quote(namespace) }} \
        -o jsonpath='{.data.password}' | base64 -d)
    if [[ -z "$NEW_ROOT_PASS" ]]; then
        echo "Error: 'arangodb-pass' secret has an empty password field."
        exit 1
    fi

    echo "Discovering ArangoDB service in namespace '{{ namespace }}'..."
    ARANGO_SVC=$(just arangodb _discover-arango-svc {{ quote(namespace) }})
    echo "Found service: $ARANGO_SVC"

    lsof -ti:${LOCAL_PORT} | xargs kill -9 2>/dev/null || true

    echo "Starting port-forward to service/$ARANGO_SVC..."
    kubectl port-forward -n {{ quote(namespace) }} "service/$ARANGO_SVC" ${LOCAL_PORT}:${REMOTE_PORT} > /dev/null 2>&1 &
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
    kubectl get secret arangodb-jwt -n {{ quote(namespace) }} \
        -o jsonpath='{.data.token}' | base64 -d > "$JWT_FILE"

    echo "Restoring databases from '{{ input_dir }}'..."
    docker run --rm $DOCKER_NET_FLAGS \
        -v "${DUMP_DIR}:/dump" \
        -v "${JWT_FILE}:/jwt.secret:ro" \
        arangodb:{{ quote(image_tag) }} \
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
        arangodb:{{ quote(image_tag) }} \
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

    if ! PASSPHRASE=$(pass show {{ quote(pass_entry) }} | head -n 1); then
        echo "Error: Failed to retrieve passphrase from" {{ quote(pass_entry) }}
        exit 1
    fi

    if ! ROOT_PASSWORD=$(pass show {{ quote(root_pass_entry) }} | head -n 1); then
        echo "Error: Failed to retrieve root password from" {{ quote(root_pass_entry) }}
        exit 1
    fi

    KUBECONFIG_FILE=$(mktemp -t k3d-kubeconfig-XXXXXX)
    cleanup() {
        rm -f "$KUBECONFIG_FILE"
    }
    trap cleanup EXIT

    echo "Exporting k3d kubeconfig..."
    just k3d export-kubeconfig --name {{ quote(cluster_name) }} --output "$KUBECONFIG_FILE"
    export KUBECONFIG="$KUBECONFIG_FILE"

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    echo "Creating local stacks..."
    just local-pulumi new-stack --folder arangodb-operator --stack {{ quote(stack) }} --pass-entry {{ quote(pass_entry) }}
    just local-pulumi new-stack --folder arangodb-single --stack {{ quote(stack) }} --pass-entry {{ quote(pass_entry) }}

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
    just local-pulumi create-resource --folder arangodb-operator --stack {{ quote(stack) }} --pass-entry {{ quote(pass_entry) }}

    echo "Deploying arangodb-single..."
    just local-pulumi create-resource --folder arangodb-single --stack {{ quote(stack) }} --pass-entry {{ quote(pass_entry) }}

# Internal: read-only preflight for a single-database restore. Validates the
# name, proves the snapshot holds /arangodump/<db>, and fails closed on an
# already-existing target database unless overwrite is yes. Callers run this
# BEFORE their first mutation, so a typo or a surprising target database
# aborts before any stack config or Job exists.
# Usage: just arangodb _preflight-database-restore --namespace <ns> --server <svc> --database <db> --bucket <b> --secret <name> --snapshot <id|latest> --stack <name> [--overwrite <yes|no>]
[arg("namespace", long="namespace", short="n", help="Target namespace (required)")]
[arg("server", long="server", short="v", help="Coordinator Service name")]
[arg("database", long="database", short="d", help="Database the restore will target")]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket holding the snapshot")]
[arg("secret", long="secret", short="c", help="Secret holding resticPass/gcsProject/gcsCredentials")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id, or 'latest'")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("overwrite", long="overwrite", short="w", help="Allow replacing an existing database: yes|no (default no)")]
[group('arangodb')]
[private]
[no-cd]
_preflight-database-restore namespace server database bucket secret snapshot stack overwrite="no":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    DB={{ quote(database) }}
    BUCKET={{ quote(bucket) }}
    SECRET={{ quote(secret) }}
    SNAPSHOT={{ quote(snapshot) }}
    OVERWRITE={{ quote(overwrite) }}

    if [[ ! "$DB" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: --database '$DB' must be a valid ArangoDB database name: an ASCII letter or" >&2
        echo "       underscore, then letters, digits, underscores or hyphens, at most 64 chars." >&2
        exit 1
    fi
    case "$OVERWRITE" in
        yes|true|no|false|"") ;;
        *)
            echo "Error: --overwrite takes yes or no; got '$OVERWRITE'." >&2
            exit 1
            ;;
    esac

    echo "Preflight: does snapshot '$SNAPSHOT' in gs://$BUCKET hold /arangodump/$DB?"
    ENTRIES=$(just arangodb _restic-ls \
        --namespace "$NS" --bucket "$BUCKET" --secret "$SECRET" \
        --snapshot "$SNAPSHOT" --path "/arangodump/$DB" | grep -c .)
    echo "  yes: $ENTRIES entries under /arangodump/$DB"

    probe_rc=0
    just arangodb _database-exists \
        --namespace "$NS" --server "$SERVER" \
        --database "$DB" --stack {{ quote(stack) }} || probe_rc=$?
    if [[ "$probe_rc" -ne 0 && "$probe_rc" -ne 3 ]]; then
        echo "Error: could not determine whether database '$DB' exists on '$SERVER' (probe exit $probe_rc)." >&2
        exit 1
    fi
    if [[ "$probe_rc" -eq 0 && "$OVERWRITE" != "yes" && "$OVERWRITE" != "true" ]]; then
        echo "Error: database '$DB' already exists on '$SERVER' in namespace '$NS'." >&2
        echo "       A single-database restore replaces its collections. Nothing was changed." >&2
        echo "       Re-run the same recipe with --overwrite yes to do that deliberately." >&2
        echo "       Users and grants live in _system and are NOT part of a database restore." >&2
        exit 1
    fi
    if [[ "$probe_rc" -eq 3 ]]; then
        echo "  target database '$DB' does not exist yet; arangorestore --create-database true creates it"
    else
        echo "  target database '$DB' exists and --overwrite yes was given: its collections are replaced"
    fi
    if [[ "$DB" == "_system" ]]; then
        echo "Warning: restoring _system also restores the source's users and root password." >&2
        echo "         Run 'just arangodb reset-root-password' after the restore." >&2
    fi

# Configure the arangodb-restore stack in one non-interactive command.
# Resolves every value the restore Job needs, then runs ensure-stack plus one
# pulumi config set-all against the arangodb-restore project.
#   --namespace   required; no safe default exists for a restore target.
#   --server      omitted: discovered from Services labelled arango_deployment=arangodb
#                 in that namespace (member/-int/-ea Services filtered out);
#                 falls back to "arangodb" with a warning if nothing is found.
#   --restore-id  omitted: "drill-<UTC YYYYMMDD-HHMMSS>" (DNS-1123 safe, unique per run).
#   --snapshot    "latest" or a specific restic snapshot id.
#   --database    optional: restore ONLY that database. Adds a read-only preflight
#                 proving the snapshot holds /arangodump/<database> and refusing to
#                 touch an existing target database without --overwrite yes.
#   --overwrite   yes|no (default no). Requires --database: a whole-instance DR
#                 drill must never overwrite unconditionally.
# Without --database the stack keeps its whole-instance DR behavior, and any
# database/overwrite keys left by an earlier single-database run are removed
# in the same step.
# confirmTarget is always computed as "<namespace>/<server>/<restoreId>" — the
# safety gate in arangodb-restore is unchanged, only the retyping is gone.
# Usage: just arangodb configure-restore --namespace <ns> [--server <svc>] [--restore-id <id>] [--snapshot <snap>] [--database <db>] [--overwrite yes|no] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("server", long="server", short="s", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: drill-<UTC timestamp>)")]
[arg("snapshot", long="snapshot", short="p", help="restic snapshot id or 'latest'")]
[arg("database", long="database", short="d", help="Restore only this database (default: whole instance)")]
[arg("overwrite", long="overwrite", short="w", help="Replace an existing target database: yes|no (default no)")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('arangodb')]
[no-cd]
configure-restore namespace server="" restore_id="" snapshot="latest" database="" overwrite="no" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    NAMESPACE="{{ namespace }}"
    SERVER="{{ server }}"
    RESTORE_ID="{{ restore_id }}"
    SNAPSHOT="{{ snapshot }}"
    DATABASE="{{ database }}"
    OVERWRITE="{{ overwrite }}"
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

    # Same rules arangodb-restore/types.go enforces, checked here so a bad flag
    # fails before anything is mutated. yes/no are the operator spellings;
    # true/false are accepted because that is what stack config stores.
    case "$OVERWRITE" in
        yes|true) OVERWRITE_VALUE="true" ;;
        no|false|"") OVERWRITE_VALUE="false" ;;
        *)
            echo "Error: --overwrite takes yes or no; got '$OVERWRITE'." >&2
            exit 1
            ;;
    esac
    if [[ "$OVERWRITE_VALUE" == "true" && -z "$DATABASE" ]]; then
        echo "Error: --overwrite needs --database — overwriting only applies to a single-database restore." >&2
        exit 1
    fi
    if [[ -n "$DATABASE" && ! "$DATABASE" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: --database '$DATABASE' must be a valid ArangoDB database name: an ASCII letter or" >&2
        echo "       underscore, then letters, digits, underscores or hyphens, at most 64 chars." >&2
        exit 1
    fi

    # Resolved before the preflight, not at the set-all below: the
    # single-database preflight reads this stack's own bucket/secret from its
    # config file, and that must happen before ensure-stack creates anything.
    STACK="${STACK:-${PULUMI_STACK:-dev}}"

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

    # --- single-database preflight (read-only, before any mutation) --------
    if [[ -n "$DATABASE" ]]; then
        CONFIG="arangodb-restore/Pulumi.${STACK}.yaml"
        if [[ ! -f "$CONFIG" ]]; then
            echo "Error: $CONFIG is missing; initialize the stack first:" >&2
            echo "       just gcp-pulumi ensure-stack --folder arangodb-restore --stack $STACK" >&2
            exit 1
        fi
        STACK_BUCKET=$(yq -r '.config."arangodb-restore:properties".bucket // ""' "$CONFIG")
        STACK_SECRET=$(yq -r '.config."arangodb-restore:properties".resticSecret.name // ""' "$CONFIG")
        if [[ -z "$STACK_BUCKET" || "$STACK_BUCKET" == "null" || -z "$STACK_SECRET" || "$STACK_SECRET" == "null" ]]; then
            echo "Error: could not read bucket/resticSecret.name from $CONFIG." >&2
            echo "       Run 'just arangodb reset-restore-config --stack $STACK' to restore the DR defaults." >&2
            exit 1
        fi
        just arangodb _preflight-database-restore \
            --namespace "$NAMESPACE" \
            --server "$SERVER" \
            --database "$DATABASE" \
            --bucket "$STACK_BUCKET" \
            --secret "$STACK_SECRET" \
            --snapshot "$SNAPSHOT" \
            --stack "$STACK" \
            --overwrite "$OVERWRITE"
    fi

    echo
    echo "arangodb-restore configuration"
    echo "  namespace     : ${NAMESPACE}"
    echo "  server        : ${SERVER}"
    echo "  restoreId     : ${RESTORE_ID}"
    echo "  snapshot      : ${SNAPSHOT}"
    echo "  confirmTarget : ${CONFIRM_TARGET}"
    echo "  job name      : arangodb-restore-${RESTORE_ID}"
    if [[ -n "$DATABASE" ]]; then
        echo "  database      : ${DATABASE} (single-database restore)"
        echo "  overwrite     : ${OVERWRITE_VALUE}"
    else
        echo "  scope         : whole instance (all databases)"
    fi
    echo

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    SET_ARGS=(
        --path
        --plaintext "arangodb-restore:properties.namespace=$NAMESPACE"
        --plaintext "arangodb-restore:properties.server=$SERVER"
        --plaintext "arangodb-restore:properties.restoreId=$RESTORE_ID"
        --plaintext "arangodb-restore:properties.snapshot=$SNAPSHOT"
        --plaintext "arangodb-restore:properties.confirmTarget=$CONFIRM_TARGET"
    )
    if [[ -n "$DATABASE" ]]; then
        SET_ARGS+=(
            --plaintext "arangodb-restore:properties.database=$DATABASE"
            --plaintext "arangodb-restore:properties.overwrite=$OVERWRITE_VALUE"
        )
    fi
    pulumi -C "$FOLDER" config set-all --stack "$STACK" "${SET_ARGS[@]}"

    # Stale-value guard: a whole-instance run must never inherit an earlier
    # single-database filter, or the next DR drill silently restores one
    # database and reports success.
    if [[ -z "$DATABASE" ]]; then
        for key in database overwrite; do
            pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
        done
    fi

    echo
    echo "Config written. Next:"
    echo "  just gcp-pulumi preview --folder arangodb-restore"
    echo "  just gcp-pulumi create-resource --folder arangodb-restore"

# Probe until a namespaced resource list is empty, or time out.
# Missing CRD / resource type counts as gone (operator already uninstalled).
# Usage: just arangodb _wait-gone --namespace <ns> --kind <kind> [--selector <label>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace to watch")]
[arg("kind", long="kind", short="k", help="Resource kind, e.g. pods or arangodeployment")]
[arg("selector", long="selector", short="l", help="Optional label selector")]
[arg("retries", long="retries", short="r", help="Probe attempts before failing")]
[arg("interval", long="interval", short="i", help="Seconds between probes")]
[group('arangodb')]
[no-cd]
_wait-gone namespace kind selector="" retries="36" interval="5":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    KIND="{{ kind }}"
    SELECTOR="{{ selector }}"
    RETRIES="{{ retries }}"
    INTERVAL="{{ interval }}"
    extra=()
    if [[ -n "$SELECTOR" ]]; then
        extra+=(-l "$SELECTOR")
    fi

    for i in $(seq 1 "$RETRIES"); do
        set +e
        out=$(kubectl get "$KIND" -n "$NS" "${extra[@]}" -o json 2>&1)
        status=$?
        set -e
        if [[ $status -ne 0 ]]; then
            if echo "$out" | grep -qiE "doesn.t have a resource type|the server could not find the requested resource|namespaces? \"$NS\" not found"; then
                echo "$KIND gone in $NS (API type or namespace not found)."
                exit 0
            fi
            echo "$out" >&2
            echo "Error: kubectl get $KIND -n $NS failed." >&2
            exit 1
        fi
        count=$(printf '%s\n' "$out" | jq '.items | length')
        if [[ "$count" -eq 0 ]]; then
            echo "$KIND gone in $NS."
            exit 0
        fi
        echo "Waiting for $KIND in $NS to go ($count left, try $i/$RETRIES)..."
        sleep "$INTERVAL"
    done

    echo "Error: $KIND in $NS still present after $((RETRIES * INTERVAL))s." >&2
    kubectl get "$KIND" -n "$NS" "${extra[@]}" || true
    exit 1

# Destroy a Pulumi folder if that stack exists; skip (do not fail) if it never did.
# Usage: just arangodb _destroy-stack --folder <dir> [--stack <name>]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[group('arangodb')]
[no-cd]
_destroy-stack folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    STACK="{{ stack }}"
    if [[ -z "$STACK" ]]; then
        STACK="${PULUMI_STACK:-}"
    fi
    if [[ -z "$STACK" ]]; then
        echo "Error: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    FOLDER="{{ folder }}"

    if ! json=$(pulumi -C "$FOLDER" stack ls --json); then
        echo "Error: cannot list Pulumi stacks in $FOLDER." >&2
        exit 1
    fi
    if echo "$json" | jq -e --arg n "$STACK" 'any(.[]; (.name == $n) or (.name | endswith("/" + $n)))' >/dev/null; then
        echo "Destroying $FOLDER stack '$STACK'..."
        just gcp-pulumi remove-resource --folder "$FOLDER" --stack "$STACK"
    else
        echo "Stack '$STACK' not found in $FOLDER — skipping."
    fi

# Delete leftover ArangoDB PVCs. Operator often keeps them after the CR is gone.
# Usage: just arangodb _remove-pvcs --namespace <ns>
[arg("namespace", long="namespace", short="n", help="Namespace whose Arango PVCs to delete")]
[group('arangodb')]
[no-cd]
_remove-pvcs namespace:
    #!/usr/bin/env bash
    set -euo pipefail
    NS="{{ namespace }}"
    echo "Deleting PVCs labelled arango_deployment=arangodb in $NS..."
    kubectl delete pvc -n "$NS" -l arango_deployment=arangodb --ignore-not-found --wait=false

# Print leftover Arango instance resources and exit 1 if the instance is not clean.
# Leftover PVCs fail only when --delete-pvcs yes (otherwise they are a warning).
# Usage: just arangodb _report-clean --namespace <ns> [--delete-pvcs yes] [--operator-namespace <ns>]
[arg("namespace", long="namespace", short="n", help="Namespace that held the ArangoDeployment")]
[arg("delete_pvcs", long="delete-pvcs", pattern="yes|no", help="Whether leftover PVCs are a failure")]
[arg("operator_namespace", long="operator-namespace", help="Namespace that held the operator (default prod)")]
[group('arangodb')]
[no-cd]
_report-clean namespace delete_pvcs="no" operator_namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    OP_NS="{{ operator_namespace }}"
    DELETE_PVCS="{{ delete_pvcs }}"

    count_kind() {
        local kind="$1" ns="$2" sel="${3:-}"
        local extra=()
        if [[ -n "$sel" ]]; then
            extra+=(-l "$sel")
        fi
        set +e
        local out
        out=$(kubectl get "$kind" -n "$ns" "${extra[@]}" -o json 2>&1)
        local status=$?
        set -e
        if [[ $status -ne 0 ]]; then
            if echo "$out" | grep -qiE "doesn.t have a resource type|the server could not find the requested resource|namespaces? \"$ns\" not found"; then
                echo 0
                return 0
            fi
            echo "$out" >&2
            echo "Error: kubectl get $kind -n $ns failed." >&2
            exit 1
        fi
        printf '%s\n' "$out" | jq '.items | length'
    }

    cr=$(count_kind arangodeployment "$NS")
    pods=$(count_kind pods "$NS" arango_deployment=arangodb)
    svcs=$(count_kind svc "$NS" arango_deployment=arangodb)
    pvcs=$(count_kind pvc "$NS" arango_deployment=arangodb)
    operator=$(count_kind pods "$OP_NS" app.kubernetes.io/name=kube-arangodb)

    echo
    echo "arangodb teardown report ($NS)"
    echo "  ArangoDeployment : $cr"
    echo "  member pods      : $pods"
    echo "  member services  : $svcs"
    echo "  member PVCs      : $pvcs"
    echo "  operator pods    : $operator (ns $OP_NS)"
    echo

    dirty=0
    if [[ "$cr" -ne 0 || "$pods" -ne 0 || "$svcs" -ne 0 || "$operator" -ne 0 ]]; then
        dirty=1
    fi
    if [[ "$pvcs" -ne 0 ]]; then
        if [[ "$DELETE_PVCS" == "yes" ]]; then
            dirty=1
        else
            echo "Warning: $pvcs PVC(s) remain. Re-run with --delete-pvcs yes if those disks may go."
        fi
    fi

    if [[ "$dirty" -ne 0 ]]; then
        echo "Error: ArangoDB instance is not clean." >&2
        exit 1
    fi
    echo "ArangoDB instance is clean."

# Tear down the ArangoDB Cluster instance, then the operator, then leftover PVCs.
# Waits for each step to go before the next. Does not touch backup, loaders, or backup_secrets.
# Usage: just arangodb teardown --namespace <ns> [--delete-pvcs yes] [--stack <name>] [--retries <n>] [--interval <s>] [--operator-namespace <ns>]
[arg("namespace", long="namespace", short="n", help="Namespace that holds the ArangoDeployment (required)")]
[arg("delete_pvcs", long="delete-pvcs", pattern="yes|no", help="Delete leftover Arango PVCs after the operator is gone")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per wait (default 36)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 5)")]
[arg("operator_namespace", long="operator-namespace", help="Namespace that holds the operator (default prod)")]
[group('arangodb')]
[no-cd]
teardown namespace delete_pvcs="no" stack="" retries="36" interval="5" operator_namespace="prod":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    OP_NS="{{ operator_namespace }}"
    DELETE_PVCS="{{ delete_pvcs }}"
    STACK="{{ stack }}"
    RETRIES="{{ retries }}"
    INTERVAL="{{ interval }}"

    if [[ -z "$NS" ]]; then
        echo "Error: --namespace is required; there is no safe default teardown target." >&2
        exit 1
    fi
    if [[ -z "$STACK" ]]; then
        STACK="${PULUMI_STACK:-}"
    fi
    if [[ -z "$STACK" ]]; then
        echo "Error: no stack name — set PULUMI_STACK (via cluster env) or pass --stack." >&2
        exit 1
    fi
    if [[ ! "$RETRIES" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: --retries must be a positive integer, got '$RETRIES'." >&2
        exit 1
    fi
    if [[ ! "$INTERVAL" =~ ^[1-9][0-9]*$ ]]; then
        echo "Error: --interval must be a positive integer, got '$INTERVAL'." >&2
        exit 1
    fi
    WAIT=(--retries "$RETRIES" --interval "$INTERVAL")

    echo "Destroying arangodb-cluster..."
    just arangodb _destroy-stack --folder arangodb-cluster --stack "$STACK"
    just arangodb _wait-gone --namespace "$NS" --kind arangodeployment "${WAIT[@]}"
    just arangodb _wait-gone --namespace "$NS" --kind pods --selector arango_deployment=arangodb "${WAIT[@]}"
    just arangodb _wait-gone --namespace "$NS" --kind svc --selector arango_deployment=arangodb "${WAIT[@]}"

    echo "Destroying arangodb-operator..."
    just arangodb _destroy-stack --folder arangodb-operator --stack "$STACK"
    just arangodb _wait-gone --namespace "$OP_NS" --kind pods --selector app.kubernetes.io/name=kube-arangodb "${WAIT[@]}"

    if [[ "$DELETE_PVCS" == "yes" ]]; then
        just arangodb _remove-pvcs --namespace "$NS"
        just arangodb _wait-gone --namespace "$NS" --kind pvc --selector arango_deployment=arangodb "${WAIT[@]}"
    fi

    just arangodb _report-clean --namespace "$NS" --delete-pvcs "$DELETE_PVCS" --operator-namespace "$OP_NS"

# Deploy the kube-arangodb operator stack, then wait until it is serving.
# One command for: ensure-stack, preview, apply, operator pod ready, CRD present.
# Since kube-arangodb 1.4 the operator is namespaced — it watches only its
# own pod's namespace — so the release installs into the app namespace,
# next to the ArangoDeployment CRs.
# Usage: just arangodb deploy-operator [--stack <name>] [--namespace <ns>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("namespace", long="namespace", short="n", help="Namespace the operator is installed into; must equal the namespace-bootstrap appNamespace export")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
deploy-operator stack="" namespace="prod" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-operator"
    NS="{{ namespace }}"
    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    # The program takes the release namespace from the namespace-bootstrap
    # stack's appNamespace export — refuse to wait on anything else.
    BOOT_NS=$(pulumi -C namespace-bootstrap stack output appNamespace --stack "$STACK")
    if [[ "$NS" != "$BOOT_NS" ]]; then
        echo "Error: --namespace '$NS' does not match the namespace-bootstrap export '$BOOT_NS' — the operator cannot deploy there." >&2
        exit 1
    fi

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just arangodb _wait-ready --namespace "$NS" \
        --selector app.kubernetes.io/name=kube-arangodb \
        --count 1 --retries "{{ retries }}" --interval "{{ interval }}"

    echo "Checking the ArangoDeployment CRD is registered..."
    kubectl get crd arangodeployments.database.arangodb.com

    echo "Operator ready in namespace '$NS'."

# Deploy the production ArangoDB Cluster, then wait for every member pod.
# One command for: ensure-stack, root-password secret, preview, apply, readiness.
# The root password is never generated or defaulted — you supply it.
# Usage: just arangodb deploy-cluster --root-password <pw> [--namespace <ns>] [--stack <name>] [--members <n>] [--retries <n>] [--interval <s>]
[arg("root_password", long="root-password", short="p", help="ArangoDB root password (required; stored encrypted as properties.secret.password)")]
[arg("namespace", long="namespace", short="n", help="Namespace holding the ArangoDeployment")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("members", long="members", short="m", help="Expected member pod count (3 agents + 3 dbservers + 3 coordinators)")]
[arg("retries", long="retries", short="r", help="Readiness probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
deploy-cluster root_password namespace="prod" stack="" members="9" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-cluster"
    NS="{{ namespace }}"
    ROOT_PASSWORD={{ quote(root_password) }}

    if [[ -z "$ROOT_PASSWORD" ]]; then
        echo "Error: --root-password is required; this recipe never invents a root password." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    echo "Deploying $FOLDER (stack '$STACK') into namespace '$NS'..."
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi set-secret --folder "$FOLDER" --stack "$STACK" \
        --key properties.secret.password --value "$ROOT_PASSWORD"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just arangodb _wait-ready --namespace "$NS" \
        --selector arango_deployment=arangodb \
        --count "{{ members }}" --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    echo "Coordinator Services in $NS:"
    kubectl get svc -n "$NS" -l arango_deployment=arangodb

# Create the application user and the seven logical databases, then wait.
# One command for: ensure-stack, user+password secrets, preview, apply, Job wait.
# Usage: just arangodb create-databases --app-user <user> --app-password <pw> [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("app_user", long="app-user", short="u", help="Application database user (required; stored encrypted)")]
[arg("app_password", long="app-password", short="p", help="Application user password (required; stored encrypted)")]
[arg("namespace", long="namespace", short="n", help="Namespace the Job runs in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
create-databases app_user app_password namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="create-arangodb-databases"
    NS={{ quote(namespace) }}
    APP_USER={{ quote(app_user) }}
    APP_PASSWORD={{ quote(app_password) }}

    if [[ -z "$APP_USER" || -z "$APP_PASSWORD" ]]; then
        echo "Error: --app-user and --app-password are both required." >&2
        exit 1
    fi
    if [[ "$APP_USER" == "root" ]]; then
        echo "Error: --app-user must not be the root administrator." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    STACK_CONFIG="$FOLDER/Pulumi.${STACK}.yaml"
    if [[ ! -f "$STACK_CONFIG" ]]; then
        echo "Error: stack config '$STACK_CONFIG' is missing." >&2
        exit 1
    fi
    SECRET_NAME=$(yq -r '.config."create-arangodb-databases:properties".arangodbSecret.name // "backend"' "$STACK_CONFIG")
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: target namespace '$NS' does not exist." >&2
        exit 1
    fi
    if ! kubectl get secret arangodb-pass -n "$NS" -o json | jq -e '.data.password != null' >/dev/null; then
        echo "Error: admin Secret arangodb-pass is missing its password key in '$NS'." >&2
        exit 1
    fi
    if [[ -z "${PULUMI_GCP_CREDENTIALS:-}" || ! -r "$PULUMI_GCP_CREDENTIALS" ]]; then
        echo "Error: PULUMI_GCP_CREDENTIALS must point to a readable credential file." >&2
        exit 1
    fi

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just arangodb _refresh-before-job --folder "$FOLDER" --namespace "$NS" \
        --selector app=arangodb-create-databases --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.createDatabases=true" \
        --plaintext "$FOLDER:properties.runId=$RUN_ID" \
        --secret "$FOLDER:properties.arangodbSecret.user=$APP_USER" \
        --secret "$FOLDER:properties.arangodbSecret.pass=$APP_PASSWORD"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    JOB="${SECRET_NAME}-create-databases-${RUN_ID}"
    just arangodb _wait-job --namespace "$NS" --job "$JOB" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    kubectl get secret -n "$NS" "$SECRET_NAME"
    echo "Job '$JOB' retained for logs; next run replaces it."

# Rotate the imported application user's password and update Secret `backend`.
# Re-applies rw grants but does not create databases; Pulumi-owned backend Secret
# stays in the existing create-arangodb-databases stack.
# Usage: just arangodb configure-app-credentials --app-user <user> --app-password <new-destination-password> [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("app_user", long="app-user", short="u", help="Imported application username to keep")]
[arg("app_password", long="app-password", short="p", help="New destination-only application password (stored encrypted)")]
[arg("namespace", long="namespace", short="n", help="Namespace the Job runs in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
configure-app-credentials app_user app_password namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="create-arangodb-databases"
    NS={{ quote(namespace) }}
    APP_USER={{ quote(app_user) }}
    APP_PASSWORD={{ quote(app_password) }}

    if [[ -z "$APP_USER" || -z "$APP_PASSWORD" ]]; then
        echo "Error: --app-user and --app-password are both required." >&2
        exit 1
    fi
    if [[ "$APP_USER" == "root" ]]; then
        echo "Error: --app-user must not be the root administrator." >&2
        exit 1
    fi
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: target namespace '$NS' does not exist." >&2
        exit 1
    fi
    if ! kubectl get secret arangodb-pass -n "$NS" -o json | jq -e '.data.password != null' >/dev/null; then
        echo "Error: admin Secret arangodb-pass is missing its password key in '$NS'." >&2
        exit 1
    fi
    if [[ -z "${PULUMI_GCP_CREDENTIALS:-}" || ! -r "$PULUMI_GCP_CREDENTIALS" ]]; then
        echo "Error: PULUMI_GCP_CREDENTIALS must point to a readable credential file." >&2
        exit 1
    fi
    ROOT_PASSWORD=$(kubectl get secret arangodb-pass -n "$NS" -o jsonpath='{.data.password}' | base64 -d)
    if [[ -z "$ROOT_PASSWORD" || "$APP_PASSWORD" == "$ROOT_PASSWORD" ]]; then
        echo "Error: app password must be non-empty and different from the root password." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    STACK_CONFIG="$FOLDER/Pulumi.${STACK}.yaml"
    if [[ ! -f "$STACK_CONFIG" ]]; then
        echo "Error: stack config '$STACK_CONFIG' is missing." >&2
        exit 1
    fi
    SECRET_NAME=$(yq -r '.config."create-arangodb-databases:properties".arangodbSecret.name // "backend"' "$STACK_CONFIG")
    if [[ -z "$SECRET_NAME" ]]; then
        echo "Error: app Secret name is missing from '$STACK_CONFIG'." >&2
        exit 1
    fi
    DATABASES=$(yq -r '.config."create-arangodb-databases:properties".databases[]' "$STACK_CONFIG")
    if [[ -z "$DATABASES" ]]; then
        echo "Error: no app databases are configured in '$STACK_CONFIG'." >&2
        exit 1
    fi
    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just arangodb _refresh-before-job --folder "$FOLDER" --namespace "$NS" \
        --selector app=arangodb-create-databases --stack "$STACK"

    RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.createDatabases=false" \
        --plaintext "$FOLDER:properties.runId=$RUN_ID" \
        --secret "$FOLDER:properties.arangodbSecret.user=$APP_USER" \
        --secret "$FOLDER:properties.arangodbSecret.pass=$APP_PASSWORD"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    JOB="${SECRET_NAME}-configure-app-credentials-${RUN_ID}"
    just arangodb _wait-job --namespace "$NS" --job "$JOB" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    kubectl get secret -n "$NS" "$SECRET_NAME"
    echo "Application password updated; database creation disabled, grants re-applied."
    echo "Job '$JOB' retained for logs; next run replaces it."

# Verify destination application credentials can connect to every configured DB.
# Runs short-lived `arangosh` pods with values read from Secret `backend`.
# Usage: just arangodb verify-app-credentials [--namespace <ns>] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Namespace holding ArangoDB and Secret backend")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[no-cd]
verify-app-credentials namespace="prod" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    CONFIG="create-arangodb-databases/Pulumi.${STACK}.yaml"
    CLUSTER_CONFIG="arangodb-cluster/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CONFIG" || ! -f "$CLUSTER_CONFIG" ]]; then
        echo "Error: stack config is missing for '$STACK'." >&2
        exit 1
    fi
    SECRET_NAME=$(yq -r '.config."create-arangodb-databases:properties".arangodbSecret.name' "$CONFIG")
    USER_KEY=$(yq -r '.config."create-arangodb-databases:properties".arangodbSecret.userkey' "$CONFIG")
    PASS_KEY=$(yq -r '.config."create-arangodb-databases:properties".arangodbSecret.passkey' "$CONFIG")
    ARANGO_VERSION=$(yq -r '.config."arangodb-cluster:properties".version' "$CLUSTER_CONFIG")
    DATABASES=$(yq -r '.config."create-arangodb-databases:properties".databases[]' "$CONFIG")
    if [[ -z "$SECRET_NAME" || -z "$USER_KEY" || -z "$PASS_KEY" || -z "$ARANGO_VERSION" || -z "$DATABASES" ]]; then
        echo "Error: app Secret keys, ArangoDB version, or database list missing from stack config." >&2
        exit 1
    fi
    kubectl get secret "$SECRET_NAME" -n "$NS" -o json | jq -e --arg user "$USER_KEY" --arg pass "$PASS_KEY" '.data[$user] != null and .data[$pass] != null' >/dev/null

    while IFS= read -r DB; do
        [[ -z "$DB" ]] && continue
        POD_DB="${DB//_/-}"
        POD="arango-app-check-${POD_DB}-$(date -u +%s)-$$"
        OVERRIDES=$(jq -nc \
            --arg name "$POD" --arg image "arangodb:${ARANGO_VERSION}" \
            --arg secret "$SECRET_NAME" --arg user_key "$USER_KEY" \
            --arg pass_key "$PASS_KEY" --arg db "$DB" \
            '{spec:{containers:[{name:$name,image:$image,command:["arangosh"],
              args:["--server.endpoint","http+tcp://arangodb:8529",
                    "--server.username","$(APP_USER)",
                    "--server.password","$(APP_PASSWORD)",
                    "--server.database",$db,
                    "--javascript.execute-string","db._query(\"RETURN 1\").toArray(); print(\"application access verified\");"],
              env:[{name:"APP_USER",valueFrom:{secretKeyRef:{name:$secret,key:$user_key}}},
                   {name:"APP_PASSWORD",valueFrom:{secretKeyRef:{name:$secret,key:$pass_key}}}]}],
              restartPolicy:"Never"}}')
        echo "Verifying app access to database '$DB'..."
        kubectl run "$POD" --rm -i --restart=Never -n "$NS" \
            --image "arangodb:${ARANGO_VERSION}" --overrides="$OVERRIDES"
    done <<< "$DATABASES"
    echo "Application credentials verified on all configured databases."

# Internal: run one arangosh expression against the coordinator in a
# short-lived pod and print its stdout. Root credentials come from Secret
# `arangodb-pass` (same wiring as reset-root-password), and the image tag is
# derived from the destination cluster's stack config so the probe always
# speaks this cluster's ArangoDB version. Callers interpret the printed
# DATABASE_* sentinel; this recipe only guarantees the probe itself ran.
# Usage: just arangodb _arangosh-probe --namespace <ns> --server <svc> --connect-database <db> --stack <name> --javascript <expr>
[arg("namespace", long="namespace", short="n", help="Namespace holding ArangoDB and Secret arangodb-pass")]
[arg("server", long="server", short="v", help="Coordinator Service name")]
[arg("connect_database", long="connect-database", short="d", help="Database to log into (usually _system)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("javascript", long="javascript", short="j", help="arangosh --javascript.execute-string expression")]
[group('arangodb')]
[private]
[no-cd]
_arangosh-probe namespace server connect_database stack javascript:
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    CONNECT_DB={{ quote(connect_database) }}
    JAVASCRIPT={{ quote(javascript) }}
    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})

    if [[ ! "$CONNECT_DB" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: connect database '$CONNECT_DB' is not a valid ArangoDB database name." >&2
        exit 1
    fi

    CLUSTER_CONFIG="arangodb-cluster/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CLUSTER_CONFIG" ]]; then
        echo "Error: $CLUSTER_CONFIG is missing; cannot resolve the destination ArangoDB version." >&2
        exit 1
    fi
    ARANGO_VERSION=$(yq -r '.config."arangodb-cluster:properties".version' "$CLUSTER_CONFIG")
    if [[ -z "$ARANGO_VERSION" || "$ARANGO_VERSION" == "null" ]]; then
        echo "Error: arangodb-cluster:properties.version is not set in $CLUSTER_CONFIG." >&2
        exit 1
    fi

    kubectl get secret arangodb-pass -n "$NS" -o json | jq -e '.data.password' >/dev/null

    POD="arangosh-probe-$(date -u +%s)-$$"
    # $(ARANGO_PASSWORD) is expanded by Kubernetes from the pod's own env,
    # exactly as verify-app-credentials does it.
    OVERRIDES=$(jq -nc \
        --arg name "$POD" --arg image "arangodb:${ARANGO_VERSION}" \
        --arg endpoint "http+tcp://${SERVER}:8529" --arg db "$CONNECT_DB" \
        --arg js "$JAVASCRIPT" \
        '{spec:{containers:[{name:$name,image:$image,command:["arangosh"],
          args:["--server.endpoint",$endpoint,
                "--server.username","root",
                "--server.password","$(ARANGO_PASSWORD)",
                "--server.database",$db,
                "--javascript.execute-string",$js],
          env:[{name:"ARANGO_PASSWORD",valueFrom:{secretKeyRef:{name:"arangodb-pass",key:"password"}}}]}],
          restartPolicy:"Never"}}')

    kubectl run "$POD" --rm -i --restart=Never -n "$NS" \
        --image "arangodb:${ARANGO_VERSION}" --overrides="$OVERRIDES"

# Internal: does one database exist on the target coordinator? The exit status
# is the contract callers rely on: 0 = exists, 3 = definitively absent,
# anything else = the probe itself failed (callers must abort rather than
# treat an unreachable cluster as "absent").
# Usage: just arangodb _database-exists --namespace <ns> --server <svc> --database <db> --stack <name>
[arg("namespace", long="namespace", short="n", help="Namespace holding ArangoDB and Secret arangodb-pass")]
[arg("server", long="server", short="v", help="Coordinator Service name")]
[arg("database", long="database", short="d", help="Database to look for")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[private]
[no-cd]
_database-exists namespace server database stack:
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    DB={{ quote(database) }}

    if [[ ! "$DB" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: database '$DB' is not a valid ArangoDB database name." >&2
        exit 1
    fi

    JS="const dbs = db._databases(); print(dbs.indexOf(\"${DB}\") === -1 ? \"DATABASE_MISSING\" : \"DATABASE_PRESENT\");"
    probe_rc=0
    OUT=$(just arangodb _arangosh-probe \
        --namespace "$NS" --server "$SERVER" \
        --connect-database _system --stack {{ quote(stack) }} \
        --javascript "$JS") || probe_rc=$?
    if [[ "$probe_rc" -ne 0 ]]; then
        echo "Error: arangosh probe failed (exit $probe_rc) while checking whether '$DB' exists on '$SERVER'." >&2
        exit 1
    fi
    if grep -q "DATABASE_PRESENT" <<<"$OUT"; then
        echo "Database '$DB' exists on $SERVER."
        exit 0
    fi
    if grep -q "DATABASE_MISSING" <<<"$OUT"; then
        echo "Database '$DB' does not exist on $SERVER."
        exit 3
    fi
    echo "Error: arangosh probe did not report database presence. Raw output:" >&2
    printf '%s\n' "$OUT" >&2
    exit 1

# Internal: post-restore assertion that one database exists and holds at
# least one non-system collection, so a restore that produced an empty
# database fails loudly instead of being reported as success.
# Exit status: 0 = verified, 4 = database exists but has no non-system
# collection, anything else = the probe itself failed.
# Usage: just arangodb _database-has-collections --namespace <ns> --server <svc> --database <db> --stack <name>
[arg("namespace", long="namespace", short="n", help="Namespace holding ArangoDB and Secret arangodb-pass")]
[arg("server", long="server", short="v", help="Coordinator Service name")]
[arg("database", long="database", short="d", help="Database to verify")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[private]
[no-cd]
_database-has-collections namespace server database stack:
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SERVER={{ quote(server) }}
    DB={{ quote(database) }}

    if [[ ! "$DB" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: database '$DB' is not a valid ArangoDB database name." >&2
        exit 1
    fi

    JS="const cols = db._collections().filter(function (c) { return c.name().charAt(0) !== \"_\"; }); print(cols.length === 0 ? \"DATABASE_EMPTY\" : \"DATABASE_OK \" + cols.length);"
    probe_rc=0
    OUT=$(just arangodb _arangosh-probe \
        --namespace "$NS" --server "$SERVER" \
        --connect-database "$DB" --stack {{ quote(stack) }} \
        --javascript "$JS") || probe_rc=$?
    if [[ "$probe_rc" -ne 0 ]]; then
        echo "Error: could not open database '$DB' on '$SERVER' (arangosh exit $probe_rc) — the restore may not have created it." >&2
        exit 1
    fi
    if grep -q "DATABASE_EMPTY" <<<"$OUT"; then
        echo "Error: database '$DB' exists but has no non-system collection — the restore brought no data." >&2
        exit 4
    fi
    COUNT=$(grep -o "DATABASE_OK [0-9]*" <<<"$OUT" | head -n1 | awk '{print $2}')
    if [[ -z "$COUNT" ]]; then
        echo "Error: arangosh probe did not report a collection count. Raw output:" >&2
        printf '%s\n' "$OUT" >&2
        exit 1
    fi
    echo "Verified: database '$DB' exists on $SERVER with $COUNT non-system collection(s)."

# Reset the ArangoDB root password to the value in the destination cluster's
# 'arangodb-pass' secret. Required after a cross-project bootstrap restores
# the source's _users collection, which brings the source's root password.
# Usage: just arangodb reset-root-password [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace (default: prod)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts (default 60)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
reset-root-password namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="reset-root-password"
    NS={{ quote(namespace) }}

    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: target namespace '$NS' does not exist." >&2
        exit 1
    fi
    if ! kubectl get secret arangodb-pass -n "$NS" -o json | jq -e '.data.password != null' >/dev/null || \
       ! kubectl get secret arangodb-jwt -n "$NS" -o json | jq -e '.data.token != null' >/dev/null; then
        echo "Error: arangodb-pass.password or arangodb-jwt.token is missing in '$NS'." >&2
        exit 1
    fi
    if [[ -z "${PULUMI_GCP_CREDENTIALS:-}" || ! -r "$PULUMI_GCP_CREDENTIALS" ]]; then
        echo "Error: PULUMI_GCP_CREDENTIALS must point to a readable credential file." >&2
        exit 1
    fi

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just arangodb _refresh-before-job --folder "$FOLDER" --namespace "$NS" \
        --selector app=arangodb-reset-root-password --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    RUN_ID="$(date -u +%Y%m%d%H%M%S)-$$"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.runId=$RUN_ID"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    JOB="arangodb-reset-root-password-${RUN_ID}"
    just arangodb _wait-job --namespace "$NS" --job "$JOB" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    echo "Root password reset log:"
    kubectl logs -n "$NS" "job/$JOB" --tail=20 || \
        echo "Job logs unavailable — check root login before proceeding."
    echo "Job '$JOB' retained for logs; next run replaces it."

# Complete the required post-import setup in one rerunnable action:
# reset restore config, reset root, rotate the imported app user's password,
# then verify app access against every configured database.
# Usage: just arangodb finalize-bootstrap --app-user <existing-user> --app-password <new-destination-password> [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("app_user", long="app-user", short="u", help="Imported application username to keep")]
[arg("app_password", long="app-password", short="p", help="New destination-only application password (stored encrypted)")]
[arg("namespace", long="namespace", short="n", help="Production destination namespace (currently prod)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 60)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
finalize-bootstrap app_user app_password namespace="prod" stack="" retries="60" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    APP_USER={{ quote(app_user) }}
    APP_PASSWORD={{ quote(app_password) }}
    NS={{ quote(namespace) }}

    # Preflight the entire workflow before the first Pulumi config mutation.
    if [[ -z "$APP_USER" || -z "$APP_PASSWORD" ]]; then
        echo "Error: --app-user and --app-password are both required." >&2
        exit 1
    fi
    if [[ "$APP_USER" == "root" ]]; then
        echo "Error: --app-user must not be the root administrator." >&2
        exit 1
    fi
    if [[ "$NS" != "prod" ]]; then
        echo "Error: finalize-bootstrap currently supports the prod destination only." >&2
        exit 1
    fi
    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    if [[ ! -r "${PULUMI_GCP_CREDENTIALS:-}" ]]; then
        echo "Error: PULUMI_GCP_CREDENTIALS must point to a readable credential file." >&2
        exit 1
    fi
    if ! command -v yq >/dev/null 2>&1; then
        echo "Error: yq is required to validate stack configuration." >&2
        exit 1
    fi
    for folder in arangodb-restore reset-root-password create-arangodb-databases arangodb-cluster; do
        if [[ ! -f "$folder/Pulumi.${STACK}.yaml" ]]; then
            echo "Error: missing stack config $folder/Pulumi.${STACK}.yaml." >&2
            exit 1
        fi
    done
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: destination namespace '$NS' does not exist." >&2
        exit 1
    fi
    kubectl get secret arangodb-pass -n "$NS" -o json | jq -e '.data.username and .data.password' >/dev/null
    ROOT_USER=$(kubectl get secret arangodb-pass -n "$NS" -o jsonpath='{.data.username}' | base64 -d)
    ROOT_PASSWORD=$(kubectl get secret arangodb-pass -n "$NS" -o jsonpath='{.data.password}' | base64 -d)
    if [[ "$ROOT_USER" != "root" || -z "$ROOT_PASSWORD" ]]; then
        echo "Error: arangodb-pass must contain username=root and a non-empty password." >&2
        exit 1
    fi
    if [[ "$APP_PASSWORD" == "$ROOT_PASSWORD" ]]; then
        echo "Error: application password must differ from destination root password." >&2
        exit 1
    fi
    kubectl get secret arangodb-jwt -n "$NS" -o json | jq -e '.data.token | length > 0' >/dev/null
    kubectl get secret dictycr -n "$NS" >/dev/null
    restore_active=$(kubectl get jobs -n "$NS" -l app=arangodb-restore -o json | jq '[.items[]? | select((.status.active // 0) > 0)] | length')
    if (( restore_active > 0 )); then
        echo "Error: a bootstrap restore Job is still active in '$NS'; wait for import to finish before finalizing." >&2
        exit 1
    fi
    APP_CONFIG="create-arangodb-databases/Pulumi.${STACK}.yaml"
    DATABASES=$(yq -r '.config."create-arangodb-databases:properties".databases[]' "$APP_CONFIG")
    if [[ -z "$DATABASES" ]]; then
        echo "Error: no application databases configured in '$APP_CONFIG'." >&2
        exit 1
    fi
    for selector in app=arangodb-restore app=arangodb-reset-root-password app=arangodb-create-databases; do
        just arangodb _assert-no-running-jobs --namespace "$NS" --selector "$selector"
    done

    echo "1/4 Resetting restore stack to this cluster's own backup config..."
    just arangodb reset-restore-config --stack "$STACK"
    RESTORE_CONFIG="arangodb-restore/Pulumi.${STACK}.yaml"
    RESTORE_BUCKET=$(yq -r '.config."arangodb-restore:properties".bucket' "$RESTORE_CONFIG")
    RESTORE_SECRET=$(yq -r '.config."arangodb-restore:properties".resticSecret.name' "$RESTORE_CONFIG")
    RESTORE_BUCKET_SECRET=$(yq -r '.config."arangodb-restore:properties".bucketSecret.name' "$RESTORE_CONFIG")
    RESTORE_PROJECT_SECRET=$(yq -r '.config."arangodb-restore:properties".projectSecret.name' "$RESTORE_CONFIG")
    RESTORE_NO_LOCK=$(yq -r '.config."arangodb-restore:properties".noLock' "$RESTORE_CONFIG")
    if [[ "$RESTORE_BUCKET" != "restic-arangodb-backup-prod" || \
          "$RESTORE_SECRET" != "dictycr" || \
          "$RESTORE_BUCKET_SECRET" != "dictycr" || \
          "$RESTORE_PROJECT_SECRET" != "dictycr" || \
          "$RESTORE_NO_LOCK" != "false" ]]; then
        echo "Error: restore stack did not reset to destination bucket/Secret/locking defaults." >&2
        exit 1
    fi

    echo "2/4 Resetting root password to destination arangodb-pass..."
    just arangodb reset-root-password --namespace "$NS" --stack "$STACK" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo "3/4 Rotating app password and refreshing backend Secret..."
    just arangodb configure-app-credentials \
        --app-user "$APP_USER" --app-password "$APP_PASSWORD" \
        --namespace "$NS" --stack "$STACK" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo "4/4 Verifying app login and database access..."
    just arangodb verify-app-credentials --namespace "$NS" --stack "$STACK"
    echo "Post-import setup complete. Next: just arangodb deploy-backup, then just arangodb verify."

# Create the backup writer service account in THIS cluster's GCP project and
# mint credentials/<project-id>/backup-gcs-sa.json — the key file
# configure-backup-secrets reads at pulumi up time. Grants
# roles/storage.objectAdmin pinned to the backup bucket by an IAM condition,
# so it works BEFORE deploy-backup creates the bucket. Run once per project;
# safe to re-run (key creation is skipped when the file already exists).
# Usage: just arangodb setup-backup-sa [--bucket <name>] [--project <id>] [--sa-name <n>] [--key-file <path>]
[arg("bucket", long="bucket", short="b", help="Backup GCS bucket the key gets write access to")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<project>/<sa-name>.json)")]
[group('arangodb')]
[no-cd]
setup-backup-sa bucket="restic-arangodb-backup-prod" project="" sa_name="backup-gcs-sa" key_file="":
    just arangodb _setup-backup-sa \
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
_setup-backup-sa bucket="restic-arangodb-backup-prod" project="" sa_name="backup-gcs-sa" key_file="":
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
            --display-name "ArangoDB backup GCS writer"
    fi

    # Least privilege, ordering-safe: a project-level binding with an IAM
    # condition pinning it to the backup bucket. A plain bucket-level binding
    # (like grant-source-bucket-reader uses) is impossible here —
    # deploy-backup creates the bucket AFTER this key must already exist.
    # Additive and idempotent on re-run.
    gcloud projects add-iam-policy-binding "$PROJECT" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectAdmin \
        --condition="expression=resource.name.startsWith(\"projects/_/buckets/${BUCKET}\"),title=arangodb-backup-bucket-writer,description=Object admin limited to the ArangoDB restic backup bucket"

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
    echo "Next: just arangodb configure-backup-secrets --restic-password '<restic-pass>'"

# Create the shared `dictycr` backup Secret. Creates no namespaces — the
# namespace-bootstrap stack owns prod/operators (just gcp-pulumi
# apply-namespaces, docs/pulumi-setup.md §5).
# One command for: ensure SA + key, ensure-stack, all four secret values,
# preview, apply, verify. Apply this FIRST — restore and backup jobs read the
# Secret at apply time.
# Usage: just arangodb configure-backup-secrets --restic-password <pw> [--no-setup-sa] [--gcs-project <id>] [--gcs-key-file <path>] [--key-name <k>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="restic repository password (required)")]
[arg("setup_sa", long="setup-sa", help="Create/refresh the backup-gcs-sa service account + key first (disable with --no-setup-sa when pointing at your own key)")]
[arg("gcs_project", long="gcs-project", short="g", help="GCP project id that owns the backup bucket (defaults to PROJECT_ID from the cluster env)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to a GCS-capable service account JSON key (defaults to credentials/<project-id>/backup-gcs-sa.json; read at pulumi up time)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("namespace", long="namespace", short="n", help="Optional guard: must equal the namespace-bootstrap appNamespace export (default: no override)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[no-cd]
configure-backup-secrets restic_password setup_sa="true" gcs_project="" gcs_key_file="" key_name="gcsCredentials" namespace="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="backup_secrets"
    NS="{{ namespace }}"
    RESTIC_PASSWORD={{ quote(restic_password) }}
    SETUP_SA="{{ setup_sa }}"
    GCS_PROJECT="{{ gcs_project }}"
    KEY_FILE="{{ gcs_key_file }}"
    KEY_NAME="{{ key_name }}"

    # Both GCS arguments default off the active cluster env: `just cluster-env`
    # sources .env.<env>.<cluster>, which exports PROJECT_ID.
    [[ -z "$GCS_PROJECT" ]] && GCS_PROJECT="${PROJECT_ID:-}"
    [[ -z "$KEY_FILE" && -n "$GCS_PROJECT" ]] && KEY_FILE="credentials/${GCS_PROJECT}/backup-gcs-sa.json"

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
    DEFAULT_KEY_FILE="credentials/${GCS_PROJECT}/backup-gcs-sa.json"
    if [[ "$SETUP_SA" == "true" && "$KEY_FILE" == "$DEFAULT_KEY_FILE" ]]; then
        just arangodb _setup-backup-sa --project "$GCS_PROJECT"
    elif [[ "$SETUP_SA" == "true" ]]; then
        echo "Custom --gcs-key-file given — skipping SA setup; verifying the key file exists."
    fi

    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Error: service account key '$KEY_FILE' does not exist on this machine." >&2
        exit 1
    fi
    # Absolute path: backup_secrets/main.go reads this file with os.ReadFile at
    # `pulumi up` time, and pulumi -C changes the working directory, so a
    # relative path would resolve against the project dir, not your shell.
    KEY_FILE=$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    # The Secret's namespace is NOT configured here — the backup_secrets
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

    SECRET_NAME=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path properties.secret.name 2>/dev/null || echo "dictycr")

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

# Create Secret `dictycr-source`: the READ-ONLY identity for the cross-project
# first load (docs/arangodb-deploy.md §4). Creates no namespaces — `prod` must
# already exist from namespace-bootstrap (just gcp-pulumi apply-namespaces).
# Print source app username from Secret `backend`, without revealing password.
# Run inside the SOURCE cluster's active `just cluster-env` shell.
# Usage: just arangodb source-app-user --namespace <ns> [--secret backend]
[arg("namespace", long="namespace", short="n", help="Namespace containing the source application Secret")]
[arg("secret", long="secret", short="s", help="Secret name (default backend)")]
[group('arangodb')]
[no-cd]
source-app-user namespace secret="backend":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    SECRET={{ quote(secret) }}
    if [[ -z "$NS" || -z "$SECRET" ]]; then
        echo "Error: --namespace and --secret must be non-empty." >&2
        exit 1
    fi
    if [[ -z "${CLUSTER_ENV:-}" || -z "${CLUSTER_NAME:-}" || \
          -z "${KUBECONFIG:-}" || ! -r "$KUBECONFIG" ]]; then
        echo "Error: enter the source cluster with 'just cluster-env' before reading its app username." >&2
        exit 1
    fi
    if ! secret_json=$(kubectl get secret "$SECRET" -n "$NS" -o json 2>&1); then
        echo "Error: unable to read Secret '$SECRET' in namespace '$NS': $secret_json" >&2
        exit 1
    fi
    if ! user_b64=$(printf '%s\n' "$secret_json" | jq -er '.data.user | strings' 2>/dev/null); then
        echo "Error: Secret '$SECRET' in '$NS' is missing a valid 'user' key." >&2
        exit 1
    fi
    if [[ -z "$user_b64" ]]; then
        echo "Error: Secret '$SECRET' in '$NS' has an empty 'user' value." >&2
        exit 1
    fi
    if ! app_user=$(printf '%s' "$user_b64" | base64 -d 2>/dev/null) || [[ -z "$app_user" ]]; then
        echo "Error: Secret '$SECRET' in '$NS' has an invalid or empty 'user' value." >&2
        exit 1
    fi
    printf '%s\n' "$app_user"

# Print a Secret value from the active (source) cluster environment.
# Defaults to the restic repository password in Secret `dictycr` — the value
# `configure-source-secrets` needs when importing from this cluster.
# Prints the plain value; never logs or echoes it elsewhere.
# Usage: just arangodb source-secret-value --namespace <ns> [--secret dictycr] [--key resticPass]
[arg("namespace", long="namespace", short="n", help="Namespace containing the Secret")]
[arg("secret", long="secret", short="s", help="Secret name (default dictycr)")]
[arg("key", long="key", short="k", help="Secret data key (default resticPass)")]
[group('arangodb')]
[no-cd]
source-secret-value namespace secret="dictycr" key="resticPass":
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
    if [[ -z "$value_b64" ]]; then
        echo "Error: Secret '$SECRET' in '$NS' has an empty '$KEY' value." >&2
        exit 1
    fi
    if ! value=$(printf '%s' "$value_b64" | base64 -d 2>/dev/null) || [[ -z "$value" ]]; then
        echo "Error: Secret '$SECRET' in '$NS' has an invalid or empty '$KEY' value." >&2
        exit 1
    fi
    printf '%s\n' "$value"

# Usage: just arangodb configure-source-secrets --restic-password <pw> --gcs-project <source-id> --gcs-key-file <path> [--key-name <k>] [--secret-name <n>] [--namespace <ns>] [--stack <name>]
[arg("restic_password", long="restic-password", short="p", help="SOURCE restic repository password (required; may differ from the dictycr one)")]
[arg("gcs_project", long="gcs-project", short="g", help="SOURCE GCP project id that owns the source bucket (required; NOT this cluster's project)")]
[arg("gcs_key_file", long="gcs-key-file", short="f", help="Path to the SOURCE service account JSON key with objectViewer on the source bucket (required)")]
[arg("key_name", long="key-name", short="k", help="Data key the JSON is stored under inside the Secret")]
[arg("secret_name", long="secret-name", short="e", help="Name of the source Secret")]
[arg("namespace", long="namespace", short="n", help="Namespace the Secret is created in (must already exist)")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[no-cd]
configure-source-secrets restic_password gcs_project gcs_key_file key_name="gcsCredentials" secret_name="dictycr-source" namespace="prod" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="source_backup_secrets"
    NS="{{ namespace }}"
    RESTIC_PASSWORD={{ quote(restic_password) }}
    GCS_PROJECT="{{ gcs_project }}"
    KEY_FILE="{{ gcs_key_file }}"
    KEY_NAME="{{ key_name }}"
    SECRET_NAME="{{ secret_name }}"

    if [[ -z "$RESTIC_PASSWORD" || -z "$GCS_PROJECT" || -z "$KEY_FILE" ]]; then
        echo "Error: --restic-password, --gcs-project and --gcs-key-file are all required." >&2
        exit 1
    fi
    if [[ ! -f "$KEY_FILE" ]]; then
        echo "Error: source service account key '$KEY_FILE' does not exist on this machine." >&2
        exit 1
    fi
    # Absolute path: source_backup_secrets/main.go reads this file with
    # os.ReadFile at `pulumi up` time, and `pulumi -C` changes the working
    # directory, so a relative path would resolve against the project dir.
    KEY_FILE=$(cd "$(dirname "$KEY_FILE")" && pwd)/$(basename "$KEY_FILE")

    # This project creates no namespaces on purpose — backup_secrets owns them.
    if ! kubectl get namespace "$NS" >/dev/null 2>&1; then
        echo "Error: namespace '$NS' does not exist." >&2
        echo "Run 'just gcp-pulumi apply-namespaces' (pulumi setup §5) to create '$NS', then 'just arangodb configure-backup-secrets ...' for Secret dictycr." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "$FOLDER:properties.namespace=$NS" \
        --plaintext "$FOLDER:properties.secret.name=$SECRET_NAME" \
        --secret "$FOLDER:properties.secret.resticPass=$RESTIC_PASSWORD" \
        --secret "$FOLDER:properties.secret.gcsProject=$GCS_PROJECT" \
        --secret "$FOLDER:properties.secret.serviceAccount.keyname=$KEY_NAME" \
        --secret "$FOLDER:properties.secret.serviceAccount.filepath=$KEY_FILE"

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    echo
    kubectl get secret "$SECRET_NAME" -n "$NS"
    echo "Keys in $SECRET_NAME:"
    kubectl get secret "$SECRET_NAME" -n "$NS" -o json | jq -r '.data | keys[] | "  " + .'
    echo
    echo "Reminder: $SECRET_NAME is READ-ONLY bootstrap identity. Never point deploy-backup at it."

# Create a read-only service account in the SOURCE GCP project and grant it
# objectViewer on the source restic bucket. Only usable if you hold IAM admin
# on that project — otherwise ask its owner to run these three gcloud calls.
# --bucket defaults to the arangodb-backup stack's properties.bucket (source
# cluster env). --source-project defaults to PROJECT_ID. The bucket is
# verified to exist before any IAM change.
# Usage: just arangodb grant-source-bucket-reader [--bucket <name>] [--source-project <id>] [--sa-name <n>] [--key-file <path>] [--stack <name>]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (optional; inferred from the arangodb-backup stack config when omitted)")]
[arg("source_project", long="source-project", short="p", help="SOURCE GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="a", help="Service account short name to create/reuse")]
[arg("key_file", long="key-file", short="f", help="Where to write the JSON key (default credentials/<source-project>/<sa-name>.json)")]
[arg("stack", long="stack", short="s", help="Pulumi stack for bucket inference (defaults to PULUMI_STACK; only needed when --bucket is omitted)")]
[group('arangodb')]
[no-cd]
grant-source-bucket-reader bucket="" source_project="" sa_name="arangodb-restic-reader" key_file="" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    SOURCE_BUCKET="{{ bucket }}"
    SOURCE_PROJECT="{{ source_project }}"
    [ -z "${SOURCE_PROJECT}" ] && SOURCE_PROJECT="${PROJECT_ID:-}"
    SA_NAME="{{ sa_name }}"
    KEY_FILE="{{ key_file }}"

    if [[ -z "$SOURCE_PROJECT" ]]; then
        echo "Error: --source-project (or PROJECT_ID) is required." >&2
        exit 1
    fi

    # Inference fallback: the source bucket name is owned by this cluster's
    # arangodb-backup stack, so read it from that stack's config. Read the
    # local Pulumi.<stack>.yaml directly (no backend login needed); `pulumi
    # config get` is only a fallback because it insists on resolving the
    # cloud URL even for a purely local read. Explicit --bucket skips this
    # entirely (no cluster env required).
    if [[ -z "$SOURCE_BUCKET" ]]; then
        STACK=$(just arangodb _require-stack --stack "{{ stack }}") || exit 1
        STACK_YAML="arangodb-backup/Pulumi.${STACK}.yaml"
        if [[ -f "$STACK_YAML" ]]; then
            SOURCE_BUCKET=$(yq -r '.config."arangodb-backup:properties".bucket' "$STACK_YAML" 2>/dev/null || true)
        fi
        if [[ -z "$SOURCE_BUCKET" ]] && command -v pulumi >/dev/null 2>&1; then
            SOURCE_BUCKET=$(pulumi -C arangodb-backup config get --stack "$STACK" --path properties.bucket 2>&1 || true)
            [[ "$SOURCE_BUCKET" == *"error:"* ]] && SOURCE_BUCKET=""
        fi
        if [[ -z "$SOURCE_BUCKET" ]]; then
            echo "Error: could not infer the source bucket from ${STACK_YAML} — pass --bucket explicitly, or deploy the backup first (just arangodb deploy-backup)." >&2
            exit 1
        fi
        echo "Inferred source bucket from arangodb-backup stack '$STACK': $SOURCE_BUCKET"
    fi

    # Preflight: verify the bucket exists BEFORE any IAM mutation, so a typo
    # cannot grant objectViewer on the wrong project's namespace.
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

    # Idempotent: probe first so a re-run does not spew a conflict ERROR.
    if gcloud iam service-accounts describe "$SA_EMAIL" --project "$SOURCE_PROJECT" >/dev/null 2>&1; then
        echo "Service account already exists — continuing."
    else
        gcloud iam service-accounts create "$SA_NAME" \
            --project "$SOURCE_PROJECT" \
            --display-name "ArangoDB restic cross-project reader"
    fi

    # Least privilege: BUCKET-level objectViewer (list+get). Never grant
    # project-wide storage.admin, and never grant write on the source project.
    # Additive binding, so re-running is idempotent.
    gcloud storage buckets add-iam-policy-binding "gs://${SOURCE_BUCKET}" \
        --member "serviceAccount:${SA_EMAIL}" \
        --role roles/storage.objectViewer \
        --project "$SOURCE_PROJECT"

    # Key creation is NOT idempotent — every run mints a new key and old ones
    # keep working until deleted. Reuse an existing key file when present.
    if [[ -f "$KEY_FILE" ]]; then
        echo
        echo "Key file '$KEY_FILE' already exists — NOT creating another key."
        echo "Delete it first if you really want to mint a new one, and prune the old key with:"
        echo "  gcloud iam service-accounts keys list --iam-account $SA_EMAIL --project $SOURCE_PROJECT"
    else
        mkdir -p "$(dirname "$KEY_FILE")"
        gcloud iam service-accounts keys create "$KEY_FILE" \
            --iam-account "$SA_EMAIL" \
            --project "$SOURCE_PROJECT"
        echo "Warning: service account keys accumulate. Audit with 'keys list' and delete unused ones."
    fi

    echo
    echo "Next: just arangodb configure-source-secrets --restic-password '<source pw>' --gcs-project '$SOURCE_PROJECT' --gcs-key-file '$KEY_FILE'"

# Deploy the backup bucket, CronJob and immediate Job, then wait for that Job.
# One command for: ensure-stack, preview, apply, Job wait, log tail, CronJob check.
# Usage: just arangodb deploy-backup [--namespace <ns>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Namespace the backup Job runs in")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Job probe attempts (default 90)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
deploy-backup namespace="prod" stack="" retries="90" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-backup"
    NS="{{ namespace }}"
    JOB="arangodb-backup-job"
    CRONJOB="arangodb-backup-cronjob"
    SOURCE_SECRET="dictycr-source"

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"

    # Pin the GCP project for the pulumi-gcp provider. Without it, the provider
    # falls back to the GOOGLE_CLOUD_PROJECT env var, which can carry a stale
    # value from another cluster env — bucket creates then target the wrong
    # project and fail 403 (same failure as cloudnative-pg-cluster before the
    # gcp:project pin). The env file also sets GOOGLE_CLOUD_PROJECT, but the
    # stack config wins and is immune to shell leaks.
    PROJECT="${PROJECT_ID:-}"
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no GCP project id — enter 'just cluster-env' (it exports PROJECT_ID) or pass --project." >&2
        exit 1
    fi
    just gcp-pulumi set-config --folder "$FOLDER" --stack "$STACK" \
        --key 'gcp:project' --value "$PROJECT"

    # Hard guard, not just documentation: `dictycr-source` is the read-only
    # identity for the FOREIGN source project. Backing up through it would
    # attempt writes into someone else's bucket with a credential that has no
    # write permission, and would point this cluster's backups at the wrong
    # repository. Ongoing backups must always use `dictycr`.
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    for key in resticSecret bucketSecret projectSecret; do
        configured=$(pulumi -C "$FOLDER" config get --stack "$STACK" --path "properties.${key}.name" 2>/dev/null || true)
        if [[ "$configured" == "$SOURCE_SECRET" ]]; then
            echo "Error: properties.${key}.name is '$SOURCE_SECRET' on $FOLDER stack '$STACK'." >&2
            echo "That Secret is the read-only cross-project BOOTSTRAP identity and must never drive backups." >&2
            echo "Fix: set it back to 'dictycr' (re-run configure-backup-secrets with the NEW project key if needed)." >&2
            exit 1
        fi
    done
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    just arangodb _wait-job --namespace "$NS" --job "$JOB" \
        --retries "{{ retries }}" --interval "{{ interval }}"

    echo
    echo "Backup log tail (restic summary should be at the end):"
    kubectl logs -n "$NS" "job/$JOB" --tail=30 || \
        echo "Job already removed by its 15-minute TTL — nothing left to tail."

    echo
    kubectl get cronjob -n "$NS" "$CRONJOB"

# Apply one of the optional loader projects (arangodb-dataloader,
# load-content-from-s3, load-uniprot-mapping).
# One command for: stack config check, ensure-stack, preview, apply, Job listing.
# Usage: just arangodb deploy-loader --folder <project> [--namespace <ns>] [--stack <name>]
[arg("folder", long="folder", short="f", help="Loader project folder (required)")]
[arg("namespace", long="namespace", short="n", help="Namespace whose Jobs are listed afterwards")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[no-cd]
deploy-loader folder namespace="prod" stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="{{ folder }}"
    NS="{{ namespace }}"

    if [[ ! -f "$FOLDER/Pulumi.yaml" ]]; then
        echo "Error: '$FOLDER' is not a Pulumi project (no Pulumi.yaml)." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    if [[ ! -f "$FOLDER/Pulumi.$STACK.yaml" ]]; then
        echo "Error: $FOLDER/Pulumi.$STACK.yaml does not exist." >&2
        echo "Loader projects have no production stack config in this repo yet — author it before deploying." >&2
        exit 1
    fi

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    echo
    kubectl get jobs -n "$NS"

# Run the restore Job that `configure-restore` set up, then follow it to the end.
# One command for: preview, apply, init-container wait, both container logs,
# Job completion, scratch PVC report. Every value is read back from the stack
# config, so it cannot drift from what configure-restore wrote.
# Usage: just arangodb apply-restore [--stack <name>] [--retries <n>] [--interval <s>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="i", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
apply-restore stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    RETRIES="{{ retries }}"
    INTERVAL="{{ interval }}"

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    read_cfg() {
        local key="$1"
        if ! pulumi -C "$FOLDER" config get --stack "$STACK" --path "properties.$key" 2>/dev/null; then
            echo "Error: properties.$key is not set on $FOLDER stack '$STACK'." >&2
            echo "Run: just arangodb configure-restore --namespace <ns>" >&2
            exit 1
        fi
    }

    NS=$(read_cfg namespace)
    RESTORE_ID=$(read_cfg restoreId)
    SERVER=$(read_cfg server)
    SNAPSHOT=$(read_cfg snapshot)
    JOB="arangodb-restore-${RESTORE_ID}"

    echo
    echo "Restoring into ${NS}/${SERVER} as job ${JOB} from snapshot '${SNAPSHOT}'."
    echo

    just gcp-pulumi preview --folder "$FOLDER" --stack "$STACK"
    just gcp-pulumi create-resource --folder "$FOLDER" --stack "$STACK"

    echo "Waiting for the restic-restore init container to finish..."
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

    echo
    echo "--- restic-restore log ---"
    kubectl logs -n "$NS" "job/$JOB" -c restic-restore --tail=50 || true

    just arangodb _wait-job --namespace "$NS" --job "$JOB" \
        --retries "$RETRIES" --interval "$INTERVAL"

    echo
    echo "--- arangorestore log ---"
    kubectl logs -n "$NS" "job/$JOB" -c arangorestore --tail=50 || true

    echo
    kubectl get pvc -n "$NS" -l type=arangodb-restore-scratch || true
    echo
    echo "Job and scratch PVC self-delete 1 hour after finishing (ttlSecondsAfterFinished: 3600)."
    echo "Destroy the stack when done inspecting: just gcp-pulumi remove-resource --folder $FOLDER --stack $STACK"

# Point the arangodb-restore stack at the SOURCE project's restic repository
# for a cross-project first load (docs/arangodb-deploy.md §4). Separate from
# configure-restore on purpose: this one targets the FOREIGN source bucket with
# the read-only identity and `noLock: true`. `--snapshot` is optional on both —
# omitted or `latest` resolves to the newest snapshot and echoes the resolved id.
# `--database` narrows the run to one database exactly like configure-restore:
# same read-only preflight, same refusal to touch an existing target database
# without `--overwrite yes`, same foreign-bucket teardown via reset-restore-config.
# Usage: just arangodb configure-bootstrap --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--database <db>] [--overwrite yes|no] [--stack <name>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id (optional; omit or 'latest' resolves to the newest snapshot in the source repo)")]
[arg("server", long="server", short="v", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("database", long="database", short="d", help="Import only this database (default: whole instance)")]
[arg("overwrite", long="overwrite", short="w", help="Replace an existing target database: yes|no (default no)")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[group('arangodb')]
[no-cd]
configure-bootstrap namespace bucket snapshot="" server="" restore_id="" secret="dictycr-source" stack="" database="" overwrite="no":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    NAMESPACE="{{ namespace }}"
    BUCKET="{{ bucket }}"
    SNAPSHOT="{{ snapshot }}"
    SERVER="{{ server }}"
    RESTORE_ID="{{ restore_id }}"
    SECRET="{{ secret }}"
    DATABASE="{{ database }}"
    OVERWRITE="{{ overwrite }}"

    # Same 63-char Job-name budget arangodb-restore/types.go enforces.
    MAX_RESTORE_ID_LEN=46

    if [[ -z "$NAMESPACE" ]]; then
        echo "Error: --namespace is required; there is no safe default restore target." >&2
        exit 1
    fi
    if [[ -z "$BUCKET" ]]; then
        echo "Error: --bucket is required; pass the SOURCE project's restic bucket." >&2
        exit 1
    fi

    # Snapshot is optional: omitted (or the literal "latest") resolves to the
    # newest snapshot in the SOURCE repository via a read-only restic pod run.
    # The resolved id is echoed and later recorded in the arangodb-restore
    # stack config, so the effective RPO stays auditable. An explicit id pins
    # the restore exactly as before.
    if [[ -z "$SNAPSHOT" || "$SNAPSHOT" == "latest" ]]; then
        SNAPSHOT=$(just arangodb _restic-snapshots-json \
            --namespace "$NAMESPACE" \
            --bucket "$BUCKET" \
            --secret "$SECRET" \
            | jq -r 'sort_by(.time) | last | .id')
        if [[ ! "$SNAPSHOT" =~ ^[a-f0-9]{64}$ ]]; then
            echo "Error: could not resolve the latest snapshot id — pass --snapshot explicitly." >&2
            exit 1
        fi
        echo "Resolved latest snapshot: $SNAPSHOT"
    elif [[ ! "$SNAPSHOT" =~ ^[a-f0-9]{8,64}$ ]]; then
        echo "Error: snapshot '$SNAPSHOT' is not a restic snapshot id (expected 8-64 lowercase hex chars, or omit / 'latest' for the newest)." >&2
        exit 1
    fi

    # Same single-database rules configure-restore and arangodb-restore/types.go
    # enforce: yes/no with true/false accepted, overwrite only with --database.
    case "$OVERWRITE" in
        yes|true) OVERWRITE_VALUE="true" ;;
        no|false|"") OVERWRITE_VALUE="false" ;;
        *)
            echo "Error: --overwrite takes yes or no; got '$OVERWRITE'." >&2
            exit 1
            ;;
    esac
    if [[ "$OVERWRITE_VALUE" == "true" && -z "$DATABASE" ]]; then
        echo "Error: --overwrite needs --database — overwriting only applies to a single-database import." >&2
        exit 1
    fi
    if [[ -n "$DATABASE" && ! "$DATABASE" =~ ^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$ ]]; then
        echo "Error: --database '$DATABASE' must be a valid ArangoDB database name: an ASCII letter or" >&2
        echo "       underscore, then letters, digits, underscores or hyphens, at most 64 chars." >&2
        exit 1
    fi

    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})

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
        RESTORE_ID="bootstrap-$(date -u +%Y%m%d-%H%M%S)"
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

    # --- single-database preflight (read-only, before any mutation) --------
    if [[ -n "$DATABASE" ]]; then
        just arangodb _preflight-database-restore \
            --namespace "$NAMESPACE" \
            --server "$SERVER" \
            --database "$DATABASE" \
            --bucket "$BUCKET" \
            --secret "$SECRET" \
            --snapshot "$SNAPSHOT" \
            --stack "$STACK" \
            --overwrite "$OVERWRITE"
    fi

    echo
    echo "arangodb-restore BOOTSTRAP configuration (cross-project first load)"
    echo "  namespace     : ${NAMESPACE}"
    echo "  server        : ${SERVER}"
    echo "  restoreId     : ${RESTORE_ID}"
    echo "  source bucket : ${BUCKET}"
    echo "  snapshot      : ${SNAPSHOT}"
    echo "  source secret : ${SECRET}"
    echo "  noLock        : true (read-only SA cannot write a restic lock)"
    echo "  confirmTarget : ${CONFIRM_TARGET}"
    echo "  job name      : arangodb-restore-${RESTORE_ID}"
    if [[ -n "$DATABASE" ]]; then
        echo "  database      : ${DATABASE} (single-database import)"
        echo "  overwrite     : ${OVERWRITE_VALUE}"
    else
        echo "  scope         : whole instance (all databases)"
    fi
    echo

    just gcp-pulumi ensure-stack --folder "$FOLDER" --stack "$STACK"
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    # Overlay on the DR template: target identity, the SOURCE bucket, the three
    # SOURCE secret NAMES (keys keep their dictycr-compatible names), noLock,
    # and — for a single-database import — the database/overwrite pair.
    SET_ARGS=(
        --path
        --plaintext "arangodb-restore:properties.namespace=$NAMESPACE"
        --plaintext "arangodb-restore:properties.server=$SERVER"
        --plaintext "arangodb-restore:properties.restoreId=$RESTORE_ID"
        --plaintext "arangodb-restore:properties.snapshot=$SNAPSHOT"
        --plaintext "arangodb-restore:properties.confirmTarget=$CONFIRM_TARGET"
        --plaintext "arangodb-restore:properties.bucket=$BUCKET"
        --plaintext "arangodb-restore:properties.resticSecret.name=$SECRET"
        --plaintext "arangodb-restore:properties.resticSecret.key=resticPass"
        --plaintext "arangodb-restore:properties.bucketSecret.name=$SECRET"
        --plaintext "arangodb-restore:properties.bucketSecret.key=gcsCredentials"
        --plaintext "arangodb-restore:properties.projectSecret.name=$SECRET"
        --plaintext "arangodb-restore:properties.projectSecret.key=gcsProject"
        --plaintext "arangodb-restore:properties.noLock=true"
    )
    if [[ -n "$DATABASE" ]]; then
        SET_ARGS+=(
            --plaintext "arangodb-restore:properties.database=$DATABASE"
            --plaintext "arangodb-restore:properties.overwrite=$OVERWRITE_VALUE"
        )
    fi
    pulumi -C "$FOLDER" config set-all --stack "$STACK" "${SET_ARGS[@]}"

    # Stale-value guard: a whole-instance first load must never inherit an
    # earlier single-database filter.
    if [[ -z "$DATABASE" ]]; then
        for key in database overwrite; do
            pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
        done
    fi

    echo
    echo "Bootstrap config written. Run 'just arangodb reset-restore-config' when done"
    echo "(bootstrap-from-snapshot does that for you, even on failure)."

# Put the arangodb-restore stack back on the DR defaults after a bootstrap:
# this cluster's own bucket + `dictycr`, locking re-enabled, target cleared.
# Without this a later DR drill would silently read the FOREIGN source bucket.
# Usage: just arangodb reset-restore-config [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[no-cd]
reset-restore-config stack="":
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    PROD_BUCKET="restic-arangodb-backup-prod"
    PROD_SECRET="dictycr"

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    pulumi -C "$FOLDER" config set-all --stack "$STACK" --path \
        --plaintext "arangodb-restore:properties.bucket=$PROD_BUCKET" \
        --plaintext "arangodb-restore:properties.resticSecret.name=$PROD_SECRET" \
        --plaintext "arangodb-restore:properties.bucketSecret.name=$PROD_SECRET" \
        --plaintext "arangodb-restore:properties.projectSecret.name=$PROD_SECRET" \
        --plaintext "arangodb-restore:properties.noLock=false"

    # Clearing these forces a fresh configure-restore / configure-bootstrap
    # next time instead of reusing a stale target identity.
    for key in snapshot restoreId confirmTarget database overwrite; do
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
    done

    echo
    echo "arangodb-restore reset to DR defaults:"
    echo "  bucket  : $PROD_BUCKET"
    echo "  secrets : $PROD_SECRET (restic/bucket/project)"
    echo "  noLock  : false"
    echo "  snapshot/restoreId/confirmTarget/database/overwrite cleared"

# Internal: narrow cleanup used by restore-database's EXIT trap — clears only
# the single-database keys and leaves bucket/secrets/noLock alone (that run
# never touched them). It runs from a trap, so it must stay best-effort: a
# failed config rm must not turn a completed restore into a failed one.
# Usage: just arangodb _reset-single-database-config --stack <name>
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[group('arangodb')]
[private]
[no-cd]
_reset-single-database-config stack:
    #!/usr/bin/env bash
    set -euo pipefail

    FOLDER="arangodb-restore"
    STACK=$(just arangodb _require-stack --stack {{ quote(stack) }})
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"

    for key in database overwrite; do
        pulumi -C "$FOLDER" config rm --stack "$STACK" --path "properties.$key" 2>/dev/null || true
    done
    echo "Cleared properties.database and properties.overwrite on the $FOLDER stack '$STACK'."

# One command for the cross-project first load: configure-bootstrap, then the
# existing apply-restore, then reset-restore-config via trap (so the stack is
# never left pointing at the foreign bucket, even if the restore fails).
# Usage: just arangodb bootstrap-from-snapshot --namespace <ns> --bucket <source-bucket> [--snapshot <id>] [--server <svc>] [--restore-id <id>] [--secret <name>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id (optional; defaults to the newest snapshot in the repo)")]
[arg("server", long="server", short="v", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: bootstrap-<UTC timestamp>)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
bootstrap-from-snapshot namespace bucket snapshot="" server="" restore_id="" secret="dictycr-source" stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    # Reset on EVERY exit path. A failed bootstrap that left the stack pointing
    # at the foreign source bucket is exactly how a later DR drill would read
    # the wrong repository, so the reset must not depend on success.
    trap 'just arangodb reset-restore-config --stack "$STACK" || true' EXIT

    just arangodb configure-bootstrap \
        --namespace "{{ namespace }}" \
        --bucket "{{ bucket }}" \
        --snapshot "{{ snapshot }}" \
        --server "{{ server }}" \
        --restore-id "{{ restore_id }}" \
        --secret "{{ secret }}" \
        --stack "$STACK"

    just arangodb apply-restore \
        --stack "$STACK" \
        --retries "{{ retries }}" \
        --interval "{{ interval }}"

    echo
    echo "Bootstrap restore finished. Next steps:"
    echo "  1. Spot-check a restored database has documents."
    echo "  2. just arangodb finalize-bootstrap --app-user '<existing-user>' --app-password '<new-destination-password>'"
    echo "  3. just arangodb deploy-backup (uses dictycr, never dictycr-source)."

# Restore ONE database into this cluster from its own backup bucket, at the
# snapshot's point in time. Wraps `configure-restore --database` (same
# read-only preflights, same confirmTarget gate) plus apply-restore, and clears
# the single-database keys from the stack on EVERY exit path — so a failed run
# cannot leave a later whole-instance DR drill quietly narrowed to one database.
# Usage: just arangodb restore-database --namespace <ns> --database <db> [--snapshot <id|latest>] [--overwrite yes|no] [--server <svc>] [--restore-id <id>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("database", long="database", short="d", help="Database to restore (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id, or 'latest' (default latest)")]
[arg("overwrite", long="overwrite", short="w", help="Replace an existing target database: yes|no (default no)")]
[arg("server", long="server", short="v", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: dbrestore-<UTC timestamp>)")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
restore-database namespace database snapshot="latest" overwrite="no" server="" restore_id="" stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    NAMESPACE="{{ namespace }}"
    DATABASE="{{ database }}"
    SNAPSHOT="{{ snapshot }}"
    OVERWRITE="{{ overwrite }}"
    SERVER="{{ server }}"
    RESTORE_ID="{{ restore_id }}"
    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    if [[ -z "$NAMESPACE" ]]; then
        echo "Error: --namespace is required; there is no safe default restore target." >&2
        exit 1
    fi
    if [[ -z "$DATABASE" ]]; then
        echo "Error: --database is required; use configure-restore + apply-restore for a whole-instance restore." >&2
        exit 1
    fi
    # An empty or `latest` --snapshot is pinned below, once the stack's own
    # bucket is known, and always BEFORE the preflight.
    if [[ -z "$RESTORE_ID" ]]; then
        RESTORE_ID="dbrestore-$(date -u +%Y%m%d-%H%M%S)"
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

    # This run reads THIS cluster's own bucket: the DR defaults in
    # arangodb-restore/Pulumi.<stack>.yaml (bucket + `dictycr`). A stack left
    # pointing at a foreign source bucket is a bug — configure-bootstrap's own
    # trap resets it, so seeing one here means reset-restore-config was skipped.
    CONFIG="arangodb-restore/Pulumi.${STACK}.yaml"
    if [[ ! -f "$CONFIG" ]]; then
        echo "Error: $CONFIG is missing; initialize the stack with 'just arangodb configure-restore --namespace <ns>'." >&2
        exit 1
    fi
    BUCKET=$(yq -r '.config."arangodb-restore:properties".bucket // ""' "$CONFIG")
    SECRET=$(yq -r '.config."arangodb-restore:properties".resticSecret.name // ""' "$CONFIG")
    if [[ -z "$BUCKET" || "$BUCKET" == "null" || -z "$SECRET" || "$SECRET" == "null" ]]; then
        echo "Error: could not read bucket/resticSecret.name from $CONFIG." >&2
        echo "       Run 'just arangodb reset-restore-config --stack $STACK' to restore the DR defaults." >&2
        exit 1
    fi

    # Assert the DR identity instead of assuming it. import-database's trap runs
    # reset-restore-config with `|| true`, so a failed reset can leave the
    # foreign source overlay on disk — and this recipe would then read another
    # project's bucket while claiming to be a same-cluster restore.
    STACK_NOLOCK=$(yq -r '.config."arangodb-restore:properties".noLock // "false"' "$CONFIG")
    if [[ "$SECRET" == "dictycr-source" || "$STACK_NOLOCK" == "true" ]]; then
        echo "Error: the arangodb-restore stack still carries a cross-project bootstrap overlay (resticSecret.name '$SECRET', noLock '$STACK_NOLOCK')." >&2
        echo "       Run 'just arangodb reset-restore-config --stack $STACK' before a same-cluster restore," >&2
        echo "       or use 'just arangodb import-database' for a cross-cluster one." >&2
        exit 1
    fi

    # Pin an omitted / `latest` --snapshot to a concrete id BEFORE the preflight:
    # the preflight proves one snapshot holds this database, and the Job must
    # restore exactly that snapshot. Left as `latest`, a CronJob backup landing
    # between the check and the Job would restore a point in time nobody
    # inspected, and the run would report no auditable recovery point at all.
    if [[ -z "$SNAPSHOT" || "$SNAPSHOT" == "latest" ]]; then
        SNAPSHOT=$(just arangodb _restic-snapshots-json \
            --namespace "$NAMESPACE" \
            --bucket "$BUCKET" \
            --secret "$SECRET" \
            | jq -r 'sort_by(.time) | last | .id')
        if [[ ! "$SNAPSHOT" =~ ^[a-f0-9]{64}$ ]]; then
            echo "Error: could not resolve the newest snapshot in gs://$BUCKET — pass --snapshot explicitly." >&2
            exit 1
        fi
        echo "Pinned newest snapshot: $SNAPSHOT"
    fi

    # --- preflight, read-only, before the first mutation -------------------
    just arangodb _assert-no-running-jobs --namespace "$NAMESPACE" --selector app=arangodb-restore
    just arangodb _preflight-database-restore \
        --namespace "$NAMESPACE" \
        --server "$SERVER" \
        --database "$DATABASE" \
        --bucket "$BUCKET" \
        --secret "$SECRET" \
        --snapshot "$SNAPSHOT" \
        --stack "$STACK" \
        --overwrite "$OVERWRITE"

    # Whole-instance DR posture on EVERY exit path, failed restore included.
    trap 'just arangodb _reset-single-database-config --stack "$STACK" || true' EXIT

    OVERWRITE_FLAG=""
    if [[ "$OVERWRITE" == "yes" || "$OVERWRITE" == "true" ]]; then
        OVERWRITE_FLAG="--overwrite yes"
    fi

    # ${OVERWRITE_FLAG} stays unquoted on purpose: it is either an empty string
    # or the two-word flag pair, and quoting would pass it as one argument.
    just arangodb configure-restore \
        --namespace "$NAMESPACE" \
        --database "$DATABASE" \
        --snapshot "$SNAPSHOT" \
        --server "$SERVER" \
        --restore-id "$RESTORE_ID" \
        --stack "$STACK" \
        ${OVERWRITE_FLAG}

    just arangodb apply-restore \
        --stack "$STACK" \
        --retries "{{ retries }}" \
        --interval "{{ interval }}"

    echo
    echo "Verifying database '$DATABASE' on $SERVER..."
    just arangodb _database-has-collections \
        --namespace "$NAMESPACE" \
        --server "$SERVER" \
        --database "$DATABASE" \
        --stack "$STACK"

    echo
    echo "Single-database restore of '$DATABASE' finished."
    echo "  point in time : snapshot '$SNAPSHOT' in gs://$BUCKET"
    echo "  users/grants are NOT restored (they live in _system) — run 'just arangodb create-databases' and 'just arangodb configure-app-credentials' if the app user is missing."
    echo "  single-database keys are cleared from the arangodb-restore stack on exit; 'just arangodb reset-restore-config' also restores the DR defaults."

# Import ONE database from ANOTHER project's restic bucket (the cross-cluster
# case). Same shape as restore-database, but the config overlay points this
# cluster's arangodb-restore stack at the SOURCE bucket with the read-only
# `dictycr-source` identity, so the EXIT trap is the broader
# reset-restore-config: bucket, secrets, noLock and every single-database key.
# Usage: just arangodb import-database --namespace <ns> --bucket <source-bucket> --database <db> [--snapshot <id|latest>] [--overwrite yes|no] [--secret <name>] [--server <svc>] [--restore-id <id>] [--stack <name>] [--retries <n>] [--interval <s>]
[arg("namespace", long="namespace", short="n", help="Target namespace to restore into (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS bucket holding the restic repository (required)")]
[arg("database", long="database", short="d", help="Database to import (required)")]
[arg("snapshot", long="snapshot", short="p", help="Restic snapshot id, or 'latest' (default latest)")]
[arg("overwrite", long="overwrite", short="w", help="Replace an existing target database: yes|no (default no)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("server", long="server", short="v", help="Coordinator Service name (default: auto-discover, else 'arangodb')")]
[arg("restore_id", long="restore-id", short="i", help="DNS-1123 restore id (default: import-<UTC timestamp>)")]
[arg("stack", long="stack", short="k", help="Pulumi stack name (defaults to PULUMI_STACK; no dev fallback)")]
[arg("retries", long="retries", short="r", help="Probe attempts per phase (default 180)")]
[arg("interval", long="interval", short="t", help="Seconds between probes (default 10)")]
[group('arangodb')]
[no-cd]
import-database namespace bucket database snapshot="" overwrite="no" secret="dictycr-source" server="" restore_id="" stack="" retries="180" interval="10":
    #!/usr/bin/env bash
    set -euo pipefail

    NAMESPACE="{{ namespace }}"
    BUCKET="{{ bucket }}"
    DATABASE="{{ database }}"
    SNAPSHOT="{{ snapshot }}"
    OVERWRITE="{{ overwrite }}"
    SECRET="{{ secret }}"
    SERVER="{{ server }}"
    RESTORE_ID="{{ restore_id }}"
    STACK=$(just arangodb _require-stack --stack "{{ stack }}")

    if [[ -z "$NAMESPACE" ]]; then
        echo "Error: --namespace is required; there is no safe default restore target." >&2
        exit 1
    fi
    if [[ -z "$BUCKET" ]]; then
        echo "Error: --bucket is required; pass the SOURCE project's restic bucket." >&2
        exit 1
    fi
    if [[ -z "$DATABASE" ]]; then
        echo "Error: --database is required; use bootstrap-from-snapshot for a whole-instance first load." >&2
        exit 1
    fi

    # Pin an omitted / `latest` --snapshot to a concrete id BEFORE the preflight,
    # so the snapshot the preflight proves is the snapshot the Job restores and
    # the closing summary names an auditable recovery point. configure-bootstrap
    # resolves the same value; pinning here keeps both halves on one id.
    if [[ -z "$SNAPSHOT" || "$SNAPSHOT" == "latest" ]]; then
        SNAPSHOT=$(just arangodb _restic-snapshots-json \
            --namespace "$NAMESPACE" \
            --bucket "$BUCKET" \
            --secret "$SECRET" \
            | jq -r 'sort_by(.time) | last | .id')
        if [[ ! "$SNAPSHOT" =~ ^[a-f0-9]{64}$ ]]; then
            echo "Error: could not resolve the newest snapshot in gs://$BUCKET — pass --snapshot explicitly." >&2
            exit 1
        fi
        echo "Pinned newest source snapshot: $SNAPSHOT"
    fi
    if [[ -z "$RESTORE_ID" ]]; then
        RESTORE_ID="import-$(date -u +%Y%m%d-%H%M%S)"
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

    # --- preflight, read-only, before the first mutation -------------------
    just arangodb _assert-no-running-jobs --namespace "$NAMESPACE" --selector app=arangodb-restore
    just arangodb _preflight-database-restore \
        --namespace "$NAMESPACE" \
        --server "$SERVER" \
        --database "$DATABASE" \
        --bucket "$BUCKET" \
        --secret "$SECRET" \
        --snapshot "$SNAPSHOT" \
        --stack "$STACK" \
        --overwrite "$OVERWRITE"

    # The foreign-bucket overlay must never outlive this command, even on
    # failure — reset-restore-config also clears the single-database keys.
    trap 'just arangodb reset-restore-config --stack "$STACK" || true' EXIT

    OVERWRITE_FLAG=""
    if [[ "$OVERWRITE" == "yes" || "$OVERWRITE" == "true" ]]; then
        OVERWRITE_FLAG="--overwrite yes"
    fi

    just arangodb configure-bootstrap \
        --namespace "$NAMESPACE" \
        --bucket "$BUCKET" \
        --database "$DATABASE" \
        --snapshot "$SNAPSHOT" \
        --server "$SERVER" \
        --restore-id "$RESTORE_ID" \
        --secret "$SECRET" \
        --stack "$STACK" \
        ${OVERWRITE_FLAG}

    just arangodb apply-restore \
        --stack "$STACK" \
        --retries "{{ retries }}" \
        --interval "{{ interval }}"

    echo
    echo "Verifying database '$DATABASE' on $SERVER..."
    just arangodb _database-has-collections \
        --namespace "$NAMESPACE" \
        --server "$SERVER" \
        --database "$DATABASE" \
        --stack "$STACK"

    echo
    echo "Single-database import of '$DATABASE' finished."
    echo "  point in time : snapshot '$SNAPSHOT' in gs://$BUCKET"
    echo "  users/grants are NOT restored (they live in _system) — run 'just arangodb finalize-bootstrap --app-user <user> --app-password <password>' to rotate root and the app password, then verify."
    echo "  the arangodb-restore stack is reset to its DR defaults on exit (bucket, secrets, noLock, database, overwrite)."

# Internal: run `restic snapshots --json` against <bucket> as a throwaway pod
# in <namespace>, using resticPass/gcsProject/gcsCredentials from <secret>.
# Emits ONLY the restic JSON array on stdout. Used by list-snapshots-in-cluster
# and bootstrap-from-snapshot (--snapshot inference).
# --no-lock is required for the SOURCE repository: its reader SA is read-only
# (roles/storage.objectViewer) and cannot create restic lock objects. Listing
# never mutates the repo, so --no-lock is safe for the own-bucket case too.
# Usage: JSON=$(just arangodb _restic-snapshots-json --namespace <ns> --bucket <b> --secret <name> [--image <ref>])
[arg("namespace", long="namespace", short="n", help="Namespace holding the restic secret")]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket")]
[arg("secret", long="secret", short="c", help="Secret holding resticPass/gcsProject/gcsCredentials")]
[arg("image", long="image", short="m", help="restic image reference")]
[group('arangodb')]
[no-cd]
_restic-snapshots-json namespace bucket secret image="restic/restic:0.17.0":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    BUCKET="{{ bucket }}"
    SECRET="{{ secret }}"
    IMAGE="{{ image }}"
    POD="restic-json-$(date -u +%s)"

    # restic's GCS backend needs GOOGLE_APPLICATION_CREDENTIALS to be a file
    # path, so the key is mounted from the secret rather than passed inline.
    RESTIC_ARGS=("-r" "gs:${BUCKET}:/" "--no-lock" "snapshots" "--json")
    ARGS_JSON=$(printf '%s\n' "${RESTIC_ARGS[@]}" | jq -R . | jq -sc .)
    OVERRIDES=$(jq -nc \
        --arg name "$POD" --arg image "$IMAGE" --arg secret "$SECRET" \
        --argjson args_array "$ARGS_JSON" \
        '{spec:{containers:[{name:$name,image:$image,args:$args_array,
          env:[{name:"RESTIC_PASSWORD",valueFrom:{secretKeyRef:{name:$secret,key:"resticPass"}}},
               {name:"GOOGLE_PROJECT_ID",valueFrom:{secretKeyRef:{name:$secret,key:"gcsProject"}}},
               {name:"GOOGLE_APPLICATION_CREDENTIALS",value:"/var/secret/gcs-credentials"}],
          volumeMounts:[{name:"gcs-credentials",mountPath:"/var/secret",readOnly:true}]}],
          volumes:[{name:"gcs-credentials",secret:{secretName:$secret,items:[{key:"gcsCredentials",path:"gcs-credentials"}]}}]}}')

    RAW=$(kubectl run "$POD" --rm -i --restart=Never -n "$NS" \
        --image "$IMAGE" --overrides="$OVERRIDES")

    # kubectl mixes banner/trailer lines ("pod ... deleted") into stdout; keep
    # only the restic --json array block.
    RAW=$(printf '%s\n' "$RAW" | awk '/^\[/{f=1} f{print} f&&/\]$/{exit}')
    if [[ -z "$RAW" ]]; then
        echo "Error: no snapshot JSON returned — check the Secret and bucket." >&2
        exit 1
    fi
    printf '%s\n' "$RAW"

# Internal: assert that a restic snapshot contains a path, printing the
# matching entries. This is how a single-database restore proves
# /arangodump/<db> exists BEFORE it writes any stack config: without the
# check a typo'd --database matches nothing, restic restores an empty tree,
# and the failure only shows up after the restore Job exists.
# Wired exactly like _restic-snapshots-json (same secret/credential
# handling). --no-lock is always passed: the SOURCE repository's reader
# identity cannot create a restic lock object, and listing never mutates.
# Usage: just arangodb _restic-ls --namespace <ns> --bucket <b> --secret <name> --snapshot <id|latest> --path <p> [--image <ref>]
[arg("namespace", long="namespace", short="n", help="Namespace holding the restic secret")]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket")]
[arg("secret", long="secret", short="c", help="Secret holding resticPass/gcsProject/gcsCredentials")]
[arg("snapshot", long="snapshot", short="s", help="Restic snapshot id, or 'latest'")]
[arg("path", long="path", short="p", help="Absolute path inside the snapshot, e.g. /arangodump/mydb")]
[arg("image", long="image", short="m", help="restic image reference")]
[group('arangodb')]
[private]
[no-cd]
_restic-ls namespace bucket secret snapshot path image="restic/restic:0.17.0":
    #!/usr/bin/env bash
    set -euo pipefail

    NS={{ quote(namespace) }}
    BUCKET={{ quote(bucket) }}
    SECRET={{ quote(secret) }}
    SNAPSHOT={{ quote(snapshot) }}
    PATH_IN_SNAPSHOT={{ quote(path) }}
    IMAGE={{ quote(image) }}
    POD="restic-ls-$(date -u +%s)"

    if [[ ! "$PATH_IN_SNAPSHOT" =~ ^/[a-zA-Z0-9_/-]+$ ]]; then
        echo "Error: path '$PATH_IN_SNAPSHOT' must be absolute and hold only letters, digits, underscore, hyphen and slash." >&2
        exit 1
    fi

    # restic's GCS backend needs GOOGLE_APPLICATION_CREDENTIALS to be a file
    # path, so the key is mounted from the secret rather than passed inline.
    RESTIC_ARGS=("-r" "gs:${BUCKET}:/" "--no-lock" "ls" "$SNAPSHOT" "$PATH_IN_SNAPSHOT")
    ARGS_JSON=$(printf '%s\n' "${RESTIC_ARGS[@]}" | jq -R . | jq -sc .)
    OVERRIDES=$(jq -nc \
        --arg name "$POD" --arg image "$IMAGE" --arg secret "$SECRET" \
        --argjson args_array "$ARGS_JSON" \
        '{spec:{containers:[{name:$name,image:$image,args:$args_array,
          env:[{name:"RESTIC_PASSWORD",valueFrom:{secretKeyRef:{name:$secret,key:"resticPass"}}},
               {name:"GOOGLE_PROJECT_ID",valueFrom:{secretKeyRef:{name:$secret,key:"gcsProject"}}},
               {name:"GOOGLE_APPLICATION_CREDENTIALS",value:"/var/secret/gcs-credentials"}],
          volumeMounts:[{name:"gcs-credentials",mountPath:"/var/secret",readOnly:true}]}],
          volumes:[{name:"gcs-credentials",secret:{secretName:$secret,items:[{key:"gcsCredentials",path:"gcs-credentials"}]}}]}}')

    RAW=$(kubectl run "$POD" --rm -i --restart=Never -n "$NS" \
        --image "$IMAGE" --overrides="$OVERRIDES")

    # restic ls prints a "snapshot <id> of [...]" header first and kubectl
    # mixes banner/trailer lines into stdout; keeping only lines that start
    # with the requested path leaves exactly the snapshot's own entries.
    MATCHES=$(printf '%s\n' "$RAW" | grep -E -- "^${PATH_IN_SNAPSHOT}(/|\$)" || true)
    if [[ -z "$MATCHES" ]]; then
        echo "Error: snapshot '$SNAPSHOT' in gs://$BUCKET contains no '$PATH_IN_SNAPSHOT'." >&2
        echo "       Check the database name spelling, and pick a snapshot that holds it:" >&2
        echo "       just arangodb list-snapshots-in-cluster --namespace $NS --bucket $BUCKET --secret $SECRET" >&2
        exit 1
    fi
    printf '%s\n' "$MATCHES"

# List restic snapshots from inside the cluster, using the same `dictycr`
# secret wiring the restore Job uses. No local restic, no kops state store.
# Usage: just arangodb list-snapshots-in-cluster --namespace <ns> [--bucket <b>] [--secret <name>] [--image <ref>] [--latest <n>]
[arg("namespace", long="namespace", short="n", help="Namespace holding the dictycr secret (required)")]
[arg("bucket", long="bucket", short="b", help="GCS restic bucket")]
[arg("secret", long="secret", short="c", help="Secret holding resticPass/gcsProject/gcsCredentials")]
[arg("image", long="image", short="m", help="restic image reference")]
[arg("latest", long="latest", short="l", help="Print only the newest <n> snapshots (default 4; 0 = full list)")]
[group('arangodb')]
[no-cd]
list-snapshots-in-cluster namespace bucket="restic-arangodb-backup-prod" secret="dictycr" image="restic/restic:0.17.0" latest="4":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    LATEST="{{ latest }}"

    # restic --latest is NOT used for limiting: restic filters it per host+path
    # and every backup job has a unique pod hostname, so it would return nothing
    # less than the full list. Instead we fetch --json and sort locally.
    RAW=$(just arangodb _restic-snapshots-json \
        --namespace "$NS" \
        --bucket "{{ bucket }}" \
        --secret "{{ secret }}" \
        --image "{{ image }}")

    # restic snapshots --json emits one JSON array. Sort by time, keep the
    # newest <LATEST>, print a fixed-width table. 0 = full list.
    if [[ "$LATEST" != "0" ]]; then
        FILTER="sort_by(.time) | .[-${LATEST}:] | reverse"
    else
        FILTER="sort_by(.time) | reverse"
    fi
    {
        printf 'ID\tTIME\tHOST\tPATH\n'
        printf '%s\n' "$RAW" | jq -r "$FILTER | .[] | [.id[0:8], .time, .hostname, (.paths | first)] | @tsv"
    } | column -t -s "$(printf '\t')"
    printf '%s\n' "$RAW" | jq 'length' | awk '{print "\n" $1 " snapshots in repository"}'

# List snapshots in the SOURCE project's restic repository. Thin wrapper over
# list-snapshots-in-cluster that makes --bucket mandatory and defaults the
# secret to `dictycr-source`, so a forgotten flag cannot silently list this
# cluster's own repository and hand you a snapshot id from the wrong repo.
# Usage: just arangodb list-source-snapshots --namespace <ns> --bucket <source-bucket> [--secret <name>] [--image <ref>] [--latest <n>]
[arg("namespace", long="namespace", short="n", help="Namespace holding the dictycr-source secret (required)")]
[arg("bucket", long="bucket", short="b", help="SOURCE GCS restic bucket (required; never defaulted)")]
[arg("secret", long="secret", short="c", help="Secret holding the SOURCE resticPass/gcsProject/gcsCredentials")]
[arg("image", long="image", short="m", help="restic image reference")]
[arg("latest", long="latest", short="l", help="Print only the newest <n> snapshots (default 4; 0 = full list)")]
[group('arangodb')]
[no-cd]
list-source-snapshots namespace bucket secret="dictycr-source" image="restic/restic:0.17.0" latest="4":
    #!/usr/bin/env bash
    set -euo pipefail

    if [[ -z "{{ bucket }}" ]]; then
        echo "Error: --bucket is required; pass the SOURCE project's restic bucket explicitly." >&2
        exit 1
    fi

    echo "Listing snapshots in gs://{{ bucket }} using secret '{{ secret }}' (source repository)."
    if [[ "{{ latest }}" == "0" ]]; then
        echo "Showing the full list — pin an explicit id for an auditable restore, or omit --snapshot in bootstrap-from-snapshot to use the newest."
    else
        echo "Showing newest {{ latest }} — omit --snapshot in bootstrap-from-snapshot to use the newest, or pin an explicit id."
    fi
    echo

    just arangodb list-snapshots-in-cluster \
        --namespace "{{ namespace }}" \
        --bucket "{{ bucket }}" \
        --secret "{{ secret }}" \
        --image "{{ image }}" \
        --latest "{{ latest }}"

# Verify stateful-db pool prerequisites before ArangoDB installation.
# Checks node count, architecture, taint, and zones. Exits non-zero if any check fails.
# Usage: just arangodb check-pool [--pool <label>] [--node-count <n>]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('arangodb')]
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

    # Fetch node data once
    nodes_json=$(kubectl get nodes -l "pool=$POOL" -o json)
    node_count=$(printf '%s\n' "$nodes_json" | jq '.items | length')

    # Node count
    if [[ "$node_count" -eq "$WANT_NODES" ]]; then
        ok "$node_count node(s) with pool=$POOL"
    else
        bad "$node_count node(s) with pool=$POOL, expected $WANT_NODES"
    fi

    # Architecture check (must be amd64 for production ArangoDB)
    amd64_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select(.status.nodeInfo.architecture == "amd64")] | length')
    if [[ "$amd64_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $amd64_count node(s) are amd64"
    else
        bad "$amd64_count/$node_count node(s) are amd64 — ArangoDB Cluster requires amd64"
    fi

    # Taint check
    tainted=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.spec.taints[]? |
        select(.key == "dedicated" and .value == "'"$POOL"'" and .effect == "NoSchedule")] | length > 0)] | length')
    if [[ "$tainted" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $tainted node(s) carry dedicated=$POOL:NoSchedule"
    else
        bad "$tainted/$node_count node(s) carry dedicated=$POOL:NoSchedule"
    fi

    # Zone spread (informational)
    zones=$(printf '%s\n' "$nodes_json" | jq -r '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | sort | join(", ")')
    zone_count=$(printf '%s\n' "$nodes_json" | jq '[.items[].metadata.labels["topology.kubernetes.io/zone"] // "unknown"] | unique | length')
    info "zones: $zones ($zone_count unique)"

    # Ready status
    ready_count=$(printf '%s\n' "$nodes_json" | jq '[.items[] | select([.status.conditions[] | select(.type == "Ready" and .status == "True")] | length > 0)] | length')
    if [[ "$ready_count" -eq "$node_count" && "$node_count" -gt 0 ]]; then
        ok "all $ready_count node(s) are Ready"
    else
        bad "$ready_count/$node_count node(s) are Ready"
    fi

    # Summary
    echo
    if [[ "$failures" -eq 0 ]]; then
        printf '\033[32mAll pool checks passed.\033[0m Next: just arangodb configure-backup-secrets.\n'
    else
        printf '\033[31m%d check(s) failed.\033[0m See reference/arangodb/pool-requirements.md for fixes.\n' "$failures"
        exit 1
    fi

# Full post-install check of the pool, operator, storage, members and jobs.
# Prints one line per check and exits non-zero if a required check fails.
# Usage: just arangodb verify [--namespace <ns>] [--operator-namespace <ns>] [--members <n>] [--pool <label>] [--node-count <n>]
[arg("namespace", long="namespace", short="n", help="Namespace holding the ArangoDeployment")]
[arg("operator_namespace", long="operator-namespace", help="Namespace holding the operator (default prod; override for custom installs)")]
[arg("members", long="members", short="m", help="Expected member pod count (default 9)")]
[arg("pool", long="pool", short="p", help="Value of the node label 'pool' (default database)")]
[arg("node_count", long="node-count", short="c", help="Expected node count in that pool (default 3)")]
[group('arangodb')]
[no-cd]
verify namespace="prod" operator_namespace="prod" members="9" pool="database" node_count="3":
    #!/usr/bin/env bash
    set -euo pipefail

    NS="{{ namespace }}"
    OP_NS="{{ operator_namespace }}"
    WANT_MEMBERS="{{ members }}"
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

    operator=$(kubectl get pods -n "$OP_NS" -l app.kubernetes.io/name=kube-arangodb -o json 2>/dev/null \
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

    if kubectl get arangodeployment -n "$NS" arangodb >/dev/null 2>&1; then
        ok "ArangoDeployment arangodb present in $NS"
    else
        bad "ArangoDeployment arangodb missing in $NS"
    fi

    members_ready=$(kubectl get pods -n "$NS" -l arango_deployment=arangodb -o json \
        | jq '[.items[] | select(.status.phase == "Running")
        | select([.status.containerStatuses[]? | select(.ready | not)] | length == 0)] | length')
    if [[ "$members_ready" -eq "$WANT_MEMBERS" ]]; then
        ok "$members_ready/$WANT_MEMBERS member pods Running and ready"
    else
        bad "$members_ready/$WANT_MEMBERS member pods Running and ready"
    fi

    pvc_json=$(kubectl get pvc -n "$NS" -l arango_deployment=arangodb -o json)
    check_pvcs() {
        local class="$1" size="$2" want="$3" label="$4"
        local n
        n=$(printf '%s\n' "$pvc_json" | jq --arg c "$class" --arg s "$size" \
            '[.items[] | select(.spec.storageClassName == $c)
              | select(.status.phase == "Bound")
              | select(.spec.resources.requests.storage == $s)] | length')
        if [[ "$n" -eq "$want" ]]; then
            ok "$n Bound $label PVC(s) at $size on $class"
        else
            bad "$n Bound $label PVC(s) at $size on $class, expected $want"
        fi
    }
    check_pvcs dictycr-ssd 20Gi 3 agent
    check_pvcs dictycr-balanced 150Gi 3 dbserver

    coord=$(kubectl get svc -n "$NS" -l arango_deployment=arangodb -o json \
        | jq '[.items[] | select([.spec.ports[]? | select(.port == 8529)] | length > 0)] | length')
    if [[ "$coord" -ge 1 ]]; then
        ok "$coord Service(s) exposing port 8529 in $NS"
    else
        bad "no Service exposing port 8529 in $NS"
    fi

    dbjobs=$(kubectl get jobs -n "$NS" -l app=arangodb-create-databases -o json 2>/dev/null \
        | jq '[.items[] | select((.status.succeeded // 0) >= 1)] | length' || echo 0)
    total_dbjobs=$(kubectl get jobs -n "$NS" -l app=arangodb-create-databases -o json 2>/dev/null \
        | jq '.items | length' || echo 0)
    if [[ "$dbjobs" -ge 1 ]]; then
        ok "application credential/database setup Job succeeded"
    elif [[ "$total_dbjobs" -eq 0 ]]; then
        warn "no application setup Job found — confirm import and Secret 'backend' state"
    else
        bad "application setup Job present but not succeeded"
    fi

    if kubectl get cronjob -n "$NS" arangodb-backup-cronjob >/dev/null 2>&1; then
        ok "backup CronJob present"
    else
        warn "no arangodb-backup-cronjob — expected only if the backup stack was applied"
    fi

    echo
    echo "Zone spread is preferred (soft anti-affinity), not guaranteed — not checked here."
    if [[ "$failures" -ne 0 ]]; then
        echo "Error: $failures check(s) failed." >&2
        exit 1
    fi
    echo "All required checks passed."
