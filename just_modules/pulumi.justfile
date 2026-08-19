# Set up Pulumi with a GCS backend.
# This target sets up a Google Cloud Storage (GCS) bucket for Pulumi state management.
# Usage: just gcp-pulumi pulumi-gcs-setup --sa-json-path <path> --gcs-bucket <bucket> [--lifecycle-config <path>] [--location <zone>]
[arg("location", long="location", short="l", help="GCS bucket location")]
[arg("gcs_bucket", long="gcs-bucket", short="b", help="GCS bucket name for Pulumi state")]
[arg("sa_json_path", long="sa-json-path", short="j", help="Path to the service account JSON file")]
[arg("lifecycle_config", long="lifecycle-config", short="c", help="Path to a lifecycle configuration file (optional)")]
[group('pulumi-management')]
pulumi-gcs-setup sa_json_path gcs_bucket lifecycle_config="" location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    full_sa_json_path=$(realpath "{{ sa_json_path }}")

    export GOOGLE_APPLICATION_CREDENTIALS="$full_sa_json_path"

    project_id=$(jq -r '.project_id' "$full_sa_json_path")

    echo "Using project: $project_id"
    echo "Setting up GCS bucket: {{ gcs_bucket }}"
    echo "Location: {{ location }}"

    if ! gcloud storage buckets describe "gs://{{ gcs_bucket }}" --project="$project_id" &>/dev/null; then
        echo "Bucket does not exist. Creating it..."
        gcloud storage buckets create "gs://{{ gcs_bucket }}" --project="$project_id" --location="{{ location }}"
        gcloud storage buckets update "gs://{{ gcs_bucket }}" --project="$project_id" --versioning
    else
        echo "Bucket already exists."
    fi

    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Applying lifecycle configuration from {{ lifecycle_config }}"
        gcloud storage buckets update "gs://{{ gcs_bucket }}" --project="$project_id" --lifecycle-file="{{ lifecycle_config }}"
    fi

    pulumi login "gs://{{ gcs_bucket }}"

    echo "Pulumi has been set up to use GCS bucket {{ gcs_bucket }} as the backend in location {{ location }} with object versioning enabled."
    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Lifecycle configuration has been applied from {{ lifecycle_config }}."
    fi

# Preview Pulumi changes for a stack in a folder.
# Usage: just gcp-pulumi preview --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
preview folder stack="dev":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} -s {{ stack }} preview

# Create a new Pulumi stack in a given folder.
# Usage: just gcp-pulumi new-stack --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
new-stack folder stack="dev":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} stack init {{ stack }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Create a new stack in a folder copied from an existing stack's config.
# Usage: just gcp-pulumi new-stack-from --folder <dir> [--stack <name>] [--from-stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[no-cd]
new-stack-from folder stack="dev" from-stack="experiments":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} stack init {{ stack }} --copy-config-from {{ from-stack }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Deploy resources for a stack.
# Usage: just gcp-pulumi create-resource --folder <dir> --stack <name>
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
create-resource folder stack:
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} up -s {{ stack }} -f -y

# Destroy resources for a stack.
# Usage: just gcp-pulumi remove-resource --folder <dir> --stack <name>
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
remove-resource folder stack:
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} destroy -s {{ stack }} -f -y

# Remove a stack, preserving its config.
# Usage: just gcp-pulumi cleanup-resource --folder <dir> --stack <name>
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
cleanup-resource folder stack:
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    pulumi -C {{ folder }} stack rm -s {{ stack }} --preserve-config --force

# Create resources for multiple projects listed in a file.
# Usage: just gcp-pulumi create-multiple-resources --stack <name> --from-stack <name> --resources-file <path>
[arg("stack", long="stack", short="s", help="Pulumi stack name")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[arg("resources_file", long="resources-file", short="r", help="File listing resource projects")]
[no-cd]
create-multiple-resources stack from-stack resources_file:
    #!/usr/bin/env bash
    set -euo pipefail

    if [[ ! -f "{{ resources_file }}" ]]; then
        echo "Error: Resources file '{{ resources_file }}' not found"
        exit 1
    fi

    echo "Reading resources from: {{ resources_file }}"

    total_projects=$(grep -v "^$" "{{ resources_file }}" | wc -l)
    current=0

    while IFS= read -r project || [[ -n "$project" ]]; do
        if [[ -z "$project" || "${project:0:1}" == "#" ]]; then
            continue
        fi

        ((current++))
        echo "[${current}/${total_projects}] Processing project: $project"

        echo "Creating stack {{ stack }} for project $project from {{ from-stack }}"
        if ! just gcp-pulumi new-stack-from --folder "$project" --stack "{{ stack }}" --from-stack "{{ from-stack }}"; then
            echo "Failed to create stack for $project. Continuing with next project..."
            continue
        fi

        echo "Creating resources for project $project in stack {{ stack }}"
        if ! just gcp-pulumi create-resource --folder "$project" --stack "{{ stack }}"; then
            echo "Failed to create resources for $project. Continuing with next project..."
            continue
        fi

        echo "Successfully deployed $project"
    done < "{{ resources_file }}"

    echo "Deployment process completed!"
