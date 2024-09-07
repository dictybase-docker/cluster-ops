# Create a new service account
[group('service-account-management')]
create-sa project sa_name sa_description:
    #!/usr/bin/env bash
    set -euo pipefail
    
    echo "Creating service account '{{sa_name}}' in project {{project}}"
    gcloud iam service-accounts create {{sa_name}} \
        --project={{project}} \
        --display-name="{{sa_name}}" \
        --description="{{sa_description}}"
    
    # Verify the service account was created
    echo "Verifying service account creation..."
    gcloud iam service-accounts describe {{sa_name}}@{{project}}.iam.gserviceaccount.com \
        --project={{project}} \
        --format="table(displayName,email,description)"

# Create service account manager and assign predefined roles
[group('service-account-management')]
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

# Create a JSON-formatted key for a service account
[group('service-account-management')]
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

# Output service account details in JSON format
[group('service-account-management')]
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


# Extract roles with custom project ID, service account name, and output file
[group('role-management')]
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

# Add a role to a service account
[group('role-management')]
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

# Assign multiple roles to an existing service account from a file
[group('role-management')]
assign-roles-to-sa project sa_name roles_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    # Construct the full service account email
    sa_email="{{sa_name}}@{{project}}.iam.gserviceaccount.com"
    
    echo "Assigning roles to service account $sa_email in project {{project}} from file {{roles_file}}"
    
    # Check if the file exists
    if [ ! -f "{{roles_file}}" ]; then
        echo "Error: File {{roles_file}} not found"
        exit 1
    fi
    
    # Read the file and assign each role
    while IFS= read -r role || [ -n "$role" ]; do
        # Trim whitespace
        role=$(echo "$role" | xargs)
        
        # Skip empty lines
        [ -z "$role" ] && continue
        
        echo "Assigning role: $role"
        gcloud projects add-iam-policy-binding {{project}} \
            --member="serviceAccount:$sa_email" \
            --role="$role"
    done < "{{roles_file}}"
    
    echo "Finished assigning roles to $sa_email"
    
    # List assigned roles
    echo "Roles assigned to $sa_email in project {{project}}:"
    gcloud projects get-iam-policy {{project}} \
        --flatten="bindings[].members" \
        --format='table(bindings.role)' \
        --filter="bindings.members:$sa_email"

# Enable multiple Google Cloud API services from a file
[group('api-management')]
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

# List enabled APIs in a Google Cloud project and write their names to a file
[group('api-management')]
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


# Authenticate with gcloud using a service account JSON key file and set up a named configuration
[group('authentication-and-configuration')]
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


# Build and run the analyze-roles subcommand
[group('analysis')]
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
