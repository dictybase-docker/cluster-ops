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
# Applies any pending changes to the cluster
# Usage: just update-cluster
[no-cd]
update-cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    kops update cluster --yes --admin

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
