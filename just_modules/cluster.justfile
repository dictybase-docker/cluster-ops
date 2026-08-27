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
        project="${PROJECT_ID:-{{ project }}}"
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

    project_id="${PROJECT_ID:-{{ project }}}"
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
    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    [ -n "${c}" ] && export CLUSTER_NAME="${c}"
    [ -n "${kn}" ] && export KOPS_CLUSTER_NAME="${kn}"
    [ -n "${st}" ] && export KOPS_STATE_STORE="${st}"

    if [ -z "${KOPS_CLUSTER_NAME:-}" ] || [ -z "${KOPS_STATE_STORE:-}" ]; then
        echo "ERROR: cluster name (or kops name) and state store must be set or passed via --cluster / --state."
        exit 1
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
    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    [ -n "${c}" ] && export CLUSTER_NAME="${c}"
    [ -n "${kn}" ] && export KOPS_CLUSTER_NAME="${kn}"
    [ -n "${st}" ] && export KOPS_STATE_STORE="${st}"

    if [ -z "${KOPS_CLUSTER_NAME:-}" ] || [ -z "${KOPS_STATE_STORE:-}" ]; then
        echo "ERROR: cluster name (or kops name) and state store must be set or passed via --cluster / --state."
        exit 1
    fi
    ./bin/cluster-ops kops plan

# Create and harden the GCS state bucket for kops.
# Usage: just gcp-cluster create-state-bucket [--project <project-id>] [--bucket-name <bucket>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name (defaults to BUCKET_NAME env var)")]
[no-cd]
create-state-bucket project="" bucket_name="": build
    #!/usr/bin/env bash
    set -euo pipefail
    project_id="${PROJECT_ID:-{{ project }}}"
    bucket="${BUCKET_NAME:-{{ bucket_name }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi
    if [ -z "$bucket" ]; then
        echo "ERROR: no bucket name — set BUCKET_NAME or pass --bucket-name."
        exit 1
    fi
    ./bin/cluster-ops bucket create --project="${project_id}" --bucket="${bucket}" --harden

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
    c="${CLUSTER_NAME:-{{ cluster }}}"
    if [ -n "${c}" ]; then
        export CLUSTER_NAME="${c}"
        kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
        [ -z "${kn}" ] && kn="${c}-k8s.local"
        export KOPS_CLUSTER_NAME="${kn}"
        st="${KOPS_STATE_STORE:-{{ state }}}"
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

    echo "--- Cluster Autoscaler ---"
    if kubectl get pods -n kube-system -l app=cluster-autoscaler 2>/dev/null | grep -q Running; then
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
    if kubectl get pods -n cert-manager 2>/dev/null | grep -q Running; then
        echo "  ✓ Running"
    else
        echo "  ✗ Not found or not Running"
        fail=1
    fi

    echo "--- Node-local DNS cache ---"
    if kubectl get pods -n kube-system -l k8s-app=node-local-dns 2>/dev/null | grep -q Running; then
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
# Usage: just gcp-cluster validate-cluster [--cluster <name>] [--kops-name <name>] [--state <uri>] [waittime]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
validate-cluster cluster="" kops_name="" state="" waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail
    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    name_args=()
    [ -n "${kn}" ] && name_args=("--name=${kn}")
    state_args=()
    [ -n "${st}" ] && state_args=("--state=${st}")

    kops validate cluster "${name_args[@]}" "${state_args[@]}" --wait {{ waittime }}m

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
    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
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

    kops export kubeconfig --admin "${name_args[@]}" "${state_args[@]}" "${kube_args[@]}"

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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    p="${PROJECT_ID:-{{ project }}}"
    st="${KOPS_STATE_STORE:-{{ state }}}"
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    bucket="${BUCKET_NAME:-{{ bucket_name }}}"
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    p="${PROJECT_ID:-{{ project }}}"
    api="${API_ACCESS_CIDR:-{{ api_access_cidr }}}"

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

    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="${KOPS_STATE_STORE:-{{ state }}}"
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    if [ -z "${c}" ]; then
        echo "ERROR: cluster name required. Pass --cluster <name> or set CLUSTER_NAME."
        exit 1
    fi

    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="${KOPS_STATE_STORE:-{{ state }}}"
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
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

    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"

    force_args=()
    if [ "{{ force }}" = "yes" ]; then
        force_args=("--force")
    fi

    echo "Replacing cluster configuration from ${cluster_yaml}..."
    kops replace -f "${cluster_yaml}" --state="${st}" --name="${kn}" "${force_args[@]}"

    echo "Replacing instance groups configuration from ${igs_yaml}..."
    kops replace -f "${igs_yaml}" --state="${st}" --name="${kn}" "${force_args[@]}"
    echo "Manifests replaced in state store: ${st}"

