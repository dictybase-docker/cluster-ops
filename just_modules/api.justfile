# Enable multiple Google Cloud API services from a file
[group('api-management')]
[no-cd]
enable-apis project api_file:
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true
    
    echo "Enabling APIs for project {{project}} from file {{api_file}}"
    
    # Build and run cluster-ops
    go build -o bin/cluster-ops cmd/cluster-ops/main.go
    ./bin/cluster-ops api enable --project={{ project }} --api-file-path={{ api_file }}
    echo "Finished enabling APIs"
    
    # List enabled APIs
    echo "Ran commands enabled APIs in project {{project}}:"

# List enabled APIs in a Google Cloud project and write their names to a file
[group('api-management')]
[no-cd]
list-enabled-apis project output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Listing enabled APIs for project {{project}}"
    
    # Fetch enabled APIs and write their names to the output file
    gcloud services list --project="{{project}}" \
        --enabled \
        --format="value(config.name)" > "{{output_file}}"
    
    # Count the number of enabled APIs
    api_count=$(wc -l < "{{output_file}}" | tr -d ' ')
    
    echo "Wrote $api_count enabled API names to {{output_file}}"
    
    # Display the first few lines of the file
    echo "First few enabled APIs:"
    head -n 5 "{{output_file}}"
    
    if [ "$api_count" -gt 5 ]; then
        echo "... (and $(($api_count - 5)) more)"
    fi

# Disable multiple Google Cloud API services from a file
[group('api-management')]
[no-cd]
disable-apis project api_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Disabling APIs for project {{project}} from file {{api_file}}"
    
    # Build and run cluster-ops
    go build -o bin/cluster-ops cmd/cluster-ops/main.go
    ./bin/cluster-ops api disable --project={{ project }} --api-file-path={{ api_file }}

    echo "Finished disabling APIs"
    
    # List remaining enabled APIs
    echo "Currently enabled APIs in project {{project}}:"
    gcloud services list --project="{{project}}" --enabled --format="table(config.name,config.title)"


# Authenticate with gcloud using a service account JSON key file and set up a named configuration
[group('authentication-and-configuration')]
[no-cd]
gcloud-auth-sa key_file config_name zone="us-central1-c":
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Authenticating with gcloud using service account key from {{key_file}}"
    
    # Create a new configuration
    gcloud config configurations create {{config_name}}
    
    # Authenticate using the service account key
    gcloud auth activate-service-account --key-file={{key_file}}
    
    # Set the project, account, and zone from the service account key and parameters
    project=$(jq -r '.project_id' {{key_file}})
    account=$(jq -r '.client_email' {{key_file}})
    gcloud config set account $account
    gcloud config set project $project
    gcloud config set compute/zone {{zone}}
    
    echo "Authentication complete. Configuration '{{config_name}}' is set to project $project with account $account and zone {{zone}}"
    
    # Display the current configuration
    gcloud config list

# Print the properties of the currently active gcloud configuration
[group('authentication-and-configuration')]
gcloud-active-config:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Current gcloud configuration:"
    echo "-----------------------------"
    
    # Get the active configuration name
    active_config=$(gcloud config configurations list --filter="IS_ACTIVE=true" --format="value(name)")
    echo "Active Configuration: $active_config"
    echo ""
    
    # Print active service account (if any)
    active_sa=$(gcloud auth list --filter="status:ACTIVE AND account~@.*\.iam\.gserviceaccount\.com" --format="value(account)")
    if [ -n "$active_sa" ]; then
        echo "Active Service Account: $active_sa"
    else
        echo "No active service account"
    fi

