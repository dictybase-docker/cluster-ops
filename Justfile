# Target to extract roles with custom project ID, service account name, and output file
extract-roles-custom project_id sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    sa_email="{{sa_name}}@{{project_id}}.iam.gserviceaccount.com"
    echo "Extracting roles for service account: $sa_email in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p $(dirname {{output_file}})
    
    # Extract predefined roles and save to output file
    gcloud projects get-iam-policy {{project_id}} --format=json | \
    jq -r '.bindings[] | 
    select(.members[] | contains("serviceAccount:'"$sa_email"'")) | 
    select(.role | startswith("roles/")) | 
    .role' > {{output_file}}

    
    echo "Roles have been extracted and saved to {{output_file}}"

# Target to output service account details in JSON format
sa-details project_id sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    sa_email="{{sa_name}}@{{project_id}}.iam.gserviceaccount.com"
    echo "Fetching details for service account: $sa_email in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p "$(dirname "{{output_file}}")"
    
    # Fetch service account details and save to output file
    gcloud iam service-accounts describe "$sa_email" \
    --project="{{project_id}}" \
    --format=json > "{{output_file}}"
    
    echo "Service account details have been saved to {{output_file}}"

# Target to build and run the analyze-roles subcommand
analyze-roles project_id sa_name credentials output_file="role_analysis_output.txt":
    #!/usr/bin/env bash
    set -euo pipefail
    sa_email="{{sa_name}}@{{project_id}}.iam.gserviceaccount.com"
    echo "Building and running analyze-roles for service account: $sa_email in project: {{project_id}}"
    
    # Build the binary
    go build -o ./bin/gcp-tools ./cmd/gcp
    
    # Run the analyze-roles subcommand
    ./bin/gcp-tools analyze-roles \
        --project-id={{project_id}} \
        --service-account="$sa_email" \
        --credentials={{credentials}} \
        --output={{output_file}}
    
    echo "Analysis complete. Results saved to {{output_file}}"

# Target to create service account and assign roles
create-sa-manager project_id sa_name sa_display_name:
    #!/usr/bin/env bash
    set -euo pipefail

    echo "Creating service account: {{sa_name}}"
    gcloud iam service-accounts create {{sa_name}} \
        --project={{project_id}} \
        --display-name="{{sa_display_name}}"

    echo "Assigning roles to {{sa_name}}"
    for role in roles/iam.serviceAccountAdmin \
                roles/iam.serviceAccountCreator \
                roles/iam.roleAdmin \
                roles/resourcemanager.projectIamAdmin \
                roles/serviceusage.serviceUsageAdmin
    do
        gcloud projects add-iam-policy-binding {{project_id}} \
            --member="serviceAccount:{{sa_name}}@{{project_id}}.iam.gserviceaccount.com" \
            --role="$role"
    done

    echo "Service account creation and role assignment completed."

# project: The Google Cloud project ID
# sa_name: The name of the service account (without @project.iam.gserviceaccount.com)
# key_file: The filename to save the JSON key to
# Target to create a JSON-formatted key for a service account
create-sa-key project sa_name key_file:
    #!/usr/bin/env bash
    set -euo pipefail
    sa_email="{{sa_name}}@{{project}}.iam.gserviceaccount.com"
    echo "Creating service account key for $sa_email in project {{project}}"
    gcloud iam service-accounts keys create {{key_file}} \
        --iam-account="$sa_email" \
        --project={{project}} \
        --key-file-type=json
    echo "Service account key created and saved to {{key_file}}"

# key_file: Path to the service account JSON key file
# config_name: Name for the gcloud configuration to create or update
# Target to authenticate with gcloud using a service account JSON key file and set up a named configuration
gcloud-auth-sa key_file config_name:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Authenticating with gcloud using service account key from {{key_file}}"
    
    # Create a new configuration or activate an existing one
    gcloud config configurations create {{config_name}} --no-activate || gcloud config configurations activate {{config_name}}
    
    # Authenticate using the service account key
    gcloud auth activate-service-account --key-file={{key_file}}
    
    # Set the project from the service account key
    project=$(jq -r '.project_id' {{key_file}})
    gcloud config set project $project
    
    echo "Authentication complete. Configuration '{{config_name}}' is now active and set to project $project"
    
    # Display the current configuration
    gcloud config list

# project: The Google Cloud project ID
# sa_name: The service account name (without @project.iam.gserviceaccount.com)
# role: The role to add (e.g., roles/serviceusage.serviceUsageAdmin)
# Add a role to a service account
add-role-to-sa project sa_name role:
    #!/usr/bin/env bash
    set -euo pipefail
    
    # Construct the full service account email
    sa_email="{{sa_name}}@{{project}}.iam.gserviceaccount.com"
    
    echo "Adding role {{role}} to service account $sa_email in project {{project}}"
    gcloud projects add-iam-policy-binding {{project}} \
        --member="serviceAccount:$sa_email" \
        --role="{{role}}"
    echo "Role {{role}} successfully added to $sa_email"

    # Verify the role was added
    echo "Verifying role assignment..."
    gcloud projects get-iam-policy {{project}} \
        --flatten="bindings[].members" \
        --format='table(bindings.role,bindings.members)' \
        --filter="bindings.members:$sa_email AND bindings.role:{{role}}"

# project: The Google Cloud project ID
# api_file: Path to the file containing API names, one per line
# Target to enable multiple Google Cloud API services from a file
enable-apis project api_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Enabling APIs for project {{project}} from file {{api_file}}"
    
    # Check if the file exists
    if [ ! -f "{{api_file}}" ]; then
        echo "Error: File {{api_file}} not found"
        exit 1
    fi
    
    # Read the file and enable each API
    while IFS= read -r api || [ -n "$api" ]; do
        # Trim whitespace
        api=$(echo "$api" | xargs)
        
        # Skip empty lines
        [ -z "$api" ] && continue
        
        echo "Enabling API: $api"
        gcloud services enable "$api" --project="{{project}}" --async
    done < "{{api_file}}"
    
    echo "Finished enabling APIs"
    
    # List enabled APIs
    echo "Ran commands enabled APIs in project {{project}}:"

# project: The Google Cloud project ID
# output_file: Path to the file where API names will be written
# Target to list enabled APIs in a Google Cloud project and write their names to a file
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

# project: The Google Cloud project ID
# api_file: Path to the file containing API names to disable, one per line
# Target to disable multiple Google Cloud API services from a file
disable-apis project api_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Disabling APIs for project {{project}} from file {{api_file}}"
    
    # Check if the file exists
    if [ ! -f "{{api_file}}" ]; then
        echo "Error: File {{api_file}} not found"
        exit 1
    fi
    
    # Read the file and disable each API
    while IFS= read -r api || [ -n "$api" ]; do
        # Trim whitespace
        api=$(echo "$api" | xargs)
        
        # Skip empty lines
        [ -z "$api" ] && continue
        
        echo "Disabling API: $api"
        if gcloud services disable "$api" --project="{{project}}" --force; then
            echo "Successfully disabled $api"
        else
            echo "Failed to disable $api"
        fi
    done < "{{api_file}}"
    
    echo "Finished disabling APIs"
    
    # List remaining enabled APIs
    echo "Currently enabled APIs in project {{project}}:"
    gcloud services list --project="{{project}}" --enabled --format="table(config.name,config.title)"
# Target to print the properties of the currently active gcloud configuration
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

