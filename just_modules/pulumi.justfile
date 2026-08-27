# Set up Pulumi with a GCS backend.
# This target sets up a Google Cloud Storage (GCS) bucket for Pulumi state management.
# Usage: just gcp-pulumi pulumi-gcs-setup [--sa-json-path <path>] [--gcs-bucket <bucket>] [--lifecycle-config <path>] [--location <zone>]
[arg("location", long="location", short="l", help="GCS bucket location")]
[arg("gcs_bucket", long="gcs-bucket", short="b", help="GCS bucket name (defaults from PULUMI_BACKEND_URL)")]
[arg("sa_json_path", long="sa-json-path", short="j", help="Path to SA JSON (defaults from PULUMI_GCP_CREDENTIALS)")]
[arg("lifecycle_config", long="lifecycle-config", short="c", help="Path to a lifecycle configuration file (optional)")]
[group('pulumi-management')]
pulumi-gcs-setup sa_json_path="" gcs_bucket="" lifecycle_config="" location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    sa_path="{{ sa_json_path }}"
    if [ -z "$sa_path" ]; then
        sa_path="${PULUMI_GCP_CREDENTIALS:-${GOOGLE_APPLICATION_CREDENTIALS:-}}"
    fi
    if [ -z "$sa_path" ]; then
        echo "ERROR: Service account key required — set PULUMI_GCP_CREDENTIALS or pass --sa-json-path."
        exit 1
    fi
    if [ ! -f "$sa_path" ]; then
        echo "ERROR: Service account key file not found: $sa_path"
        exit 1
    fi
    full_sa_json_path=$(realpath "$sa_path")

    export GOOGLE_APPLICATION_CREDENTIALS="$full_sa_json_path"

    project_id=$(jq -r '.project_id' "$full_sa_json_path")

    bucket="{{ gcs_bucket }}"
    if [ -z "$bucket" ] && [ -n "${PULUMI_BACKEND_URL:-}" ]; then
        bucket="${PULUMI_BACKEND_URL#gs://}"
    fi
    if [ -z "$bucket" ]; then
        bucket="pulumi-state-${project_id}"
    fi

    echo "Using project: $project_id"
    echo "Setting up GCS bucket: ${bucket}"
    echo "Location: {{ location }}"

    if ! gcloud storage buckets describe "gs://${bucket}" --project="$project_id" &>/dev/null; then
        echo "Bucket does not exist. Creating it..."
        gcloud storage buckets create "gs://${bucket}" --project="$project_id" --location="{{ location }}"
        gcloud storage buckets update "gs://${bucket}" --project="$project_id" --versioning
    else
        echo "Bucket already exists."
    fi

    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Applying lifecycle configuration from {{ lifecycle_config }}"
        gcloud storage buckets update "gs://${bucket}" --project="$project_id" --lifecycle-file="{{ lifecycle_config }}"
    fi

    pulumi login "gs://${bucket}"

    echo "Pulumi has been set up to use GCS bucket ${bucket} as the backend in location {{ location }} with object versioning enabled."
    if [ -n "{{ lifecycle_config }}" ]; then
        echo "Lifecycle configuration has been applied from {{ lifecycle_config }}."
    fi

# Select a project's Pulumi stack, initializing it first if it does not exist yet.
# Stack name: --stack flag, else $PULUMI_STACK (set once per cluster by create-cluster-env),
# else the recipe fails — no silent fallback to "dev" for a name this consequential.
# Usage: just gcp-pulumi ensure-stack --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
ensure-stack folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    if [ -z "${stack_name}" ]; then
        stack_name="${PULUMI_STACK:-}"
    fi
    if [ -z "${stack_name}" ]; then
        echo "ERROR: no stack name — set PULUMI_STACK (via cluster env) or pass --stack."
        exit 1
    fi
    if pulumi -C {{ folder }} stack select "${stack_name}" &>/dev/null; then
        echo "Selected existing stack '${stack_name}' in {{ folder }}."
    else
        echo "Stack '${stack_name}' not found in {{ folder }}. Initializing..."
        pulumi -C {{ folder }} stack init "${stack_name}" --secrets-provider "${PULUMI_SECRET_PROVIDER}"
    fi

# Set an encrypted config value on a stack (config set --path --secret).
# Usage: just gcp-pulumi set-secret --folder <dir> --key <config.path> --value <secret> [--stack <name>]
[arg("value", long="value", short="v", help="Secret value to store")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.secret.password")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
set-secret folder key value stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" config set --path --secret "{{ key }}" "{{ value }}"

# Set a plain (unencrypted) config value on a stack (config set --path).
# Usage: just gcp-pulumi set-config --folder <dir> --key <config.path> --value <val> [--stack <name>]
[arg("value", long="value", short="v", help="Config value to store")]
[arg("key", long="key", short="k", help="Config key path, e.g. properties.restoreId")]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
set-config folder key value stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" config set --path "{{ key }}" "{{ value }}"

# Preview Pulumi changes for a stack in a folder.
# Usage: just gcp-pulumi preview --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
preview folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} -s "${stack_name}" preview

# Create a new Pulumi stack in a given folder.
# Usage: just gcp-pulumi new-stack --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
new-stack folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack init "${stack_name}" --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Create a new stack in a folder copied from an existing stack's config.
# Usage: just gcp-pulumi new-stack-from --folder <dir> [--stack <name>] [--from-stack <name>]
[arg("stack", long="stack", short="s", help="New Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[no-cd]
new-stack-from folder stack="" from-stack="experiments":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack init "${stack_name}" --copy-config-from {{ from-stack }} --secrets-provider ${PULUMI_SECRET_PROVIDER}

# Deploy resources for a stack.
# Usage: just gcp-pulumi create-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
create-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} up -s "${stack_name}" -f -y

# Destroy resources for a stack.
# Usage: just gcp-pulumi remove-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
remove-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} destroy -s "${stack_name}" -f -y

# Remove a stack, preserving its config.
# Usage: just gcp-pulumi cleanup-resource --folder <dir> [--stack <name>]
[arg("stack", long="stack", short="s", help="Pulumi stack name (defaults to PULUMI_STACK, else dev)")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[no-cd]
cleanup-resource folder stack="":
    #!/usr/bin/env bash
    set -euo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="${PULUMI_GCP_CREDENTIALS}"
    stack_name="{{ stack }}"
    stack_name="${stack_name:-${PULUMI_STACK:-dev}}"
    pulumi -C {{ folder }} stack rm -s "${stack_name}" --preserve-config --force

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
