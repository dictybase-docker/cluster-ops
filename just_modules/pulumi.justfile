# Set up Pulumi with GCS backend
# This target sets up a Google Cloud Storage (GCS) bucket for Pulumi state management
# Parameters:
#   sa_json_path: Path to the service account JSON file
#   gcs_bucket: Name of the GCS bucket to create or use
#   lifecycle_config: Path to the lifecycle configuration file (optional)
#   location: GCS bucket location (default: us-central1)
[group('pulumi-management')]
pulumi-gcs-setup sa_json_path gcs_bucket lifecycle_config location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true
    
    # Expand sa_json_path to full path
    full_sa_json_path=$(realpath "{{sa_json_path}}")
    
    # Export the service account JSON file path
    export GOOGLE_APPLICATION_CREDENTIALS="$full_sa_json_path"
    
    # Extract project ID from the service account JSON
    project_id=$(jq -r '.project_id' "$full_sa_json_path")
    
    echo "Using project: $project_id"
    echo "Setting up GCS bucket: {{gcs_bucket}}"
    echo "Location: {{location}}"
    
    # Check if the bucket exists, create it if it doesn't
    if ! gcloud storage buckets describe "gs://{{gcs_bucket}}" --project="$project_id" &>/dev/null; then
        echo "Bucket does not exist. Creating it..."
        gcloud storage buckets create "gs://{{gcs_bucket}}" --project="$project_id" --location="{{location}}" 
        gcloud storage buckets update "gs://{{gcs_bucket}}" --project="$project_id" --versioning
    else
        echo "Bucket already exists."
    fi
    
    # Apply lifecycle configuration if provided
    if [ -n "{{lifecycle_config}}" ]; then
        echo "Applying lifecycle configuration from {{lifecycle_config}}"
        gcloud storage buckets update "gs://{{gcs_bucket}}" --project="$project_id" --lifecycle-file="{{lifecycle_config}}"
    fi
    
    # Set up Pulumi to use the GCS backend
    pulumi login "gs://{{gcs_bucket}}"
    
    echo "Pulumi has been set up to use GCS bucket {{gcs_bucket}} as the backend in location {{location}} with object versioning enabled."
    if [ -n "{{lifecycle_config}}" ]; then
        echo "Lifecycle configuration has been applied from {{lifecycle_config}}."
    fi

# Preview Pulumi changes
# This target previews Pulumi changes for a specific stack in a given folder
# Parameters:
#   folder: The folder containing the Pulumi project
#   stack: The name of the Pulumi stack (default: dev)
[no-cd]
preview folder stack="dev":
	#!/usr/bin/env bash
	set -euo pipefail
	export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
	pulumi -C {{ folder }} -s {{ stack }} preview

# This target creates a new Pulumi stack in a given folder
# Parameters:
#   folder: The folder containing the Pulumi project
#   stack: The name of the new Pulumi stack (default: dev)
[no-cd]
new-stack folder stack="dev":
	#!/usr/bin/env bash
	set -euo pipefail
	export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
	pulumi -C {{ folder }} stack init {{ stack }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

[no-cd]
create-resource folder stack:
	#!/usr/bin/env bash
	set -euo pipefail
	export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
	pulumi -C {{ folder }} up -s {{ stack }} -f -y


[no-cd]
remove-resource folder stack:
	#!/usr/bin/env bash
	set -euo pipefail
	export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
	pulumi -C {{ folder }} destroy -s {{ stack }} -f -y

[no-cd]
cleanup-resource folder stack:
	#!/usr/bin/env bash
	set -euo pipefail
	export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
	pulumi -C {{ folder }} stack rm -s {{ stack }} --preserve-config --force
