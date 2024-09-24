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

    sa_name="cloud-manager"
    sa_email="${sa_name}@{{ project }}.iam.gserviceaccount.com"

    # Check if the service account already exists
    if ! gcloud iam service-accounts describe "$sa_email" --project={{ project }} &>/dev/null; then
        echo "Creating service account: $sa_name"
        just gcp-sa create-sa {{ project }} \
            $sa_name 'service account to manage cloud resources'
    else
        echo "Service account $sa_name already exists. Skipping creation."
    fi

    just gcp-role assign-roles-to-sa {{ project }} $sa_name {{ invocation_directory() }}/gcs-files/roles-permissions/${sa_name}-roles.txt
    just gcp-sa create-sa-key {{ project }} $sa_name \
        {{ invocation_directory() }}/credentials/{{ project }}-$sa_name.json

    gcloud config set disable_prompts false
