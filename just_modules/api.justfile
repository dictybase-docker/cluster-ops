# Enable multiple Google Cloud API services from a file.
# Usage: just gcp-api enable-apis [--project <project-id>] --api-file <path>
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("api_file", long="api-file", short="f", help="Path to the API list file")]
[group('api-management')]
[no-cd]
enable-apis project="" api_file:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    echo "Enabling APIs for project ${project_id} from file {{ api_file }}"

    go build -o bin/cluster-ops cmd/cluster-ops/main.go
    ./bin/cluster-ops api enable --project="${project_id}" --api-file-path={{ api_file }}
    echo "Finished enabling APIs"
    echo "Ran commands enabled APIs in project ${project_id}:"

# List enabled APIs in a Google Cloud project and write their names to a file.
# Usage: just gcp-api list-enabled-apis [--project <project-id>] --output-file <path>
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("output_file", long="output-file", short="o", help="File to write enabled API names to")]
[group('api-management')]
[no-cd]
list-enabled-apis project="" output_file:
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    echo "Listing enabled APIs for project ${project_id}"

    gcloud services list --project="${project_id}" \
        --enabled \
        --format="value(config.name)" > "{{ output_file }}"

    api_count=$(wc -l < "{{ output_file }}" | tr -d ' ')

    echo "Wrote $api_count enabled API names to {{ output_file }}"

    echo "First few enabled APIs:"
    head -n 5 "{{ output_file }}"

    if [ "$api_count" -gt 5 ]; then
        echo "... (and $(($api_count - 5)) more)"
    fi

# Disable multiple Google Cloud API services from a file.
# Usage: just gcp-api disable-apis [--project <project-id>] --api-file <path> [--disable-dependent-services]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("api_file", long="api-file", short="f", help="Path to the API list file")]
[arg("disable_dependent", long="disable-dependent-services", help="Also disable services that depend on the listed APIs")]
[group('api-management')]
[no-cd]
disable-apis project="" api_file disable_dependent="false":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="{{ project }}"
    [ -z "${project_id}" ] && project_id="${PROJECT_ID:-}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    echo "Disabling APIs for project ${project_id} from file {{ api_file }}"

    go build -o bin/cluster-ops cmd/cluster-ops/main.go
    if [ "{{ disable_dependent }}" = "true" ]; then
        ./bin/cluster-ops api disable --project="${project_id}" --api-file-path={{ api_file }} --disable-dependent-services
    else
        ./bin/cluster-ops api disable --project="${project_id}" --api-file-path={{ api_file }}
    fi

    echo "Finished disabling APIs"

    echo "Currently enabled APIs in project ${project_id}:"
    gcloud services list --project="${project_id}" --enabled --format="table(config.name,config.title)"

# Authenticate with gcloud using a service account JSON key file and set up a named configuration.
# Usage: just gcp-api gcloud-auth-sa --key-file <path> --config-name <name> [--zone <zone>]
[arg("zone", long="zone", short="z", help="GCP compute zone")]
[arg("key_file", long="key-file", short="k", help="Service account JSON key file")]
[arg("config_name", long="config-name", short="c", help="gcloud named configuration to create")]
[group('authentication-and-configuration')]
[no-cd]
gcloud-auth-sa key_file config_name zone="us-central1-c":
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Authenticating with gcloud using service account key from {{ key_file }}"

    gcloud config configurations create {{ config_name }}

    gcloud auth activate-service-account --key-file={{ key_file }}

    project=$(jq -r '.project_id' {{ key_file }})
    account=$(jq -r '.client_email' {{ key_file }})
    gcloud config set account $account
    gcloud config set project $project
    gcloud config set compute/zone {{ zone }}

    echo "Authentication complete. Configuration '{{ config_name }}' is set to project $project with account $account and zone {{ zone }}"

    gcloud config list

# Print the properties of the currently active gcloud configuration.
[group('authentication-and-configuration')]
gcloud-active-config:
    #!/usr/bin/env bash
    set -euo pipefail

    echo "Current gcloud configuration:"
    echo "-----------------------------"

    active_config=$(gcloud config configurations list --filter="IS_ACTIVE=true" --format="value(name)")
    echo "Active Configuration: $active_config"
    echo ""

    active_sa=$(gcloud auth list --filter="status:ACTIVE AND account~@.*\.iam\.gserviceaccount\.com" --format="value(account)")
    if [ -n "$active_sa" ]; then
        echo "Active Service Account: $active_sa"
    else
        echo "No active service account"
    fi
