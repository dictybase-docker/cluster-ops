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

[no-cd]
update-cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    kops update cluster --yes --admin

[no-cd]
validate-cluster waittime="20":
    #!/usr/bin/env bash
    set -euo pipefail
    kops validate cluster --wait {{ waittime }}m

[no-cd]
cluster-status:
    #!/usr/bin/env bash
    set -euo pipefail
    kubectl version
    kubectl cluster-info
    kubectl get nodes

[no-cd]
k9s:
    #!/usr/bin/env bash
    set -euo pipefail
    k9s

[no-cd]
export-kubeconfig:
    #!/usr/bin/env bash
    set -euo pipefail
    kops export kubeconfig --admin
