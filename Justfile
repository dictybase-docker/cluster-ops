# Target to extract roles with custom project ID, service account email, and output file
extract-roles-custom project_id sa_email output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Extracting roles for service account: {{sa_email}} in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p $(dirname {{output_file}})
    
    # Extract predefined roles and save to output file
    gcloud projects get-iam-policy {{project_id}} --format=json | \
    jq -r '.bindings[] | 
    select(.members[] | contains("serviceAccount:{{sa_email}}")) | 
    select(.role | startswith("roles/")) | 
    .role' > {{output_file}}

    
    echo "Roles have been extracted and saved to {{output_file}}"

# Target to output service account details in JSON format
sa-details project_id sa_email output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Fetching details for service account: {{sa_email}} in project: {{project_id}}"
    echo "Output will be saved to: {{output_file}}"
    
    # Create directory for output file if it doesn't exist
    mkdir -p "$(dirname "{{output_file}}")"
    
    # Fetch service account details and save to output file
    gcloud iam service-accounts describe "{{sa_email}}" \
    --project="{{project_id}}" \
    --format=json > "{{output_file}}"
    
    echo "Service account details have been saved to {{output_file}}"

# Target to build and run the analyze-roles subcommand
analyze-roles project_id sa_email credentials output_file="role_analysis_output.txt":
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Building and running analyze-roles for service account: {{sa_email}} in project: {{project_id}}"
    
    # Build the binary
    go build -o ./bin/gcp-tools ./cmd/gcp
    
    # Run the analyze-roles subcommand
    ./bin/gcp-tools analyze-roles \
        --project-id={{project_id}} \
        --service-account={{sa_email}} \
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
                roles/resourcemanager.projectIamAdmin
    do
        gcloud projects add-iam-policy-binding {{project_id}} \
            --member="serviceAccount:{{sa_name}}@{{project_id}}.iam.gserviceaccount.com" \
            --role="$role"
    done

    echo "Service account creation and role assignment completed."

# project: The Google Cloud project ID
# service_account: The name of the service account (without @project.iam.gserviceaccount.com)
# key_file: The filename to save the JSON key to
# Target to create a JSON-formatted key for a service account
create-sa-key project service_account key_file:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Creating service account key for {{service_account}} in project {{project}}"
    gcloud iam service-accounts keys create {{key_file}} \
        --iam-account={{service_account}}@{{project}}.iam.gserviceaccount.com \
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
# service_account: The service account name (without @project.iam.gserviceaccount.com)
# role: The role to add (e.g., roles/serviceusage.serviceUsageAdmin)
# Add a role to a service account
add-role-to-sa project service_account role:
    #!/usr/bin/env bash
    set -euo pipefail
    
    # Construct the full service account email
    sa_email="{{service_account}}@{{project}}.iam.gserviceaccount.com"
    
    echo "Adding role {{role}} to service account {{service_account}} in project {{project}}"
    gcloud projects add-iam-policy-binding {{project}} \
        --member="serviceAccount:$sa_email" \
        --role="{{role}}"
    echo "Role {{role}} successfully added to {{service_account}}"

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

# Target to list enabled apis for a google cloud project
available-apis project:
    gcloud services list --project="{{project}}" --enabled --format="table(config.name,config.title)"
