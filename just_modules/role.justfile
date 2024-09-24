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
    # disable prompt
    gcloud config set disable_prompts true
    
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
[no-cd]
assign-roles-to-sa project sa_name roles_file:
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true
    
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