# Diff canonical bundle files against live state storage.
# Usage: just gcp-cluster drift-manifests [--cluster <name>] [--kops-name <name>] [--state <uri>]
[arg("cluster", long="cluster", short="c", help="Cluster name (defaults to CLUSTER_NAME env var)")]
[arg("kops_name", long="kops-name", short="n", help="Full kops DNS name (defaults to KOPS_CLUSTER_NAME env var or <cluster>-k8s.local)")]
[arg("state", long="state", short="s", help="Kops state storage URI (defaults to KOPS_STATE_STORE env var)")]
[no-cd]
drift-manifests cluster="" kops_name="" state="":
    #!/usr/bin/env bash
    set -euo pipefail

    c="${CLUSTER_NAME:-{{ cluster }}}"
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

    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && kn="${c}-k8s.local"

    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && st="gs://kops-state-${c}"

    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir}"' EXIT

    kops get cluster --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/live-cluster.yaml"
    kops get instancegroups --name="${kn}" --state="${st}" -o yaml > "${tmp_dir}/live-igs.yaml"

    rc=0
    echo "--- Checking cluster.yaml drift ---"
    if ! diff -u "${cluster_yaml}" "${tmp_dir}/live-cluster.yaml"; then
        echo "DRIFT detected in cluster.yaml"
        rc=1
    else
        echo "cluster.yaml matches live state."
    fi

    echo "--- Checking instancegroups.yaml drift ---"
    if ! diff -u "${igs_yaml}" "${tmp_dir}/live-igs.yaml"; then
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
    [ -z "${st}" ] && [ -n "${c}" ] && st="gs://kops-state-${c}"

    key="${SSH_KEY:-{{ ssh_key }}}"
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

    kops create secret sshpublickey "${kn}" --state="${st}" -i "${key}"
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

    c="${CLUSTER_NAME:-{{ cluster }}}"
    kn="${KOPS_CLUSTER_NAME:-{{ kops_name }}}"
    [ -z "${kn}" ] && [ -n "${c}" ] && kn="${c}-k8s.local"
    st="${KOPS_STATE_STORE:-{{ state }}}"
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
    kops rolling-update cluster "${kn}" --state="${st}" "${cmd_args[@]}"


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
[arg("name", long="name", short="n", help="gcloud configuration name (default sa-manager)")]
[arg("project", long="project", short="p", help="GCP project ID (defaults to PROJECT_ID)")]
[arg("key_file", long="key-file", short="k", help="SA JSON key (defaults to GOOGLE_APPLICATION_CREDENTIALS)")]
[arg("zone", long="zone", short="z", help="Default compute zone (default us-central1-c)")]
[group('cluster-management')]
[no-cd]
configure-gcloud name="sa-manager" project="" key_file="" zone="us-central1-c":
    #!/usr/bin/env bash
    set -euo pipefail

    CFG="{{ name }}"
    ZONE="{{ zone }}"

    PROJECT="{{ project }}"
    [[ -z "$PROJECT" ]] && PROJECT="${PROJECT_ID:-}"
    if [[ -z "$PROJECT" ]]; then
        echo "Error: no project — pass --project or enter the cluster shell so PROJECT_ID is set." >&2
        exit 1
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
        gcloud config configurations create "$CFG"
    fi

    gcloud config configurations activate "$CFG"
    gcloud auth activate-service-account "$SA_EMAIL" --key-file="$KEY"
    gcloud config set project "$PROJECT"
    gcloud config set compute/zone "$ZONE"

    echo
    echo "Active gcloud configuration:"
    gcloud config configurations list --filter="name=$CFG" 2>/dev/null || true
