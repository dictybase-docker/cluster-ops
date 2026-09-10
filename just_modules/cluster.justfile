# Build the unified cluster-ops binary.
# Usage: just build
[group('setup-tools')]
[no-cd]
build:
    cd "{{ invocation_directory() }}" && go build -o bin/cluster-ops ./cmd/cluster-ops

# Generate a per-project SSH keypair for kops node access (Section 1.6).
# Key path: SSH_KEY env var if set (takes precedence), else credentials/<project>/k8sVM.
# Options: -p/--project <project-id>, -t/--type ed25519|rsa (default ed25519).
# Refuses to overwrite an existing keypair.
# Usage: just gcp-cluster generate-ssh-key [--project <project-id>] [--type ed25519|rsa]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var); builds credentials/<project>/k8sVM when SSH_KEY is unset")]
[arg("type", long="type", short="t", pattern="ed25519|rsa", help="Key algorithm: ed25519 (default) or rsa")]
[no-cd]
generate-ssh-key project="" type="ed25519":
    #!/usr/bin/env bash
    set -euo pipefail

    root="{{ invocation_directory() }}"

    if [ -n "${SSH_KEY:-}" ]; then
        case "${SSH_KEY}" in
            *.pub)
                key_path="${SSH_KEY%.pub}"
                ;;
            *)
                echo "ERROR: SSH_KEY must point at the '.pub' file, got: ${SSH_KEY}"
                exit 1
                ;;
        esac
    else
        project="{{ project }}"
        [ -z "${project}" ] && project="${PROJECT_ID:-}"
        if [ -z "${project}" ]; then
            echo "ERROR: no key path. Set SSH_KEY, PROJECT_ID, or pass --project <project-id>."
            exit 1
        fi
        key_path="${root}/credentials/${project}/k8sVM"
    fi

    key_dir="$(dirname "${key_path}")"

    if [ -e "${key_path}" ] || [ -e "${key_path}.pub" ]; then
        echo "ERROR: key already exists — refusing to overwrite:"
        [ -e "${key_path}" ] && echo "  ${key_path}"
        [ -e "${key_path}.pub" ] && echo "  ${key_path}.pub"
        echo "Remove those files only if you intend to rotate the keypair."
        exit 1
    fi

    mkdir -p "${key_dir}"

    if [ "{{ type }}" = "rsa" ]; then
        ssh-keygen -t rsa -b 4096 -f "${key_path}" -N "" -C "kops-cluster-nodes"
    else
        ssh-keygen -t ed25519 -f "${key_path}" -N "" -C "kops-cluster-nodes"
    fi

    echo ""
    echo "Created:"
    echo "  private: ${key_path}"
    echo "  public:  ${key_path}.pub"

    if [ -z "${SSH_KEY:-}" ]; then
        echo ""
        echo "Add this to the per-cluster env file with:"
        echo "  just create-cluster-env --env <env> --cluster <cluster> --credentials <sa.json> --ssh-key ${key_path}.pub"
    fi

# Set up the project's service accounts and roles.
# Usage: just gcp-cluster sa-accounts-setup [--project <project-id>] [--activate-api <true|false>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("activate_api", long="activate-api", short="a", pattern="true|false", help="Enable the required APIs first")]
[no-cd]
sa-accounts-setup project="" activate_api="true":
    #!/usr/bin/env bash
    set -euo pipefail

    gcloud config set disable_prompts true

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    if [ "{{ activate_api }}" = "true" ]; then
        just gcp-api enable-apis --project "${project_id}" \
             --api-file {{ invocation_directory() }}/gcs-files/apis/enabled_apis.txt
        sleep 10
    fi

    sa_accounts=("cloud-manager" "cluster-backup" "database-backup" "deploy-manager" "kops-cluster-creator")
    for sa_name in "${sa_accounts[@]}"; do
        sa_email="${sa_name}@${project_id}.iam.gserviceaccount.com"

        # Check if the service account already exists
        if ! gcloud iam service-accounts describe "$sa_email" --project="${project_id}" &>/dev/null; then
            echo "Creating service account: $sa_name"
            just gcp-sa create-sa --project "${project_id}" --sa-name "$sa_name" \
                --roles-file {{ invocation_directory() }}/gcs-files/roles-permissions/${sa_name}-roles.txt \
                --output-file {{ invocation_directory() }}/credentials/${project_id}-$sa_name.json
        else
            echo "Service account $sa_name already exists. Skipping creation."
        fi
    done

    gcloud config set disable_prompts false

# Update the kops cluster (version-aware apply).
# Usage: just gcp-cluster update-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
update-cluster cluster="" kops_name="" state="": build
    #!/usr/bin/env bash
    set -euo pipefail
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    [ -n "${c}" ] && export CLUSTER_NAME="${c}"
    [ -n "${kn}" ] && export KOPS_CLUSTER_NAME="${kn}"
    [ -n "${st}" ] && export KOPS_STATE_STORE="${st}"

    if [ -z "${KOPS_CLUSTER_NAME:-}" ] || [ -z "${KOPS_STATE_STORE:-}" ]; then
        echo "ERROR: cluster name (or kops name) and state store must be set or passed via --cluster / --state."
        exit 1
    fi

    if [ -z "${DOCKER_CONFIG:-}" ] || [ ! -d "${DOCKER_CONFIG:-}" ]; then
        tmp_docker=$(mktemp -d)
        trap 'rm -rf "${tmp_docker}"' EXIT
        export DOCKER_CONFIG="${tmp_docker}"
    fi
    ./bin/cluster-ops kops update

# Preview pending cluster changes without applying them (version-aware dry-run).
# Usage: just gcp-cluster plan-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
plan-cluster cluster="" kops_name="" state="": build
    #!/usr/bin/env bash
    set -euo pipefail
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    [ -n "${c}" ] && export CLUSTER_NAME="${c}"
    [ -n "${kn}" ] && export KOPS_CLUSTER_NAME="${kn}"
    [ -n "${st}" ] && export KOPS_STATE_STORE="${st}"

    if [ -z "${KOPS_CLUSTER_NAME:-}" ] || [ -z "${KOPS_STATE_STORE:-}" ]; then
        echo "ERROR: cluster name (or kops name) and state store must be set or passed via --cluster / --state."
        exit 1
    fi

    if [ -z "${DOCKER_CONFIG:-}" ] || [ ! -d "${DOCKER_CONFIG:-}" ]; then
        tmp_docker=$(mktemp -d)
        trap 'rm -rf "${tmp_docker}"' EXIT
        export DOCKER_CONFIG="${tmp_docker}"
    fi
    ./bin/cluster-ops kops plan

# Create and harden the GCS state bucket for kops. Idempotent — safe to re-run
# against an existing bucket, which just updates its configuration.
# Usage: just gcp-cluster create-state-bucket [--project <project-id>] [--bucket-name <bucket>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name (defaults to BUCKET_NAME env var, else kops-state-<CLUSTER_NAME>)")]
[no-cd]
create-state-bucket project="" bucket_name="": build
    #!/usr/bin/env bash
    set -euo pipefail
    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    bucket="{{ bucket_name }}"
    [ -z "${bucket}" ] && bucket="${BUCKET_NAME:-}"
    # Same convention as delete-state-bucket: derive kops-state-<cluster> from
    # CLUSTER_NAME when no explicit bucket name is given.
    if [ -z "${bucket}" ] && [ -n "${CLUSTER_NAME:-}" ]; then
        bucket="kops-state-${CLUSTER_NAME}"
    fi
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi
    if [ -z "$bucket" ]; then
        echo "ERROR: no bucket name — set BUCKET_NAME / CLUSTER_NAME, or pass --bucket-name."
        exit 1
    fi
    ./bin/cluster-ops bucket create --project="${project_id}" --bucket="${bucket}" --harden
    echo "State bucket ready: gs://${bucket}"

