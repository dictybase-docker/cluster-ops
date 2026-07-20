# Build the unified cluster-ops binary.
# Usage: just build
[group('setup-tools')]
build:
    go build -o bin/cluster-ops ./cmd/cluster-ops

# Set up service accounts for the project
# Activates necessary APIs if specified
# Usage: just sa-accounts-setup <project> [activate_api]
[no-cd]
sa-accounts-setup project activate_api="true":
    #!/usr/bin/env bash
    set -euo pipefail

    gcloud config set disable_prompts true

    if [ "{{ activate_api }}" = "true" ]; then
        just gcp-api enable-apis {{ project }} \
             {{ invocation_directory() }}/gcs-files/apis/enabled_apis.txt
        sleep 10
    fi

    sa_accounts=("cloud-manager" "cluster-backup" "database-backup" "deploy-manager" "kops-cluster-creator")
    for sa_name in "${sa_accounts[@]}"; do
        sa_email="${sa_name}@{{ project }}.iam.gserviceaccount.com"

        # Check if the service account already exists
        if ! gcloud iam service-accounts describe "$sa_email" --project={{ project }} &>/dev/null; then
            echo "Creating service account: $sa_name"
            just gcp-sa create-sa {{ project }} \
                $sa_name "service account for ${sa_name}"
        else
            echo "Service account $sa_name already exists. Skipping creation."
        fi

        just gcp-role assign-roles-to-sa {{ project }} $sa_name {{ invocation_directory() }}/gcs-files/roles-permissions/${sa_name}-roles.txt
        just gcp-sa create-sa-key {{ project }} $sa_name \
            {{ invocation_directory() }}/credentials/{{ project }}-$sa_name.json
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
# Thin wrapper — delegates to cluster-ops.
# Usage: just create-state-bucket <project> <bucket_name>
[no-cd]
create-state-bucket project bucket_name: build
    ./bin/cluster-ops bucket create --project={{ project }} --bucket={{ bucket_name }} --harden

# Create the kops cluster manifest (no VMs provisioned yet).
# Thin wrapper around cluster-ops binary.
# Usage: just create-cluster-config <project> <bucket_name>
[no-cd]
create-cluster-config project bucket_name: build
    ./bin/cluster-ops kops create-config --project-id={{ project }}

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

# Export a kubeconfig file with a custom name and duration
# Usage: just export-named-kubeconfig <name> [duration_hours]
[no-cd]
export-named-kubeconfig name duration_hours="24":
    #!/usr/bin/env bash
    set -euo pipefail
    kops export kubeconfig --kubeconfig="{{ name }}.yaml" \
        --admin={{ duration_hours }}h

# Export a kubeconfig file with a given name and custom hour duration

# Extract logs from pods in the cluster
# Usage: just extract-logs <label> [namespace]
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
    just gcp-pulumi preview install-velero experiments
    just gcp-pulumi create-resource install-velero experiments

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
# Usage: just delete-cluster         (dry-run — safe)
#        just delete-cluster yes     (full destroy)
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
        echo "Run: just cluster-env <env> <cluster-name>"
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
        echo "  just gcp-cluster delete-cluster yes"
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
        echo "Run: just cluster-env <env> <cluster-name>"
        exit 1
    fi

    echo "=== Step 1/6: Generate cluster manifest ==="
    just gcp-cluster create-cluster-config "${PROJECT_ID}" "${BUCKET_NAME}"
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
