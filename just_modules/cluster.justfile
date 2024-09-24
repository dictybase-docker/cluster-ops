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
create-kops-cluster project bucket_name lifecycle_config_file region="US":
    #!/usr/bin/env bash
    set -euo pipefail


    gcloud config set disable_prompts true

    # Check if the bucket exists
    if ! gcloud storage buckets describe "gs://{{ bucket_name }}" --project={{ project }} &>/dev/null; then
        echo "Creating bucket: {{ bucket_name }}"
        gcloud storage buckets create "gs://{{ bucket_name }}" --project={{ project }} --location={{ region }}
        
        # Enable versioning
        gcloud storage buckets update "gs://{{ bucket_name }}" --project={{ project }} --versioning
        
        # Enable soft delete for 30 days
        gcloud storage buckets update "gs://{{ bucket_name }}" --project={{ project }} --soft-delete-duration=30d
        
        # Set lifecycle configuration if the file exists
        if [ -f "{{ lifecycle_config_file }}" ]; then
            echo "Applying lifecycle configuration from {{ lifecycle_config_file }}"
            gcloud storage buckets update "gs://{{ bucket_name }}" --project={{ project }} --lifecycle-file={{ lifecycle_config_file }}
        else
            echo "Lifecycle configuration file {{ lifecycle_config_file }} not found. Skipping lifecycle configuration."
        fi
    else
        echo "Bucket {{ bucket_name }} already exists. Skipping creation."
    fi

    # TODO: Add kops cluster creation steps here
    echo "Bucket setup complete. Ready for kops cluster creation."

    # Check for required environment variables
    required_vars=("KOPS_CLUSTER_NAME" "KOPS_STATE_STORE" "GOOGLE_APPLICATION_CREDENTIALS" "KUBECONFIG" "SSH_KEY" "KUBERNETES_VERSION")
    missing_vars=()

    for var in "${required_vars[@]}"; do
        if [ -z "${!var:-}" ]; then
            missing_vars+=("$var")
        fi
    done

    if [ ${#missing_vars[@]} -ne 0 ]; then
        echo "Error: The following required environment variables are not set:"
        printf '%s\n' "${missing_vars[@]}"
        echo "Please ensure all required variables are set before running this command."
        exit 1
    fi

    for var in "${required_vars[@]}"; do
        echo "$var set to: ${!var}"
    done

    gcloud config set disable_prompts false
