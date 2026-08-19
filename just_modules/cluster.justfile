# Build the unified cluster-ops binary.
# Usage: just build
[group('setup-tools')]
build:
    go build -o bin/cluster-ops ./cmd/cluster-ops

# Generate a per-project SSH keypair for kops node access (Section 1.6).
# Key path: SSH_KEY env var if set (takes precedence), else credentials/<project>/k8sVM.
# Options: -p/--project <project-id>, -t/--type ed25519|rsa (default ed25519).
# Refuses to overwrite an existing keypair.
# Usage: just gcp-cluster generate-ssh-key [--project <project-id>] [--type ed25519|rsa]
[arg("project", long="project", short="p", help="GCP project id; builds credentials/<project>/k8sVM when SSH_KEY is unset")]
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
        if [ -z "{{ project }}" ]; then
            echo "ERROR: no key path. Set SSH_KEY or pass --project <project-id>."
            exit 1
        fi
        key_path="${root}/credentials/{{ project }}/k8sVM"
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
        echo "Add this to your per-cluster env file (.env.<env>.<cluster>):"
        echo "  SSH_KEY=\${PWD}/credentials/{{ project }}/k8sVM.pub"
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

# Update the kops cluster
# Thin wrapper — delegates version detection and dispatch to cluster-ops.
# Usage: just update-cluster
[no-cd]
update-cluster: build
    ./bin/cluster-ops kops update

# Reconcile the kops cluster (kOps >= 1.31 only)
# Thin wrapper — delegates to cluster-ops.
# Usage: just reconcile-cluster
[no-cd]
reconcile-cluster: build
    ./bin/cluster-ops kops update

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

# Create the kops cluster manifest (no VMs provisioned yet).
# Usage: just gcp-cluster create-cluster-config [--project <project-id>] [--bucket-name <bucket>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="State bucket name (defaults to BUCKET_NAME env var)")]
[no-cd]
create-cluster-config project="" bucket_name="": build
    #!/usr/bin/env bash
    set -euo pipefail
    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi
    ./bin/cluster-ops kops create-config --project-id="${project_id}"

# Apply checked-in InstanceGroup templates to the cluster.
# Thin wrapper — delegates to cluster-ops.
# Usage: just apply-instancegroups
[no-cd]
apply-instancegroups: build
    ./bin/cluster-ops ig apply

# List all InstanceGroups with key sizing details.
# Thin wrapper — delegates to cluster-ops.
# Usage: just list-instancegroups
[no-cd]
list-instancegroups: build
    ./bin/cluster-ops ig list

# Validate HA production topology.
# Thin wrapper — delegates to cluster-ops.
# Usage: just validate-kops-ha
[no-cd]
validate-kops-ha: build
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
# Usage: just validate-cluster [waittime]
[no-cd]
validate-cluster waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail
    kops validate cluster --wait {{ waittime }}m

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
# Usage: just export-kubeconfig
[no-cd]
export-kubeconfig:
    #!/usr/bin/env bash
    set -euo pipefail
    kops export kubeconfig --admin

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
edit-cluster:
    kops edit cluster

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

# Teardown: dry-run preview or full destroy for the current cluster.
# Without argument (or anything other than "yes"): dry-run only — shows
# what kOps will destroy without touching anything.
# With "yes": preflight check → execute teardown → verify cleanup.
#
# Usage: just gcp-cluster delete-cluster               (dry-run — safe)
#       just gcp-cluster delete-cluster --confirm yes  (full destroy)
[arg("confirm", long="confirm", short="c", help="Set to 'yes' to destroy after the dry-run; anything else dry-runs")]
[no-cd]
delete-cluster confirm="no":
    #!/usr/bin/env bash
    set -euo pipefail

    # ── Preflight ──────────────────────────────
    echo "=== Cluster teardown ==="
    echo "Cluster:  ${KOPS_CLUSTER_NAME:-UNSET}"
    echo "Project:  ${PROJECT_ID:-UNSET}"
    echo "State:    ${KOPS_STATE_STORE:-UNSET}"
    echo ""

    if [ -z "${KOPS_CLUSTER_NAME:-}" ] || [ -z "${KOPS_STATE_STORE:-}" ]; then
        echo "ERROR: KOPS_CLUSTER_NAME and KOPS_STATE_STORE must be set."
        echo "Run: just cluster-env --env <env> --cluster <cluster-name>"
        exit 1
    fi

    # ── Dry-run ────────────────────────────────
    echo "--- Dry-run: resources kOps will destroy ---"
    kops delete cluster \
        --name="${KOPS_CLUSTER_NAME}" \
        --state="${KOPS_STATE_STORE}"
    echo ""

    # ── Confirm or bail ────────────────────────
    if [ "{{ confirm }}" != "yes" ]; then
        echo "Dry-run complete. No resources were touched."
        echo ""
        echo "To destroy the cluster, review the list above, then run:"
        echo "  just gcp-cluster delete-cluster --confirm yes"
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
    echo "Destroying cluster ${KOPS_CLUSTER_NAME}..."
    kops delete cluster \
        --name="${KOPS_CLUSTER_NAME}" \
        --state="${KOPS_STATE_STORE}" \
        --yes

    # ── Verify cleanup ─────────────────────────
    echo ""
    echo "=== Cleanup verification ==="
    echo ""

    echo "Instances:"
    gcloud compute instances list \
        --project="${PROJECT_ID}" \
        --filter="name:${KOPS_CLUSTER_NAME}" \
        2>/dev/null || echo "  (gcloud unavailable)"
    echo ""

    echo "Disks (check for orphaned PVs):"
    gcloud compute disks list \
        --project="${PROJECT_ID}" \
        --filter="name:${KOPS_CLUSTER_NAME}" \
        2>/dev/null || echo "  (gcloud unavailable)"
    echo ""

    echo "Forwarding rules:"
    gcloud compute forwarding-rules list \
        --project="${PROJECT_ID}" \
        2>/dev/null | { grep -E "NAME|${KOPS_CLUSTER_NAME}" || echo "  None matching."; }
    echo ""

    echo "Addresses:"
    gcloud compute addresses list \
        --project="${PROJECT_ID}" \
        2>/dev/null || echo "  (gcloud unavailable)"
    echo ""

    echo "Teardown complete."

