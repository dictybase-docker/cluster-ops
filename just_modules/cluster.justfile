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

# Create a kops cluster
# Sets up the necessary bucket and initiates cluster creation
# Usage: just create-kops-cluster <project> <bucket_name>
[no-cd]
create-kops-cluster project bucket_name:
    #!/usr/bin/env bash
    set -euo pipefail

    # Build the Go binary
    go build -o bin/gcp-tools cmd/gcp/main.go

    # Run the Go command to create or find the kops bucket
    ./bin/gcp-tools find-or-create-kops-bucket --project {{ project }} --bucket {{ bucket_name }}

    echo "Bucket setup complete. Ready for kops cluster createion"


    # Build the kops-cluster-creator binary
    go build -o bin/kops-cluster-creator cmd/kops/main.go

    # Run the kops-cluster-creator command
    ./bin/kops-cluster-creator --project-id {{ project }}

    echo "Kops cluster creation initiated. Please check the logs for details."

    just update-cluster
    just validate-cluster
    just cluster-status

# Update the kops cluster
# Applies pending changes using the appropriate command for the installed kOps version.
# kOps >= 1.31: uses reconcile; kOps <= 1.30: uses update.
# Usage: just update-cluster
[no-cd]
update-cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    KOPS_VERSION=$(kops version --short 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "0.0.0")
    MAJOR=$(echo "$KOPS_VERSION" | cut -d. -f1)
    MINOR=$(echo "$KOPS_VERSION" | cut -d. -f2)
    if [ "$MAJOR" -gt 1 ] || { [ "$MAJOR" -eq 1 ] && [ "$MINOR" -ge 31 ]; }; then
        echo "kOps $KOPS_VERSION detected — using reconcile"
        kops reconcile cluster --yes
    else
        echo "kOps $KOPS_VERSION detected — using legacy update"
        kops update cluster --yes --admin
    fi

# Reconcile the kops cluster (kOps >= 1.31 only)
# Unified command that replaces update + rolling-update.
# Usage: just reconcile-cluster
[no-cd]
reconcile-cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    kops reconcile cluster --yes

# Create and harden the GCS state bucket for kops.
# Enables versioning, uniform bucket-level access, and public access prevention.
# Usage: just create-state-bucket <project> <bucket_name>
[no-cd]
create-state-bucket project bucket_name:
    #!/usr/bin/env bash
    set -euo pipefail
    if gsutil ls -b "gs://{{ bucket_name }}" &>/dev/null; then
        echo "Bucket gs://{{ bucket_name }} already exists — hardening existing bucket."
    else
        gsutil mb -p {{ project }} -l us-central1 "gs://{{ bucket_name }}/"
        echo "Bucket created: gs://{{ bucket_name }}"
    fi
    gsutil versioning set on "gs://{{ bucket_name }}/"
    gsutil uniformbucketlevelaccess set on "gs://{{ bucket_name }}/"
    gsutil pap set enforced "gs://{{ bucket_name }}/"
    echo "State bucket hardened: versioning ON, UBLA ON, PAP enforced."

# Create the kops cluster manifest (no VMs provisioned yet).
# Builds the kops-cluster-creator binary and runs kops create cluster
# with flags drawn from the cluster env file. Does NOT apply or validate.
# Usage: just create-cluster-config <project> <bucket_name>
[no-cd]
create-cluster-config project bucket_name:
    #!/usr/bin/env bash
    set -euo pipefail
    go build -o bin/kops-cluster-creator cmd/kops/main.go
    ./bin/kops-cluster-creator --project-id {{ project }}
    echo "Cluster manifest written to state store. No VMs provisioned yet."
    echo "Next: edit the manifest (just gcp-cluster edit-cluster), configure"
    echo "InstanceGroups (just gcp-cluster apply-instancegroups), then apply"
    echo "(just gcp-cluster update-cluster)."

# Apply checked-in InstanceGroup templates to the cluster.
# Processes templates from config/kops/instancegroups/*.yaml.tmpl
# with envsubst (using current env vars), then applies with kops replace.
# Usage: just apply-instancegroups
[no-cd]
apply-instancegroups:
    #!/usr/bin/env bash
    set -euo pipefail
    TEMPLATE_DIR="{{ invocation_directory() }}/config/kops/instancegroups"
    if [ ! -d "$TEMPLATE_DIR" ]; then
        echo "Error: template directory not found: $TEMPLATE_DIR" >&2
        exit 1
    fi
    echo "Applying InstanceGroup templates from $TEMPLATE_DIR ..."
    for tmpl in "$TEMPLATE_DIR"/*.yaml.tmpl; do
        if [ ! -f "$tmpl" ]; then
            echo "No templates found in $TEMPLATE_DIR" >&2
            exit 1
        fi
        name=$(basename "$tmpl" .yaml.tmpl)
        echo "  Processing: $name"
        envsubst < "$tmpl" | kops replace -f -
        echo "  Applied: $name"
    done
    echo "All InstanceGroups applied."

# List all InstanceGroups with key sizing details.
# Usage: just list-instancegroups
[no-cd]
list-instancegroups:
    #!/usr/bin/env bash
    set -euo pipefail
    kops get instancegroups

# Validate HA production topology.
# Checks: InstanceGroup count, zone distribution, node readiness,
# taints on batch pool, and control-plane zone spread.
# Usage: just validate-kops-ha
[no-cd]
validate-kops-ha:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "=== Validating cluster health ==="
    kops validate cluster --wait 10m
    echo ""
    echo "=== InstanceGroups ==="
    kops get instancegroups
    echo ""
    echo "=== Node zone distribution ==="
    kubectl get nodes -o custom-columns=NAME:.metadata.name,ZONE:.metadata.labels.topology\.kubernetes\.io/zone,POOL:.metadata.labels.pool,TAINTS:.spec.taints[*].key
    echo ""
    echo "=== Batch pool taint check ==="
    if kubectl get nodes -l pool=batch --no-headers 2>/dev/null | grep -q .; then
        kubectl describe nodes -l pool=batch | grep -A1 Taints || echo "WARNING: No taints found on batch nodes"
    else
        echo "No batch nodes (pool may be scaled to 0)."
    fi
    echo ""
    echo "=== Control-plane zone spread ==="
    kubectl get nodes -l node-role.kubernetes.io/control-plane -o custom-columns=NAME:.metadata.name,ZONE:.metadata.labels.topology\.kubernetes\.io/zone
    echo ""
    echo "HA validation complete."

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