# Validate HA production topology.
# Thin wrapper — delegates to cluster-ops.
# Usage: just gcp-cluster validate-kops-ha [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
validate-kops-ha cluster="" kops_name="" state="": build
    #!/usr/bin/env bash
    set -euo pipefail
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    if [ -n "${c}" ]; then
        export CLUSTER_NAME="${c}"
        kn="{{ kops_name }}"
        [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
        [ -z "${kn}" ] && kn="${c}-k8s.local"
        export KOPS_CLUSTER_NAME="${kn}"
        st="{{ state }}"
        [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
        [ -z "${st}" ] && st="gs://kops-state-${c}"
        export KOPS_STATE_STORE="${st}"
    fi
    ./bin/cluster-ops validate ha

# Validate post-provisioning hardening components.
# Checks Cluster Autoscaler, Node Problem Detector, Metrics Server,
# cert-manager, and node-local DNS are all running.
# Usage: just validate-hardening
[no-cd]
validate-hardening:
    #!/usr/bin/env bash
    set -euo pipefail

    echo "=== Post-Provisioning Hardening Check ==="
    echo ""

    fail=0

    pods_running() {
        local namespace="$1" selector="$2" pods
        pods=$(kubectl get pods -n "$namespace" -l "$selector" \
            -o jsonpath='{range .items[*]}{.metadata.name}{"="}{.status.phase}{"\n"}{end}' \
            2>/dev/null || true)
        [ -n "$pods" ] && ! grep -qv '=Running$' <<<"$pods"
    }

    echo "--- Cluster Autoscaler ---"
    if pods_running "kube-system" "app=cluster-autoscaler"; then
        echo "  ✓ Running"
    else
        echo "  ✗ Not found or not Running"
        fail=1
    fi

    echo "--- Node Problem Detector ---"
    if kubectl get daemonset node-problem-detector -n kube-system &>/dev/null; then
        ready=$(kubectl get daemonset node-problem-detector -n kube-system -o jsonpath='{.status.numberReady}')
        desired=$(kubectl get daemonset node-problem-detector -n kube-system -o jsonpath='{.status.desiredNumberScheduled}')
        if [ "$ready" = "$desired" ]; then
            echo "  ✓ Running ($ready/$desired nodes)"
        else
            echo "  ✗ $ready/$desired nodes ready"
            fail=1
        fi
    else
        echo "  ✗ Not found"
        fail=1
    fi

    echo "--- Metrics Server ---"
    if kubectl top nodes &>/dev/null; then
        echo "  ✓ Reporting metrics"
    else
        echo "  ✗ Not reporting (wait 60s and retry)"
        fail=1
    fi

    echo "--- cert-manager ---"
    if pods_running "kube-system" "app.kubernetes.io/instance=cert-manager" || pods_running "cert-manager" "app.kubernetes.io/instance=cert-manager"; then
        echo "  ✓ Running"
    else
        echo "  ✗ Not found or not Running"
        fail=1
    fi

    echo "--- Node-local DNS cache ---"
    if pods_running "kube-system" "k8s-app=node-local-dns"; then
        echo "  ✓ Running"
    else
        echo "  ✗ Not found or not Running"
        fail=1
    fi

    echo ""
    if [ $fail -eq 0 ]; then
        echo "All 5 hardening components running."
    else
        echo "Some components failed. See Phase 7 for troubleshooting."
        exit 1
    fi

# Validate the kops cluster
# Checks if the cluster is correctly set up and running
# Usage: just gcp-cluster validate-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>] [--waittime <min>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("waittime", long="waittime", short="w", help="Minutes to wait for validation (default 20)")]
[no-cd]
validate-cluster cluster="" kops_name="" state="" waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    name_args=()
    [ -n "${kn}" ] && name_args=("--name=${kn}")
    state_args=()
    [ -n "${st}" ] && state_args=("--state=${st}")

    kops validate cluster "${name_args[@]+"${name_args[@]}"}" "${state_args[@]+"${state_args[@]}"}" --wait {{ waittime }}m

# Display the current status of the cluster
# Shows version, cluster info, and nodes
# Usage: just cluster-status
[no-cd]
cluster-status:
    #!/usr/bin/env bash
    set -euo pipefail
    kubectl version
    kubectl cluster-info
    kubectl get nodes

# Launch the k9s terminal UI for cluster management
# Usage: just k9s
[no-cd]
k9s:
    #!/usr/bin/env bash
    set -euo pipefail
    k9s

# Export the kubeconfig for the current cluster
# Usage: just gcp-cluster export-kubeconfig [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
export-kubeconfig cluster="" kops_name="" state="":
    #!/usr/bin/env bash
    set -euo pipefail
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    name_args=()
    [ -n "${kn}" ] && name_args=("--name=${kn}")
    state_args=()
    [ -n "${st}" ] && state_args=("--state=${st}")

    kube_args=()
    if [ -n "${KUBECONFIG:-}" ]; then
        mkdir -p "$(dirname "${KUBECONFIG}")"
        kube_args=("--kubeconfig=${KUBECONFIG}")
    fi

    kops export kubeconfig --admin "${name_args[@]+"${name_args[@]}"}" "${state_args[@]+"${state_args[@]}"}" "${kube_args[@]+"${kube_args[@]}"}"

# Export a kubeconfig file with a custom name and duration.
# Usage: just gcp-cluster export-named-kubeconfig --name <name> [--duration-hours <n>]
[arg("name", long="name", short="n", help="Kubeconfig name (without .yaml)")]
[arg("duration_hours", long="duration-hours", short="d", help="Validity in hours")]
[no-cd]
export-named-kubeconfig name duration_hours="24":
    #!/usr/bin/env bash
    set -euo pipefail
    kops export kubeconfig --kubeconfig="{{ name }}.yaml" \
        --admin={{ duration_hours }}h

# Export a kubeconfig file with a given name and custom hour duration

# Extract logs from pods in the cluster.
# Usage: just gcp-cluster extract-logs --label <label> [--namespace <ns>]
[arg("label", long="label", short="l", help="Pod label selector")]
[arg("namespace", long="namespace", short="n", help="Kubernetes namespace")]
[no-cd]
extract-logs label namespace="dev":
    #!/usr/bin/env bash
    set -euo pipefail

    # Check if KUBECONFIG is exported
    if [ -z "${KUBECONFIG:-}" ]; then
        echo "Error: KUBECONFIG environment variable is not set."
        echo "Please set KUBECONFIG to the path of your Kubernetes config file."
        exit 1
    fi

    # Build the custodian command
    echo "Building custodian command..."
    go build -o bin/custodian cmd/custodian/main.go

    # Run the custodian command
    echo "Extracting logs..."
    ./bin/custodian extract-log --label "{{ label }}" --namespace "{{ namespace }}"

    # Clean up the binary
    rm bin/custodian

# Exclude resources from backup by adding label 'velero.io/exclude-from-backup=true'
# and exclude volumes from backup by adding 'backup.velero.io/backup-volumes-excludes' annotation
# Usage: just exclude-from-backup [namespace]
[no-cd]
exclude-from-backup namespace="dev":
    #!/usr/bin/env bash
    set -euo pipefail

    # Check if KUBECONFIG is exported
    if [ -z "${KUBECONFIG:-}" ]; then
        echo "Error: KUBECONFIG environment variable is not set."
        echo "Please set KUBECONFIG to the path of your Kubernetes config file."
        exit 1
    fi

    # Build the custodian binary
    echo "Building custodian command..."
    go build -o bin/custodian cmd/custodian/main.go

    # Run the exclude-from-backup subcommand
    echo "Running exclude-from-backup..."
    ./bin/custodian exclude-from-backup --namespace "{{ namespace }}"

    # Run the exclude-volumes-from-backup subcommand
    echo "Running exclude-volumes-from-backup..."
    ./bin/custodian exclude-volumes-from-backup --namespace "{{ namespace }}"

    # Clean up the binary
    rm bin/custodian

[no-cd]
setup-cluster-backup:
    #!/usr/bin/env bash
    set -euo pipefail

    # Check if Velero is installed
    if ! command -v velero &> /dev/null; then
        echo "Error: Velero is not installed or not in the PATH."
        echo "Please install Velero and make sure it's accessible in your PATH."
        exit 1
    fi

    # If Velero is installed, proceed with the setup
    just gcp-cluster exclude-from-backup dev
    just gcp-pulumi preview --folder install-velero --stack experiments
    just gcp-pulumi create-resource --folder install-velero --stack experiments

[no-cd]
cluster-info:
    kops get cluster -o yaml

[no-cd]
cluster-dump:
    kops toolbox dump -v 9 --k8s-resources

# View instance groups in the kops cluster
# Usage: just instance-groups
[no-cd]
instance-groups:
    kops get ig

# ─────────────────────────────────────────────
# Disposable cluster lifecycle recipes (Section 9)
# ─────────────────────────────────────────────

# Teardown: dry-run preview or full destroy for the target cluster.
# Usage: just gcp-cluster delete-cluster [--cluster <name>] [--kops-name <name>] [--project <id>] [--state <uri>] [--confirm yes]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("project", long="project", short="p", help="GCP project ID (defaults to PROJECT_ID env var)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("confirm", long="confirm", pattern="yes|no", help="Set to 'yes' to destroy after dry-run")]
[no-cd]
delete-cluster cluster="" kops_name="" project="" state="" confirm="no":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    p="{{ project }}"
    [ -z "${p}" ] && p="${PROJECT_ID:-}"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    echo "=== Cluster teardown ==="
    echo "Cluster:  ${kn:-UNSET}"
    echo "Project:  ${p:-UNSET}"
    echo "State:    ${st:-UNSET}"
    echo ""

    if [ -z "${kn}" ] || [ -z "${st}" ]; then
        echo "ERROR: cluster name (or kops name) and state store must be set or passed via --cluster / --state."
        exit 1
    fi

    # ── Dry-run ────────────────────────────────
    echo "--- Dry-run: resources kOps will destroy ---"
    kops delete cluster \
        --name="${kn}" \
        --state="${st}"
    echo ""

    # ── Confirm or bail ────────────────────────
    if [ "{{ confirm }}" != "yes" ]; then
        echo "Dry-run complete. No resources were touched."
        echo ""
        echo "To destroy the cluster, review the list above, then run with --confirm yes."
        exit 0
    fi

    # ── Preflight: running workloads ───────────
    echo "--- Checking for running workloads ---"
    kubectl get pods --all-namespaces 2>/dev/null || \
        echo "  (no cluster access — may already be down)"
    echo ""

    read -r -p "Type 'destroy' to confirm: " answer
    if [ "$answer" != "destroy" ]; then
        echo "Aborted."
        exit 1
    fi

    # ── Execute ────────────────────────────────
    echo ""
    echo "Destroying cluster ${kn}..."
    kops delete cluster \
        --name="${kn}" \
        --state="${st}" \
        --yes

    # ── Verify cleanup ─────────────────────────
    echo ""
    echo "=== Cleanup verification ==="
    echo ""

    if [ -n "${p}" ]; then
        echo "Instances:"
        gcloud compute instances list \
            --project="${p}" \
            --filter="name:${kn}" \
            2>/dev/null || echo "  (gcloud unavailable)"
        echo ""

        echo "Disks (check for orphaned PVs):"
        gcloud compute disks list \
            --project="${p}" \
            --filter="name:${kn}" \
            2>/dev/null || echo "  (gcloud unavailable)"
        echo ""
    fi

    echo "Teardown complete."

# Delete the GCS state bucket and all objects/versions (Section 6.4).
# Dry-run by default; pass --confirm yes to delete permanently.
# Bucket is derived from --cluster (kops-state-<cluster>) unless --bucket-name / BUCKET_NAME is set.
# Usage: just gcp-cluster delete-state-bucket [--cluster <name>] [--bucket-name <bucket|gs://bucket>] [--confirm yes]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var); derives bucket kops-state-<cluster>")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name or URI (defaults to BUCKET_NAME env var or kops-state-<cluster>)")]
[arg("confirm", long="confirm", pattern="yes|no", help="Set to 'yes' to permanently delete the bucket and contents")]
[no-cd]
delete-state-bucket cluster="" bucket_name="" confirm="no":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    bucket="{{ bucket_name }}"
    [ -z "${bucket}" ] && bucket="${BUCKET_NAME:-}"
    if [ -z "${bucket}" ] && [ -n "${c}" ]; then
        bucket="kops-state-${c}"
    fi

    case "${bucket}" in
        gs://*) bucket_uri="${bucket}"; bucket_name="${bucket#gs://}" ;;
        *)       bucket_uri="gs://${bucket}"; bucket_name="${bucket}" ;;
    esac
    bucket_name="${bucket_name%/}"
    bucket_uri="gs://${bucket_name}"

    if [ -z "${bucket_name}" ]; then
        echo "ERROR: bucket name required. Pass --bucket-name <name> or --cluster <name> (or set BUCKET_NAME / CLUSTER_NAME)."
        exit 1
    fi

    echo "=== State bucket deletion ==="
    echo "Bucket:  ${bucket_uri}"
    echo ""

    if ! gcloud storage ls "${bucket_uri}" &>/dev/null; then
        echo "Bucket does not exist or is not accessible: ${bucket_uri}"
        echo "Nothing to delete."
        exit 0
    fi

    echo "--- Dry-run: objects that would be deleted ---"
    gcloud storage ls --recursive "${bucket_uri}" 2>/dev/null | head -n 20 || true
    object_count="$(gcloud storage ls --recursive "${bucket_uri}" 2>/dev/null | wc -l | tr -d ' ')"
    echo "Approx object count: ${object_count}"
    echo ""

    if [ "{{ confirm }}" != "yes" ]; then
        echo "Dry-run complete. Nothing deleted."
        echo "To permanently delete the bucket and all objects/versions, run with --confirm yes."
        exit 0
    fi

    read -r -p "Type 'delete' to confirm permanent deletion: " answer
    if [ "${answer}" != "delete" ]; then
        echo "Aborted."
        exit 1
    fi

    gcloud storage rm --recursive "${bucket_uri}"
    echo ""
    echo "State bucket deleted: ${bucket_uri}"

# Remove the project-scoped gcloud configurations created by bootstrap-identities
# and rotate-to-creator. Local-only cleanup — never touches GCP or the state
# bucket. Skips the active configuration (gcloud refuses deleting it); activate
# another config first. Dry-run by default.
# Usage: just gcp-cluster cleanup-gcloud-config [--project <id>] [--confirm yes]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("confirm", long="confirm", short="y", pattern="yes|no", help="Delete now instead of dry-run preview")]
[group('cluster-management')]
[no-cd]
cleanup-gcloud-config project="" confirm="no":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project." >&2
        exit 1
    fi

    names=("${project_id}-sa-manager" "${project_id}-kops-cluster-creator")
    active=$(gcloud config configurations list --filter="IS_ACTIVE=true" --format="value(name)" 2>/dev/null || true)

    for cfg in "${names[@]}"; do
        if ! gcloud config configurations describe "$cfg" >/dev/null 2>&1; then
            echo "absent:       $cfg"
            continue
        fi
        if [ "$cfg" = "$active" ] || [ "$cfg" = "${CLOUDSDK_ACTIVE_CONFIG_NAME:-}" ]; then
            echo "skip:         $cfg (active — activate another configuration first)" >&2
            continue
        fi
        if [ "{{ confirm }}" = "yes" ]; then
            gcloud config configurations delete "$cfg" --quiet
            echo "deleted:      $cfg"
        else
            echo "would delete: $cfg"
        fi
    done

    if [ "{{ confirm }}" != "yes" ]; then
        echo
        echo "Dry-run. Re-run with --confirm yes to delete."
    fi

# Bootstrap a canonical Git manifest bundle locally from starter templates.
# Pure offline operation — zero cloud/state store mutation.
# Usage: just gcp-cluster bootstrap-bundle --cluster <name> --project <id> --api-access-cidr <cidr> [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Short cluster name (e.g. dcr-kube1)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to <cluster>-k8s.local)")]
[arg("project", long="project", short="p", help="GCP project ID")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to gs://kops-state-<cluster>)")]
[arg("api_access_cidr", long="api-access-cidr", short="a", help="Administrative API CIDR (e.g. 203.0.113.10/32)")]
[no-cd]
bootstrap-bundle cluster="" kops_name="" project="" state="" api_access_cidr="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    p="{{ project }}"
    [ -z "${p}" ] && p="${PROJECT_ID:-}"
    api="{{ api_access_cidr }}"
    [ -z "${api}" ] && api="${API_ACCESS_CIDR:-}"

    if [ -z "${c}" ]; then
        echo "ERROR: cluster name is required. Pass --cluster <name> or set CLUSTER_NAME."
        exit 1
    fi
    if [ -z "${p}" ]; then
        echo "ERROR: project id is required. Pass --project <id> or set PROJECT_ID."
        exit 1
    fi
    if [ -z "${api}" ]; then
        echo "ERROR: API access CIDR is required (e.g. --api-access-cidr 203.0.113.10/32 or set API_ACCESS_CIDR)."
        exit 1
    fi

    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"
    st="${st%/}"

    target_dir="{{ invocation_directory() }}/config/kops/${c}"
    if [ -e "${target_dir}" ]; then
        echo "ERROR: target bundle directory already exists: ${target_dir}"
        echo "Refusing to overwrite existing cluster bundle."
        exit 1
    fi

    starter_dir="{{ invocation_directory() }}/config/kops/_starter"
    if [ ! -f "${starter_dir}/cluster.yaml.tmpl" ] || [ ! -f "${starter_dir}/instancegroups.yaml.tmpl" ]; then
        echo "ERROR: starter templates missing in ${starter_dir}."
        exit 1
    fi

    tmp_dir=$(mktemp -d "{{ invocation_directory() }}/config/kops/.bootstrap.XXXXXX")
    trap 'rm -rf "${tmp_dir}"' EXIT

    export KOPS_CLUSTER_NAME="${kn}"
    export PROJECT_ID="${p}"
    export KOPS_STATE_STORE="${st}"
    export API_ACCESS_CIDR="${api}"

    envsubst < "${starter_dir}/cluster.yaml.tmpl" > "${tmp_dir}/cluster.yaml"
    envsubst < "${starter_dir}/instancegroups.yaml.tmpl" > "${tmp_dir}/instancegroups.yaml"

    if [ ! -s "${tmp_dir}/cluster.yaml" ] || [ ! -s "${tmp_dir}/instancegroups.yaml" ]; then
        echo "ERROR: Generated bundle files are empty."
        exit 1
    fi

    if grep -E '\$\{[A-Za-z0-9_]+\}' "${tmp_dir}"/*.yaml; then
        echo "ERROR: Generated bundle contains unresolved template variables."
        exit 1
    fi

    mv "${tmp_dir}" "${target_dir}"
    echo "Canonical manifest bundle created: ${target_dir}"
    echo "  - ${target_dir}/cluster.yaml"
    echo "  - ${target_dir}/instancegroups.yaml"
    echo ""
    echo "Next steps:"
    echo "  1. Review/edit files:  \$EDITOR ${target_dir}/*.yaml"
    echo "  2. Commit to Git:      git add ${target_dir} && git commit -m '${c}: initial manifest bundle'"
    echo "  3. Create bucket:      just gcp-cluster create-state-bucket --project ${p} --bucket-name $(basename "${st}")"
    echo "  4. Push to state:      just gcp-cluster replace-manifests --cluster ${c} --force yes"

# Export live cluster and instance groups state into the canonical bundle.
# Break-glass recovery / adoption import only.
# Usage: just gcp-cluster export-bundle [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
export-bundle cluster="" kops_name="" state="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    if [ -z "${c}" ]; then
        echo "ERROR: cluster name required. Pass --cluster <name> or set CLUSTER_NAME."
        exit 1
    fi

    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"

    target_dir="{{ invocation_directory() }}/config/kops/${c}"
    mkdir -p "${target_dir}"
    tmp_dir=$(mktemp -d "${target_dir}/.export.XXXXXX")
    trap 'rm -rf "${tmp_dir}"' EXIT

    echo "Exporting cluster spec to temporary buffer..."
    kops get cluster --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/cluster.yaml"

    echo "Exporting instance groups to temporary buffer..."
    kops get instancegroups --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/instancegroups.yaml"

    if [ ! -s "${tmp_dir}/cluster.yaml" ] || [ ! -s "${tmp_dir}/instancegroups.yaml" ]; then
        echo "ERROR: Exported files are empty. Aborting export to prevent truncating canonical files."
        exit 1
    fi

    mv "${tmp_dir}/cluster.yaml" "${target_dir}/cluster.yaml"
    mv "${tmp_dir}/instancegroups.yaml" "${target_dir}/instancegroups.yaml"

    echo "Bundle exported successfully to: ${target_dir}"
    echo "Commit with: git add ${target_dir} && git commit -m 'save canonical cluster bundle'"

# Push the committed bundle to the state bucket.
# With --force yes, pass kops replace --force (create-or-update).
# Usage: just gcp-cluster replace-manifests [--cluster <name>] [--kops-name <name>] [--state <uri>] [--force yes]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("force", long="force", short="f", pattern="yes|no", help="Set to 'yes' to pass --force (create-or-update)")]
[no-cd]
replace-manifests cluster="" kops_name="" state="" force="no":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    if [ -z "${c}" ]; then
        echo "ERROR: cluster name required. Pass --cluster <name> or set CLUSTER_NAME."
        exit 1
    fi

    dir="{{ invocation_directory() }}/config/kops/${c}"
    cluster_yaml="$dir/cluster.yaml"
    igs_yaml="$dir/instancegroups.yaml"

    if [ ! -s "${cluster_yaml}" ] || [ ! -s "${igs_yaml}" ]; then
        echo "ERROR: bundle files missing or empty in ${dir}."
        echo "Expected non-empty:"
        echo "  - ${cluster_yaml}"
        echo "  - ${igs_yaml}"
        echo "Run 'just gcp-cluster bootstrap-bundle' or 'just gcp-cluster export-bundle' first."
        exit 1
    fi

    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"

    force_args=()
    if [ "{{ force }}" = "yes" ]; then
        force_args=("--force")
    fi

    echo "Replacing cluster configuration from ${cluster_yaml}..."
    kops replace -f "${cluster_yaml}" --state="${st}" --name="${kn}" "${force_args[@]+"${force_args[@]}"}"

    echo "Replacing instance groups configuration from ${igs_yaml}..."
    kops replace -f "${igs_yaml}" --state="${st}" --name="${kn}" "${force_args[@]+"${force_args[@]}"}"
    echo "Manifests replaced in state store: ${st}"

# Diff canonical bundle files against live state storage.
# Usage: just gcp-cluster drift-manifests [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
drift-manifests cluster="" kops_name="" state="": build
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    if [ -z "${c}" ]; then
        echo "ERROR: cluster name required. Pass --cluster <name> or set CLUSTER_NAME."
        exit 1
    fi

    target_dir="{{ invocation_directory() }}/config/kops/${c}"
    cluster_yaml="${target_dir}/cluster.yaml"
    igs_yaml="${target_dir}/instancegroups.yaml"

    if [ ! -s "${cluster_yaml}" ] || [ ! -s "${igs_yaml}" ]; then
        echo "ERROR: canonical bundle files missing or empty in ${target_dir}."
        exit 1
    fi

    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir}"' EXIT

    kops get cluster --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/live-cluster.yaml"
    kops get instancegroups --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/live-igs.yaml"

    root="{{ invocation_directory() }}"
    "${root}/bin/cluster-ops" kops normalize --file "${cluster_yaml}" --output "${tmp_dir}/norm-git-cluster.yaml"
    "${root}/bin/cluster-ops" kops normalize --file "${tmp_dir}/live-cluster.yaml" --output "${tmp_dir}/norm-live-cluster.yaml"

    "${root}/bin/cluster-ops" kops normalize --file "${igs_yaml}" --output "${tmp_dir}/norm-git-igs.yaml"
    "${root}/bin/cluster-ops" kops normalize --file "${tmp_dir}/live-igs.yaml" --output "${tmp_dir}/norm-live-igs.yaml"

    rc=0
    echo "--- Checking cluster.yaml drift ---"
    if ! diff -u "${tmp_dir}/norm-git-cluster.yaml" "${tmp_dir}/norm-live-cluster.yaml"; then
        echo "DRIFT detected in cluster.yaml"
        rc=1
    else
        echo "cluster.yaml matches live state."
    fi

    echo "--- Checking instancegroups.yaml drift ---"
    if ! diff -u "${tmp_dir}/norm-git-igs.yaml" "${tmp_dir}/norm-live-igs.yaml"; then
        echo "DRIFT detected in instancegroups.yaml"
        rc=1
    else
        echo "instancegroups.yaml matches live state."
    fi

    exit "$rc"

# Upload the SSH public key as a kops secret into the state store.
# Usage: just gcp-cluster upload-ssh-secret [--cluster <name>] [--kops-name <name>] [--state <uri>] [--ssh-key <path>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("ssh_key", long="ssh-key", short="k", help="SSH public key path (defaults to SSH_KEY env var or credentials/<project>/k8sVM.pub)")]
[no-cd]
upload-ssh-secret cluster="" kops_name="" state="" ssh_key="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    key="{{ ssh_key }}"
    [ -z "${key}" ] && key="${SSH_KEY:-}"
    [ -z "${key}" ] && [ -n "${PROJECT_ID:-}" ] && key="credentials/${PROJECT_ID}/k8sVM.pub"

    if [ -z "${kn}" ] || [ -z "${st}" ]; then
        echo "ERROR: kops name and state store must be set or passed via --cluster / --kops-name / --state."
        exit 1
    fi
    if [ -z "${key}" ] || [ ! -f "${key}" ]; then
        echo "ERROR: SSH public key not found: ${key:-<unset>}."
        echo "Pass --ssh-key <path>, set SSH_KEY, or run 'just gcp-cluster generate-ssh-key --project <id>' (writes credentials/<id>/k8sVM.pub)."
        exit 1
    fi

    kops create secret sshpublickey --name="${kn}" --state="${st}" -i "${key}"
    echo "SSH public key uploaded to state store for ${kn}."

# Perform rolling update on cluster nodes (dry-run by default, pass --yes to execute).
# Usage: just gcp-cluster rolling-update [--cluster <name>] [--kops-name <name>] [--state <uri>] [--instance-group <name>] [--force yes] [--yes yes]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("instance_group", long="instance-group", short="i", help="Restrict roll to specific InstanceGroup")]
[arg("force", long="force", short="f", pattern="yes|no", help="Set to 'yes' to force rolling update even if no changes reported")]
[arg("yes", long="yes", short="y", pattern="yes|no", help="Set to 'yes' to execute rolling update immediately")]
[no-cd]
rolling-update cluster="" kops_name="" state="" instance_group="" force="no" yes="no":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    if [ -z "${kn}" ] || [ -z "${st}" ]; then
        echo "ERROR: kops name and state store must be set or passed via --cluster / --kops-name / --state."
        exit 1
    fi

    cmd_args=()
    [ -n "{{ instance_group }}" ] && cmd_args+=("--instance-group={{ instance_group }}")
    [ "{{ force }}" = "yes" ] && cmd_args+=("--force")
    [ "{{ yes }}" = "yes" ] && cmd_args+=("--yes")

    echo "Running rolling-update for ${kn}..."
    kops rolling-update cluster --name="${kn}" --state="${st}" "${cmd_args[@]+"${cmd_args[@]}"}"


# ── operator convenience recipes ──────────────────────────────────────────────

# Print this machine's public IPv4 and the /32 CIDR to pass to --api-access-cidr.
# Rejects a non-IPv4 or private answer instead of emitting a CIDR that would
# silently lock you out of the API server.
# Usage: just gcp-cluster show-public-ip
[group('cluster-management')]
[no-cd]
show-public-ip:
    #!/usr/bin/env bash
    set -uo pipefail

    ip=$(curl -sS -4 --max-time 10 https://api.ipify.org 2>/dev/null)

    if [[ -z "$ip" ]]; then
        echo "Error: could not reach https://api.ipify.org to determine the public IP." >&2
        echo "Check network access, or find the address another way and pass it manually." >&2
        exit 1
    fi
    if [[ ! "$ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]]; then
        echo "Error: unexpected response, not an IPv4 address: $ip" >&2
        exit 1
    fi
    # A private answer means egress is NATed somewhere unexpected; a firewall rule
    # built from it would not match the address GCP actually sees.
    case "$ip" in
        10.*|192.168.*|127.*|169.254.*)
            echo "Error: got a private address ($ip) — not usable as an API access CIDR." >&2
            exit 1 ;;
        172.1[6-9].*|172.2[0-9].*|172.3[0-1].*)
            echo "Error: got a private address ($ip) — not usable as an API access CIDR." >&2
            exit 1 ;;
    esac

    echo "Public IPv4 : $ip"
    echo "API CIDR    : ${ip}/32"
    echo
    echo "Use it with:"
    echo "    just gcp-cluster bootstrap-bundle --cluster <name> --project <id> --api-access-cidr \"${ip}/32\""

# Create and activate a named gcloud configuration bound to a service-account key.
# Replaces the five-command 'gcloud config configurations' sequence.
# Usage: just gcp-cluster configure-gcloud [--name <cfg>] [--project <id>] [--key-file <path>] [--zone <zone>]
[arg("name", long="name", short="n", help="gcloud configuration name (default <project-id>-sa-manager)")]
[arg("project", long="project", short="p", help="GCP project ID (defaults to PROJECT_ID)")]
[arg("key_file", long="key-file", short="k", help="SA JSON key (defaults to GOOGLE_APPLICATION_CREDENTIALS)")]
[arg("zone", long="zone", short="z", help="Default compute zone (default us-central1-c)")]
[group('cluster-management')]
[no-cd]
configure-gcloud name="" project="" key_file="" zone="us-central1-c":
    #!/usr/bin/env bash
    set -euo pipefail

    ZONE="{{ zone }}"

    PROJECT="{{ project }}"
    [[ -z "$PROJECT" ]] && PROJECT="${PROJECT_ID:-}"
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no project — pass --project or enter the cluster shell so PROJECT_ID is set." >&2
        exit 1
    fi

    CFG="{{ name }}"
    if [[ -z "$CFG" ]]; then
        # gcloud config names are machine-global — one shared active_config pointer
        # in ~/.config/gcloud. Suffix with the project id so parallel clusters in
        # separate shells don't overwrite each other's active configuration.
        CFG="${PROJECT}-sa-manager"
    fi

    KEY="{{ key_file }}"
    [[ -z "$KEY" ]] && KEY="${GOOGLE_APPLICATION_CREDENTIALS:-}"
    if [[ -z "$KEY" ]]; then
        echo "Error: no key file — pass --key-file or set GOOGLE_APPLICATION_CREDENTIALS." >&2
        exit 1
    fi
    if [[ ! -f "$KEY" ]]; then
        echo "Error: key file not found: $KEY" >&2
        exit 1
    fi

    # Derive the SA email from the key itself rather than assuming <name>@<project>,
    # so a differently named key still activates the identity it actually contains.
    SA_EMAIL=$(jq -r '.client_email // empty' "$KEY")
    if [[ -z "$SA_EMAIL" ]]; then
        echo "Error: $KEY has no client_email — is it a service-account JSON key?" >&2
        exit 1
    fi

    echo "Configuration : $CFG"
    echo "Project       : $PROJECT"
    echo "Zone          : $ZONE"
    echo "Identity      : $SA_EMAIL"
    echo

    if gcloud config configurations describe "$CFG" >/dev/null 2>&1; then
        echo "Configuration '$CFG' already exists — reusing it."
    else
        gcloud config configurations create "$CFG" --no-activate
    fi

    gcloud auth activate-service-account "$SA_EMAIL" --key-file="$KEY"
    gcloud --configuration="$CFG" config set account "$SA_EMAIL"
    gcloud --configuration="$CFG" config set project "$PROJECT"
    gcloud --configuration="$CFG" config set compute/zone "$ZONE"

    echo
    echo "Configured gcloud configuration:"
    gcloud config configurations list --filter="name=$CFG" 2>/dev/null || true

# ── higher-order lifecycle recipes ────────────────────────────────────────────
# These fold the sequences documented in docs/kops-setup.md into single
# commands, mirroring arangodb's deploy-operator/deploy-cluster and pulumi's
# configure-backup-secrets. Steps that require a human decision (reviewing
# generated YAML, choosing an --api-access-cidr) or a shell login/logout
# (cluster-cred rotation, cluster-env re-entry) stay as separate manual steps —
# see docs/reference/kops/*.md for exactly which boundaries are which.

# Enable required APIs, disable unused ones, and create the least-privilege
# kops-cluster-creator service account. Folds Phase 1a + 1b + 2 into one call.
# Usage: just gcp-cluster setup-kops-creator [--project <project-id>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[group('cluster-management')]
[no-cd]
setup-kops-creator project="":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project." >&2
        exit 1
    fi

    root="{{ invocation_directory() }}"

    echo "=== 1/3: enabling required APIs ==="
    just gcp-api enable-apis --project "${project_id}" \
        --api-file "${root}/gcs-files/apis/enabled_apis.txt"

    echo
    echo "=== 2/3: disabling unused APIs ==="
    just gcp-api disable-apis --project "${project_id}" \
        --api-file "${root}/gcs-files/apis/disable_enabled_apis.txt"

    echo
    echo "=== 3/3: creating kops-cluster-creator service account ==="
    just gcp-sa create-sa --project "${project_id}" --sa-name kops-cluster-creator \
        --roles-file "${root}/gcs-files/roles-permissions/kops-cluster-creator-roles.txt" \
        --output-file "${root}/credentials/${project_id}/kops-cluster-creator.json"

    echo
    echo "kops-cluster-creator ready: credentials/${project_id}/kops-cluster-creator.json"
    echo "Rotate to it, then reconfigure gcloud (both require a shell re-entry):"
    echo "  just cluster-cred --key credentials/${project_id}/kops-cluster-creator.json"
    echo "  exit"
    echo "  just cluster-env --env <env> --cluster <cluster-name>"
    echo "  just gcp-cluster configure-gcloud --name ${project_id}-kops-cluster-creator"

# Bootstrap the full identity chain in one call: create sa-manager, activate it
# as a named gcloud config, then enable/disable APIs and create the
# least-privilege kops-cluster-creator. No shell re-entry needed — gcloud
# configs persist in ~/.config/gcloud and none of these steps read
# GOOGLE_APPLICATION_CREDENTIALS. The final rotation (rotate-to-creator) still
# needs one shell boundary afterward.
# Usage: just gcp-cluster bootstrap-identities [--project <project-id>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[group('cluster-management')]
[no-cd]
bootstrap-identities project="":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project." >&2
        exit 1
    fi

    root="{{ invocation_directory() }}"
    sa_manager_key="${root}/credentials/${project_id}/sa-manager.json"

    echo "=== 1/3: creating sa-manager ==="
    just gcp-sa setup-sa-manager --project-id "${project_id}" --key-file "${sa_manager_key}"

    echo
    echo "=== 2/3: activating sa-manager as a named gcloud config ==="
    just gcp-cluster configure-gcloud --name "${project_id}-sa-manager" --project "${project_id}" --key-file "${sa_manager_key}"

    echo
    echo "=== 3/3: enabling APIs, disabling unused, creating kops-cluster-creator ==="
    just gcp-cluster setup-kops-creator --project "${project_id}"

    echo
    echo "Identity chain ready. Rotate to the least-privilege SA, then re-enter the shell:"
    echo "  just gcp-cluster rotate-to-creator"
    echo "  exit"
    echo "  just cluster-env --env <env> --cluster <cluster-name>"

# Rotate the env file + gcloud identity back to the broad sa-manager in one
# call — the inverse of rotate-to-creator, for admin tasks that need SA/KMS
# admin rights (gcp-pulumi bootstrap-backend, creating other SAs). cluster-cred
# edits the env file; configure-gcloud activates the named config. One shell
# boundary is still required AFTER this — re-enter cluster-env so Section 3
# tools (kops/kubectl) pick up the new GOOGLE_APPLICATION_CREDENTIALS.
# Usage: just gcp-cluster rotate-to-manager [--project <project-id>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[group('cluster-management')]
[no-cd]
rotate-to-manager project="":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project." >&2
        exit 1
    fi

    root="{{ invocation_directory() }}"
    manager_key="${root}/credentials/${project_id}/sa-manager.json"

    if [ ! -f "${manager_key}" ]; then
        echo "ERROR: sa-manager key not found: ${manager_key}" >&2
        echo "Create the identity chain first: just gcp-cluster bootstrap-identities" >&2
        exit 1
    fi

    echo "=== 1/2: rotating env file to sa-manager ==="
    just cluster-cred --key "${manager_key}"

    echo
    echo "=== 2/2: activating sa-manager as a named gcloud config ==="
    just gcp-cluster configure-gcloud --name "${project_id}-sa-manager" --project "${project_id}" --key-file "${manager_key}"

    echo
    echo "Rotated. Re-enter the shell so Section 3 tools pick up the new credential:"
    echo "  exit"
    echo "  just cluster-env --env <env> --cluster <cluster-name>"

# Rotate the env file + gcloud identity to the least-privilege kops-cluster-creator
# in one call. cluster-cred edits the env file; configure-gcloud activates the
# named config from the key directly (no GOOGLE_APPLICATION_CREDENTIALS needed).
# One shell boundary is still required AFTER this — re-enter cluster-env so
# Section 3 tools (kops/kubectl) pick up the new GOOGLE_APPLICATION_CREDENTIALS.
# Usage: just gcp-cluster rotate-to-creator [--project <project-id>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[group('cluster-management')]
[no-cd]
rotate-to-creator project="":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "${project_id}" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project." >&2
        exit 1
    fi

    root="{{ invocation_directory() }}"
    creator_key="${root}/credentials/${project_id}/kops-cluster-creator.json"

    echo "=== 1/2: rotating env file to kops-cluster-creator ==="
    just cluster-cred --key "${creator_key}"

    echo
    echo "=== 2/2: activating kops-cluster-creator as a named gcloud config ==="
    just gcp-cluster configure-gcloud --name "${project_id}-kops-cluster-creator" --project "${project_id}" --key-file "${creator_key}"

    echo
    echo "Rotated. Re-enter the shell so Section 3 tools pick up the new credential:"
    echo "  exit"
    echo "  just cluster-env --env <env> --cluster <cluster-name>"

# Push Git-canonical manifests to state storage, preview, apply, and confirm
# zero drift. The repeatable Day-2 loop — folds replace-manifests + plan-cluster
# + update-cluster + drift-manifests. Pass --force yes only for the very first
# replace after bootstrap-bundle (see create-cluster, which does this for you).
# Usage: just gcp-cluster apply-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>] [--force yes]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("force", long="force", short="f", pattern="yes|no", help="Force the manifest replace (only needed on the first push after bootstrap)")]
[group('cluster-management')]
[no-cd]
apply-cluster cluster="" kops_name="" state="" force="no":
    #!/usr/bin/env bash
    set -euo pipefail

    args=()
    [ -n "{{ cluster }}" ] && args+=(--cluster "{{ cluster }}")
    [ -n "{{ kops_name }}" ] && args+=(--kops-name "{{ kops_name }}")
    [ -n "{{ state }}" ] && args+=(--state "{{ state }}")

    replace_args=("${args[@]+"${args[@]}"}")
    [ "{{ force }}" = "yes" ] && replace_args+=(--force yes)

    echo "=== 1/2: pushing manifests to state store ==="
    just gcp-cluster replace-manifests "${replace_args[@]+"${replace_args[@]}"}"

    echo
    echo "=== 2/2: preview, apply, confirm no drift ==="
    just gcp-cluster _plan-update-drift "${args[@]+"${args[@]}"}"

    echo
    echo "Applied cleanly. If machineType, disk, image, or kubernetesVersion changed,"
    echo "also roll the affected instances:"
    echo "  just gcp-cluster rolling-update --yes yes"

# Private helper shared by apply-cluster and create-cluster: preview, apply,
# then confirm zero drift. Does NOT push manifests — callers run
# replace-manifests first, since the target of a fresh replace differs
# (create-cluster needs --force yes on the very first push; apply-cluster does not).
# Usage: just gcp-cluster _plan-update-drift [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[group('cluster-management')]
[no-cd]
_plan-update-drift cluster="" kops_name="" state="":
    #!/usr/bin/env bash
    set -euo pipefail

    args=()
    [ -n "{{ cluster }}" ] && args+=(--cluster "{{ cluster }}")
    [ -n "{{ kops_name }}" ] && args+=(--kops-name "{{ kops_name }}")
    [ -n "{{ state }}" ] && args+=(--state "{{ state }}")

    echo "--- preview (dry-run) ---"
    just gcp-cluster plan-cluster "${args[@]+"${args[@]}"}"

    echo
    echo "--- applying to GCP ---"
    just gcp-cluster update-cluster "${args[@]+"${args[@]}"}"

    echo
    echo "--- confirming zero drift ---"
    just gcp-cluster drift-manifests "${args[@]+"${args[@]}"}"

# Validate cluster health, HA topology, and hardening addons in one pass.
# Folds validate-cluster + validate-kops-ha + validate-hardening.
# Usage: just gcp-cluster verify-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>] [--waittime <min>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("waittime", long="waittime", short="w", help="Minutes to wait for cluster validation (default 20)")]
[group('cluster-management')]
[no-cd]
verify-cluster cluster="" kops_name="" state="" waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail

    args=()
    [ -n "{{ cluster }}" ] && args+=(--cluster "{{ cluster }}")
    [ -n "{{ kops_name }}" ] && args+=(--kops-name "{{ kops_name }}")
    [ -n "{{ state }}" ] && args+=(--state "{{ state }}")

    echo "=== 1/3: cluster health ==="
    just gcp-cluster validate-cluster "${args[@]+"${args[@]}"}" --waittime "{{ waittime }}"

    # Ensure isolated per-cluster kubeconfig exists before running phases 2 & 3
    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    kubeconfig_file="${KUBECONFIG:-}"
    if [ -z "${kubeconfig_file}" ] && [ -n "${c}" ]; then
        kubeconfig_file="{{ invocation_directory() }}/${c}-kubeconfig.yaml"
        export KUBECONFIG="${kubeconfig_file}"
    fi

    if [ -n "${kubeconfig_file}" ] && [ ! -s "${kubeconfig_file}" ]; then
        echo
        echo "Kubeconfig missing at ${kubeconfig_file}; exporting from state store..."
        just gcp-cluster export-kubeconfig "${args[@]+"${args[@]}"}"
    fi

    echo
    echo "=== 2/3: HA topology ==="
    just gcp-cluster validate-kops-ha "${args[@]+"${args[@]}"}"

    echo
    echo "=== 3/3: hardening addons ==="
    just gcp-cluster validate-hardening

    echo
    echo "All validations passed."

# Create (or recreate, after teardown) the state bucket, push manifests, upload
# the SSH secret, apply, and validate — in that order. Folds create-state-bucket
# + replace-manifests(force=yes) + upload-ssh-secret + plan-cluster +
# update-cluster + drift-manifests + verify-cluster into one command.
#
# Order matters: manifests must be pushed to state BEFORE the SSH secret is
# uploaded, since kops needs the cluster object to exist in state first.
#
# This is the SAME recipe for first-time creation and for post-teardown
# recreation — both start from "no live cluster, SSH secret not yet uploaded",
# so the steps and their idempotency requirements are identical. See
# docs/reference/kops/recreation.md.
#
# Print a concise summary of the Git-canonical cluster manifests.
# Standalone offline inspection — zero credentials or GCP calls needed.
# Usage: just gcp-cluster _cluster-config-report [--cluster <name>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[group('cluster-management')]
[no-cd]
_cluster-config-report cluster="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    if [ -z "${c}" ]; then
        echo "ERROR: cluster name required. Pass --cluster or set CLUSTER_NAME." >&2
        exit 1
    fi

    root="{{ invocation_directory() }}"
    bundle_dir="${root}/config/kops/${c}"
    cluster_yaml="${bundle_dir}/cluster.yaml"
    igs_yaml="${bundle_dir}/instancegroups.yaml"

    if [ ! -f "${cluster_yaml}" ] || [ ! -f "${igs_yaml}" ]; then
        echo "ERROR: manifests not found in ${bundle_dir}" >&2
        exit 1
    fi

    p=$(grep -E '^[[:space:]]*project:[[:space:]]*' "${cluster_yaml}" 2>/dev/null | head -n1 | awk '{print $2}' | tr -d '"'\''')
    kn=$(grep -E '^[[:space:]]*name:[[:space:]]*' "${cluster_yaml}" 2>/dev/null | head -n1 | awk '{print $2}' | tr -d '"'\''')
    k8s_ver=$(grep -E '^[[:space:]]*kubernetesVersion:' "${cluster_yaml}" 2>/dev/null | awk '{print $2}' || true)

    api_cidrs=$(awk '/^[[:space:]]*kubernetesApiAccess:/ { in_api=1; next } in_api && /^[[:space:]]*-/ { print $NF; next } in_api { in_api=0 }' "${cluster_yaml}" | tr '\n' ',' | sed 's/,$//; s/,/, /g')
    [ -z "${api_cidrs}" ] && api_cidrs="(none)"

    subnet_cidr=$(grep -E 'cidr:' "${cluster_yaml}" 2>/dev/null | head -n1 | awk '{print $NF}' || true)
    pod_cidr=$(grep -E '^[[:space:]]*nonMasqueradeCIDR:' "${cluster_yaml}" 2>/dev/null | awk '{print $2}' || true)
    cfg_base=$(grep -E '^[[:space:]]*configBase:' "${cluster_yaml}" 2>/dev/null | awk '{print $2}' || true)

    cni="Unknown"
    if grep -qE '^[[:space:]]*cilium:' "${cluster_yaml}"; then
        cni="Cilium"
    elif grep -qE '^[[:space:]]*calico:' "${cluster_yaml}"; then
        cni="Calico"
    elif grep -qE '^[[:space:]]*flannel:' "${cluster_yaml}"; then
        cni="Flannel"
    elif grep -qE '^[[:space:]]*kubenet:' "${cluster_yaml}"; then
        cni="Kubenet"
    fi

    echo "=== Cluster Configuration Summary ==="
    printf "  %-18s %s\n" "Cluster DNS:" "${kn}"
    printf "  %-18s %s\n" "GCP Project:" "${p}"
    printf "  %-18s %s\n" "Kubernetes:" "${k8s_ver}"
    printf "  %-18s %s\n" "API Access CIDR:" "${api_cidrs}"
    printf "  %-18s %s (Pod CIDR: %s)\n" "Networking:" "${cni}" "${pod_cidr}"
    [ -n "${subnet_cidr}" ] && printf "  %-18s %s\n" "VPC Subnet:" "${subnet_cidr}"
    printf "  %-18s %s\n" "State Store:" "${cfg_base}"

    awk '
    function flush() {
        if (name != "") {
            if (role == "Master") {
                cp_count++;
                cp_machine=machine;
                if (zone != "") {
                    if (!(zone in cp_zones_map)) {
                        cp_zones_map[zone] = 1;
                        cp_zones[cp_zone_count++] = zone;
                    }
                }
                initial_vms += min;
                max_vms += max;
            } else {
                workers[worker_count, "name"] = name;
                workers[worker_count, "machine"] = machine;
                workers[worker_count, "min"] = min;
                workers[worker_count, "max"] = max;
                worker_count++;
                initial_vms += min;
                max_vms += max;
            }
        }
        name=""; role=""; machine=""; min=0; max=0; zone=""; in_meta=0; in_spec=0; in_zones=0;
    }
    BEGIN {
        FS=": *";
        cp_count=0;
        cp_machine="";
        cp_zone_count=0;
        worker_count=0;
        initial_vms=0;
        max_vms=0;
    }
    /^metadata:/ { in_meta=1; in_spec=0; next }
    /^spec:/ { in_spec=1; in_meta=0; next }
    /^---/ { flush(); next }
    in_meta && /^[[:space:]]+name:/ { name=$2 }
    in_spec && /^[[:space:]]+role:/ { role=$2 }
    in_spec && /^[[:space:]]+machineType:/ { machine=$2 }
    in_spec && /^[[:space:]]+minSize:/ { min=$2 + 0 }
    in_spec && /^[[:space:]]+maxSize:/ { max=$2 + 0 }
    in_spec && /^[[:space:]]+zones:/ { in_zones=1; next }
    in_spec && in_zones && /^[[:space:]]+-[[:space:]]+/ {
        z=$0;
        sub(/^[[:space:]]*-?[[:space:]]*/, "", z);
        if (zone == "") zone=z;
        next
    }
    in_spec && /^[[:space:]]+[a-zA-Z]/ { in_zones=0 }
    END {
        flush();
        zones_str="";
        for (j=0; j < cp_zone_count; j++) {
            zones_str = (j == 0) ? cp_zones[j] : zones_str ", " cp_zones[j];
        }
        if (zones_str != "") {
            printf "  %-18s %d instances (%s, zones: %s)\n", "Control Plane:", cp_count, cp_machine, zones_str;
        } else {
            printf "  %-18s %d instances (%s)\n", "Control Plane:", cp_count, cp_machine;
        }
        printf "  Worker Pools (%d):\n", worker_count;
        for (i = 0; i < worker_count; i++) {
            w_name = workers[i, "name"];
            w_mach = workers[i, "machine"];
            w_min = workers[i, "min"];
            w_max = workers[i, "max"];
            status = "";
            if (w_max == 0) {
                status = " [LOCKED TO 0]";
            } else if (w_min == w_max) {
                status = " [STATIC]";
            } else {
                status = " [AUTOSCALING]";
            }
            printf "    - %-14s %-18s (size: %d-%d)%s\n", w_name, w_mach, w_min, w_max, status;
        }
        printf "  %-18s %d initial VMs (max: %d)\n", "Provisioning:", initial_vms, max_vms;
    }
    ' "${igs_yaml}"