# Save the current cluster manifest for version-controlled reproducibility.
# Exports the live cluster spec as YAML so you can diff against it on re-creation.
# Run this after every successful bootstrap or manifest edit.
#
# Usage: just save-cluster-manifest
[no-cd]
save-cluster-manifest:
    #!/usr/bin/env bash
    set -euo pipefail
    OUT="{{ invocation_directory() }}/config/kops/cluster-manifest.yaml"
    mkdir -p "$(dirname "$OUT")"
    kops get cluster -o yaml > "$OUT"
    echo "Cluster manifest saved to: $OUT"
    echo "Commit it:  git add $OUT && git commit -m 'save known-good cluster manifest'"

# Diff the live cluster manifest against the saved copy.
# Run before re-creating to see what edits you need to re-apply in Phase 4c.
# Exits non-zero when differences exist (standard diff behaviour).
#
# Usage: just diff-cluster-manifest
[no-cd]
diff-cluster-manifest:
    #!/usr/bin/env bash
    set -euo pipefail
    SAVED="{{ invocation_directory() }}/config/kops/cluster-manifest.yaml"
    if [ ! -f "$SAVED" ]; then
        echo "ERROR: No saved manifest at $SAVED"
        echo "Run: just gcp-cluster save-cluster-manifest"
        exit 1
    fi
    echo "--- Live cluster  |  Saved manifest  ---"
    kops get cluster -o yaml | diff "$SAVED" - && echo "No differences — manifest matches."

# Full cluster re-creation after teardown.
# Prerequisite: state bucket exists (not deleted during teardown).
# Runs: create-config → diff manifest → edit (manual) → apply-ig → update → validate.
#
# Usage: just recreate-cluster
[no-cd]
recreate-cluster:
    #!/usr/bin/env bash
    set -euo pipefail

    if [ -z "${PROJECT_ID:-}" ] || [ -z "${BUCKET_NAME:-}" ]; then
        echo "ERROR: PROJECT_ID and BUCKET_NAME must be set."
        echo "Run: just cluster-env --env <env> --cluster <cluster-name>"
        exit 1
    fi

    echo "=== Step 1/6: Generate cluster manifest ==="
    just gcp-cluster create-cluster-config --project "${PROJECT_ID}" --bucket-name "${BUCKET_NAME}"
    echo ""

    echo "=== Step 2/6: Diff against saved manifest ==="
    SAVED="{{ invocation_directory() }}/config/kops/cluster-manifest.yaml"
    if [ -f "$SAVED" ]; then
        kops get cluster -o yaml | diff "$SAVED" - && \
            echo "Manifest matches saved copy — no edits needed." || \
            echo "Differences found. Review the diff above before editing."
    else
        echo "No saved manifest at $SAVED — skipping diff."
        echo "After this bootstrap, run 'just gcp-cluster save-cluster-manifest' to save it."
    fi
    echo ""

    echo "=== Step 3/6: Edit cluster manifest ==="
    echo "Opening editor for Phase 4c security settings..."
    echo "Apply: kubernetesApiAccess, etcd volume types (pd-ssd), node SA,"
    echo "       Cluster Autoscaler, Node Problem Detector, cert-manager, node-local DNS."
    just gcp-cluster edit-cluster
    echo ""

    echo "=== Step 4/6: Apply InstanceGroups ==="
    just gcp-cluster apply-instancegroups
    echo ""

    echo "=== Step 5/6: Provision cluster ==="
    just gcp-cluster update-cluster
    echo ""

    echo "=== Step 6/6: Validate HA topology ==="
    just gcp-cluster validate-kops-ha
    echo ""

    echo "Re-creation complete."
    echo "Run 'just gcp-cluster save-cluster-manifest' to update the saved manifest."
