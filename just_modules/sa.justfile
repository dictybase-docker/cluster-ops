# Create a new service account
[group('service-account-management')]
[no-cd]
create-sa project sa_name roles_file output_file="":
    #!/usr/bin/env bash
    set -euo pipefail

    # Build and run cluster-ops
    go build -o ./bin/cluster-ops ./cmd/cluster-ops

    ./bin/cluster-ops sa create \
      --name={{ sa_name }} \
      --project={{ project }} \
      --description={{ sa_name }} \
      --display-name={{ sa_name }} \
      --roles-file={{ roles_file }} \
      --output-file={{ output_file }} \

# Create a JSON-formatted key for a service account
[group('service-account-management')]
[no-cd]
create-sa-key project sa_name key_file:
    #!/usr/bin/env bash
    set -euo pipefail

    # Build and run cluster-ops
    go build -o ./bin/cluster-ops ./cmd/cluster-ops
    
    ./bin/cluster-ops sa create-key \
      --name={{ sa_name }} \
      --project={{ project }} \
      --output-file={{ key_file }} \

    echo "Service account key created and saved to {{ key_file }}"

# Output service account details in JSON format
[group('service-account-management')]
[no-cd]
sa-details project_id sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true
    sa_email="{{ sa_name }}@{{ project_id }}.iam.gserviceaccount.com"
    echo "Fetching details for service account: $sa_email in project: {{ project_id }}"
    echo "Output will be saved to: {{ output_file }}"

    # Create directory for output file if it doesn't exist
    mkdir -p "$(dirname "{{ output_file }}")"

    # Fetch service account details and save to output file
    gcloud iam service-accounts describe "$sa_email" \
    --project="{{ project_id }}" \
    --format=json > "{{ output_file }}"

    echo "Service account details have been saved to {{ output_file }}"

# Create an HMAC key for a service account
[group('service-account-management')]
[no-cd]
create-hmac-key project sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true

    sa_email="{{ sa_name }}@{{ project }}.iam.gserviceaccount.com"
    echo "Creating HMAC key for service account: $sa_email in project: {{ project }}"

    # Create HMAC key and save full response to a temporary file
    temp_file=$(mktemp)
    gcloud storage hmac create --project={{ project }} "$sa_email" --format=json > $temp_file

    # Extract accessId and secret, and create JSON output
    jq '{accessId: .metadata.accessId, secret: .secret}' "$temp_file" > "{{ output_file }}"

    # Remove temporary file
    rm $temp_file

    echo "HMAC key created. Access ID and secret saved to {{ output_file }}"

    # Display the access ID (but not the secret)
    echo "Access ID: $(jq -r .accessId {{ output_file }})"
    echo "Secret: [HIDDEN]"

# Create the sa-manager service account, assign all 13 manager roles, and generate a key.
# Uses the active gcloud identity — works with a personal login that has master access
# or with an already-activated service account that has sufficient IAM privileges.
#
# Usage: just gcp-sa setup-sa-manager <project_id> [key_file]
#   key_file defaults to credentials/sa-manager.json
[group('service-account-management')]
setup-sa-manager project_id key_file="credentials/sa-manager.json":
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    sa_name="sa-manager"
    sa_email="${sa_name}@{{ project_id }}.iam.gserviceaccount.com"
    roles_file="gcs-files/roles-permissions/service-account-manager-roles.txt"

    # 1. Create the service account (idempotent — skips if it already exists)
    echo "=== Step 1/3: Creating service account ${sa_email} ==="
    if gcloud iam service-accounts describe "${sa_email}" --project={{ project_id }} > /dev/null 2>&1; then
        echo "Service account already exists, skipping creation."
    else
        gcloud iam service-accounts create "${sa_name}" \
            --project={{ project_id }} \
            --display-name="Service Account Manager" \
            --description="Master SA for bootstrapping cluster infrastructure"
        echo "Service account created."
    fi

    # 2. Assign all 13 manager roles
    echo "=== Step 2/3: Assigning roles from ${roles_file} ==="
    just gcp-role assign-roles-to-sa {{ project_id }} ${sa_name} ${roles_file}

    # 3. Create and download a JSON key (pure gcloud CLI — no ADC needed)
    echo "=== Step 3/3: Generating key file ==="
    mkdir -p "$(dirname "${key_file}")"
    gcloud iam service-accounts keys create "${key_file}" \
        --iam-account="${sa_email}" \
        --project={{ project_id }}
    chmod 600 "${key_file}"

    echo ""
    echo "Done. sa-manager is ready:"
    echo "  SA email : ${sa_email}"
    echo "  Key file : ${key_file}"
    echo ""
    echo "Next: set up your environment — see docs/kops-setup-draft.md Section 2."
