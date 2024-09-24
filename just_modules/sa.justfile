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

    # Check if the authenticated account is an owner
    echo "Checking if the authenticated account is an owner..."
    if ! gcloud projects get-iam-policy {{project_id}} \
        --format="value(bindings.members)" \
        --filter="bindings.role:roles/owner" | \
        grep -q "$(gcloud config get-value account)"; then
        echo "Error: The authenticated account is not an owner of the project."
        echo "Please authenticate with an owner account and try again."
        exit 1
    fi

    echo "Authenticated account is an owner. Proceeding with service account creation..."

    echo "Creating service account: {{sa_name}}"
    gcloud iam service-accounts create {{sa_name}} \
        --project={{project_id}} \
        --display-name="{{sa_display_name}}"

    echo "Assigning roles to {{sa_name}}"
    for role in roles/iam.serviceAccountAdmin \
                roles/iam.serviceAccountCreator \
                roles/iam.roleAdmin \
                roles/iam.serviceAccountKeyAdmin \
                roles/resourcemanager.projectIamAdmin \
                roles/storage.hmacKeyAdmin \
                roles/storage.admin \
                roles/compute.instanceAdmin.v1 \
                roles/cloudkms.cryptoOperator \
                roles/cloudkms.admin \
                roles/serviceusage.serviceUsageAdmin
    do
        gcloud projects add-iam-policy-binding {{project_id}} \
            --member="serviceAccount:{{sa_name}}@{{project_id}}.iam.gserviceaccount.com" \
            --role="$role"
    done

    echo "Service account creation and role assignment completed."

# Create a JSON-formatted key for a service account
[group('service-account-management')]
[no-cd]
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
[no-cd]
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

# Create an HMAC key for a service account
[group('service-account-management')]
[no-cd]
create-hmac-key project sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    
    sa_email="{{sa_name}}@{{project}}.iam.gserviceaccount.com"
    echo "Creating HMAC key for service account: $sa_email in project: {{project}}"
    
    # Create HMAC key and save full response to a temporary file
    temp_file=$(mktemp)
    gcloud storage hmac create --project={{project}} --service-account="$sa_email" --format=json > $temp_file
    
    # Extract accessId and secret, and create JSON output
    jq '{accessId: .metadata.accessId, secret: .secret}' "$temp_file" > "{{output_file}}"
    
    # Remove temporary file
    rm $temp_file
    
    echo "HMAC key created. Access ID and secret saved to {{output_file}}"
    
    # Display the access ID (but not the secret)
    echo "Access ID: $(jq -r .accessId {{output_file}})"
    echo "Secret: [HIDDEN]"