# Validate all prerequisites before running create-cluster:
# environment variables, toolchain versions, credentials, SSH keypair,
# gcloud identity isolation, Git manifest bundle, and state bucket status.
# Usage: just gcp-cluster preflight-create [--cluster <name>] [--project <id>] [--kops-name <name>] [--state <uri>] [--bucket-name <name>] [--ssh-key <path>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var or gs://kops-state-<cluster>)")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name (defaults to kops-state-<cluster>)")]
[arg("ssh_key", long="ssh-key", short="k", help="SSH public key path (defaults to SSH_KEY env var or credentials/<project>/k8sVM.pub)")]
[group('cluster-management')]
[no-cd]
preflight-create cluster="" project="" kops_name="" state="" bucket_name="" ssh_key="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="{{ cluster }}"
    [ -z "${c}" ] && c="${CLUSTER_NAME:-}"
    p="{{ project }}"
    [ -z "${p}" ] && p="${PROJECT_ID:-}"

    failures=0
    source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
    CHECK_LABEL_WIDTH=24

    echo "=== Pre-flight Cluster Creation Check ==="

    # 1. Core Shell Environment
    if [ -z "${c}" ]; then
        bad "CLUSTER_NAME" "unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>"
    else
        ok "CLUSTER_NAME" "${c}"
    fi

    if [ -z "${p}" ]; then
        bad "PROJECT_ID" "unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>"
    else
        ok "PROJECT_ID" "${p}"
    fi

    if [ -z "${c}" ] || [ -z "${p}" ]; then
        echo
        printf '\033[31mPre-flight check failed with %d error(s).\033[0m Enter cluster shell first: just cluster-env --env <env> --cluster <name>\n' "$failures"
        exit 1
    fi

    kn="{{ kops_name }}"
    [ -z "${kn}" ] && kn="${KOPS_CLUSTER_NAME:-}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    bn="{{ bucket_name }}"
    [ -z "${bn}" ] && bn="${BUCKET_NAME:-}"
    [ -z "${bn}" ] && bn="kops-state-${c}"
    bn="${bn#gs://}"
    bn="${bn%/}"
    bucket_uri="gs://${bn}"

    st="{{ state }}"
    [ -z "${st}" ] && st="${KOPS_STATE_STORE:-}"
    [ -z "${st}" ] && st="gs://${bn}"
    st="${st%/}"

    ssh_pub="{{ ssh_key }}"
    [ -z "${ssh_pub}" ] && ssh_pub="${SSH_KEY:-}"
    [ -z "${ssh_pub}" ] && ssh_pub="{{ invocation_directory() }}/credentials/${p}/k8sVM.pub"

    # 2. Toolchain & Pinned Versions
    echo
    if ! just check-tools; then
        bad "toolchain binaries" "one or more tools missing on PATH — run 'just prepare-tools'"
    else
        ok "toolchain binaries" "all required tools present on PATH"
    fi

    pinfile="${ASDF_DEFAULT_TOOL_VERSIONS_FILENAME:-.tool-versions}"
    if [ -f "${pinfile}" ]; then
        pin_mismatch=0
        while read -r tool pinned_ver; do
            [[ -z "$tool" || "$tool" =~ ^# ]] && continue
            cur_line=$(asdf current "$tool" 2>/dev/null || true)
            cur_ver=$(echo "$cur_line" | awk 'NR>1 {print $2}')
            installed=$(echo "$cur_line" | awk 'NR>1 {print $NF}')
            if [ "$installed" != "true" ] || [ "$cur_ver" != "$pinned_ver" ]; then
                bad "tool pin ${tool}" "installed '${cur_ver}' != pinned '${pinned_ver}' in ${pinfile} — run 'just prepare-tools'"
                pin_mismatch=$((pin_mismatch + 1))
            fi
        done < "${pinfile}"
        if [ "$pin_mismatch" -eq 0 ]; then
            ok "toolchain pins" "all tools match pinned versions in ${pinfile}"
        fi
    fi
    echo

    # 3. Google Application Credentials
    expected_sa="kops-cluster-creator@${p}.iam.gserviceaccount.com"
    cred="${GOOGLE_APPLICATION_CREDENTIALS:-}"
    if [ -z "${cred}" ]; then
        bad "credentials" "GOOGLE_APPLICATION_CREDENTIALS unset"
    elif [ ! -f "${cred}" ]; then
        bad "credentials" "key file not found: ${cred}"
    else
        cred_project=$(jq -r '.project_id // empty' "${cred}" 2>/dev/null || true)
        cred_email=$(jq -r '.client_email // empty' "${cred}" 2>/dev/null || true)
        if [ "${cred_project}" != "${p}" ]; then
            bad "credentials" "key project (${cred_project}) != PROJECT_ID (${p})"
        elif [ "${cred_email}" != "${expected_sa}" ]; then
            bad "credentials" "SA is '${cred_email}', expected '${expected_sa}' — rotate first with 'just gcp-cluster rotate-to-creator'"
        else
            ok "credentials" "${cred_email}"
        fi
    fi

    # 4. GCloud configuration & shell isolation
    expected_cfg="${p}-kops-cluster-creator"
    actual_cfg="${CLOUDSDK_ACTIVE_CONFIG_NAME:-}"
    if [ -z "${actual_cfg}" ]; then
        bad "shell isolation" "CLOUDSDK_ACTIVE_CONFIG_NAME unset — enter cluster shell first: just cluster-env --env <env> --cluster <name>"
    elif [ "${actual_cfg}" != "${expected_cfg}" ]; then
        bad "gcloud config" "CLOUDSDK_ACTIVE_CONFIG_NAME is '${actual_cfg}', expected '${expected_cfg}'"
    else
        cfg_account=$(gcloud config get-value account 2>/dev/null || true)
        cfg_project=$(gcloud config get-value project 2>/dev/null || true)
        if [ "${cfg_project}" != "${p}" ]; then
            bad "gcloud project" "gcloud project is '${cfg_project}', expected '${p}'"
        elif [ "${cfg_account}" != "${expected_sa}" ]; then
            bad "gcloud account" "gcloud account is '${cfg_account}', expected '${expected_sa}'"
        else
            ok "gcloud config" "${actual_cfg} (${cfg_account})"
        fi
    fi

    # 5. SSH key isolation
    if [ ! -f "${ssh_pub}" ]; then
        bad "SSH public key" "not found: ${ssh_pub}"
    else
        ssh_dir_canon="$(cd "$(dirname "${ssh_pub}")" 2>/dev/null && pwd)"
        expected_ssh_canon="$(cd "{{ invocation_directory() }}/credentials/${p}" 2>/dev/null && pwd)"
        if [ "${ssh_dir_canon}" != "${expected_ssh_canon}" ]; then
            bad "SSH key isolation" "parent directory (${ssh_dir_canon}) != expected (credentials/${p})"
        else
            priv_key="${ssh_pub%.pub}"
            if [ ! -f "${priv_key}" ]; then
                bad "SSH private key" "matching private key not found: ${priv_key}"
            else
                ok "SSH keypair" "${ssh_pub}"
            fi
        fi
    fi

    # 6. Git manifest bundle
    root="{{ invocation_directory() }}"
    bundle_dir="${root}/config/kops/${c}"
    cluster_yaml="${bundle_dir}/cluster.yaml"
    igs_yaml="${bundle_dir}/instancegroups.yaml"

    if [ ! -f "${cluster_yaml}" ]; then
        bad "manifests" "missing ${cluster_yaml} — run 'just gcp-cluster bootstrap-bundle' first"
    elif [ ! -f "${igs_yaml}" ]; then
        bad "manifests" "missing ${igs_yaml} — run 'just gcp-cluster bootstrap-bundle' first"
    else
        manifest_project=$(grep -E '^[[:space:]]*project:[[:space:]]*' "${cluster_yaml}" | head -n 1 | awk '{print $2}' | tr -d '"'\''')
        manifest_name=$(grep -E '^[[:space:]]*name:[[:space:]]*' "${cluster_yaml}" | head -n 1 | awk '{print $2}' | tr -d '"'\''')
        manifest_config_base=$(grep -E '^[[:space:]]*configBase:[[:space:]]*' "${cluster_yaml}" | head -n 1 | awk '{print $2}' | tr -d '"'\''')

        if [ "${manifest_project}" != "${p}" ]; then
            bad "manifest project" "cluster.yaml project (${manifest_project}) != PROJECT_ID (${p})"
        elif [ "${manifest_name}" != "${kn}" ]; then
            bad "manifest name" "cluster.yaml metadata.name (${manifest_name}) != ${kn}"
        elif [ -n "${st}" ] && [ "${manifest_config_base}" != "${st}/${kn}" ] && [ "${manifest_config_base}" != "${st}" ]; then
            bad "manifest state" "cluster.yaml configBase (${manifest_config_base}) != ${st}/${kn}"
        elif grep -qE '\$\{[A-Za-z0-9_]+\}' "${cluster_yaml}" "${igs_yaml}"; then
            bad "manifest template" "unresolved \${VAR} placeholders found in bundle"
        else
            ok "manifest bundle" "config/kops/${c}/ (${manifest_name})"
        fi

        # Validate instance group images exist in GCP and are not family references
        while read -r img; do
            [ -z "$img" ] && continue
            img_proj="${img%%/*}"
            img_name="${img#*/}"
            if ! gcloud compute images describe "$img_name" --project "$img_proj" &>/dev/null; then
                if resolved_family=$(gcloud compute images describe-from-family "$img_name" --project "$img_proj" --format="value(name)" 2>/dev/null); then
                    bad "image family" "'${img}' is an image family — pin exact image '${img_proj}/${resolved_family}'"
                else
                    bad "image not found" "image '${img}' not found in GCP project '${img_proj}'"
                fi
            else
                ok "node image" "${img}"
            fi
        done < <(grep -E '^[[:space:]]*image:[[:space:]]*' "${igs_yaml}" | awk '{print $2}' | sort -u)

        # On GCE, kOps converts instance group taints to autoscaler tags with slashes
        # (k8s.io/cluster-autoscaler/node-template/taint/...), which GCE rejects as invalid label keys.
        if grep -qE '^[[:space:]]*taints:' "${igs_yaml}"; then
            bad "GCE taints" "spec.taints found in ${igs_yaml} — GCE rejects autoscaler taint labels containing slashes; use nodeLabels instead"
        fi

        if ! git -C "${root}" diff --quiet "${bundle_dir}"; then
            bad "git status" "uncommitted modifications in config/kops/${c}/ — review and commit first"
        elif ! git -C "${root}" diff --cached --quiet "${bundle_dir}"; then
            bad "git status" "staged uncommitted changes in config/kops/${c}/ — commit first"
        elif [ -n "$(git -C "${root}" status --porcelain "${bundle_dir}" 2>/dev/null)" ]; then
            bad "git status" "untracked files in config/kops/${c}/ — commit or clean first"
        else
            ok "git status" "manifests committed and clean in Git"
        fi
    fi

    # 7. State bucket & cluster status
    bucket_out=$(gcloud storage ls "${bucket_uri}" 2>&1 || true)
    if gcloud storage ls "${bucket_uri}" &>/dev/null; then
        cluster_out=$(gcloud storage ls "${bucket_uri}/${kn}/" 2>&1 || true)
        if gcloud storage ls "${bucket_uri}/${kn}/" &>/dev/null; then
            running_vms=$(gcloud compute instances list --filter="name ~ ${c}" --format="value(name)" 2>/dev/null || true)
            if [ -n "${running_vms}" ]; then
                bad "state store" "cluster '${kn}' already has running VMs in GCP — use 'just gcp-cluster apply-cluster' for updates, or 'delete-cluster' first"
            else
                ok "state store" "bucket exists (${bucket_uri}), manifests in state, compute not yet provisioned (clean to create/resume)"
            fi
        elif echo "${cluster_out}" | grep -qiE "404|not found"; then
            ok "state store" "bucket exists (${bucket_uri}), no active cluster (clean for creation)"
        else
            bad "state store" "error checking cluster in ${bucket_uri}: ${cluster_out}"
        fi
    elif echo "${bucket_out}" | grep -qiE "404|not found"; then
        ok "state store" "bucket does not exist yet (${bucket_uri} will be created by create-cluster)"
    else
        bad "state store" "error querying bucket ${bucket_uri}: ${bucket_out}"
    fi

    echo
    if [ "$failures" -gt 0 ]; then
        printf '\033[31mPre-flight check failed with %d error(s).\033[0m Resolve issues before running create-cluster.\n' "$failures"
        exit 1
    fi

    just gcp-cluster _cluster-config-report --cluster "${c}"

    echo
    printf '\033[32mAll pre-flight checks passed.\033[0m Ready for create-cluster.\n'

# Precondition: config/kops/<cluster>/*.yaml already exists, reviewed, and
# committed (bootstrap-bundle, or already in Git if recreating).
# Usage: just gcp-cluster create-cluster [--cluster <name>] [--project <id>] [--kops-name <name>] [--state <uri>] [--bucket-name <name>] [--ssh-key <path>] [--waittime <min>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name (defaults to kops-state-<cluster>)")]
[arg("ssh_key", long="ssh-key", short="k", help="SSH public key path (defaults to SSH_KEY env var or credentials/<project>/k8sVM.pub)")]
[arg("waittime", long="waittime", short="w", help="Minutes to wait for cluster validation (default 20)")]
[group('cluster-management')]
[no-cd]
create-cluster cluster="" project="" kops_name="" state="" bucket_name="" ssh_key="" waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail

    args=()
    [ -n "{{ cluster }}" ] && args+=(--cluster "{{ cluster }}")
    [ -n "{{ kops_name }}" ] && args+=(--kops-name "{{ kops_name }}")
    [ -n "{{ state }}" ] && args+=(--state "{{ state }}")

    preflight_args=()
    [ -n "{{ cluster }}" ] && preflight_args+=(--cluster "{{ cluster }}")
    [ -n "{{ project }}" ] && preflight_args+=(--project "{{ project }}")
    [ -n "{{ kops_name }}" ] && preflight_args+=(--kops-name "{{ kops_name }}")
    [ -n "{{ state }}" ] && preflight_args+=(--state "{{ state }}")
    [ -n "{{ bucket_name }}" ] && preflight_args+=(--bucket-name "{{ bucket_name }}")
    [ -n "{{ ssh_key }}" ] && preflight_args+=(--ssh-key "{{ ssh_key }}")

    echo "=== 0/5: pre-flight check ==="
    just gcp-cluster preflight-create "${preflight_args[@]+"${preflight_args[@]}"}"

    bucket_args=()
    [ -n "{{ project }}" ] && bucket_args+=(--project "{{ project }}")
    [ -n "{{ bucket_name }}" ] && bucket_args+=(--bucket-name "{{ bucket_name }}")

    ssh_args=("${args[@]+"${args[@]}"}")
    [ -n "{{ ssh_key }}" ] && ssh_args+=(--ssh-key "{{ ssh_key }}")

    echo
    echo "=== 1/5: state bucket ==="
    just gcp-cluster create-state-bucket "${bucket_args[@]+"${bucket_args[@]}"}"

    echo
    echo "=== 2/5: pushing manifests to state store (forced first push) ==="
    just gcp-cluster replace-manifests "${args[@]+"${args[@]}"}" --force yes

    echo
    echo "=== 3/5: SSH secret ==="
    echo "(must come after the manifest push — kops needs the cluster in state first)"
    just gcp-cluster upload-ssh-secret "${ssh_args[@]+"${ssh_args[@]}"}"

    echo
    echo "=== 4/5: preview, apply, confirm no drift ==="
    just gcp-cluster _plan-update-drift "${args[@]+"${args[@]}"}"

    echo
    echo "=== 5/5: validate ==="
    just gcp-cluster verify-cluster "${args[@]+"${args[@]}"}" --waittime "{{ waittime }}"

    echo
    echo "Cluster created and validated."
