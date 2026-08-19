# Create a new service account.
# Usage: just gcp-sa create-sa [--project <project-id>] --sa-name <name> --roles-file <path> [--output-file <path>]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("roles_file", long="roles-file", short="r", help="File listing roles for the SA")]
[arg("output_file", long="output-file", short="o", help="Optional output file")]
[group('service-account-management')]
[no-cd]
create-sa project="" sa_name roles_file output_file="":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    # Build and run cluster-ops
    go build -o ./bin/cluster-ops ./cmd/cluster-ops

    ./bin/cluster-ops sa create \
      --name={{ sa_name }} \
      --project="${project_id}" \
      --description={{ sa_name }} \
      --display-name={{ sa_name }} \
      --roles-file={{ roles_file }} \
      --output-file={{ output_file }} \

# Create a JSON-formatted key for a service account.
# Usage: just gcp-sa create-sa-key [--project <project-id>] --sa-name <name> --key-file <path>
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("key_file", long="key-file", short="k", help="Output path for the JSON key")]
[group('service-account-management')]
[no-cd]
create-sa-key project="" sa_name key_file:
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    # Build and run cluster-ops
    go build -o ./bin/cluster-ops ./cmd/cluster-ops

    ./bin/cluster-ops sa create-key \
      --name={{ sa_name }} \
      --project="${project_id}" \
      --output-file={{ key_file }} \

    echo "Service account key created and saved to {{ key_file }}"

# Output service account details in JSON format.
# Usage: just gcp-sa sa-details [--project-id <project-id>] --sa-name <name> --output-file <path>
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("project_id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("output_file", long="output-file", short="o", help="Output file for SA details")]
[group('service-account-management')]
[no-cd]
sa-details project_id="" sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    project_id="${PROJECT_ID:-{{ project_id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"
    echo "Fetching details for service account: $sa_email in project: ${project_id}"
    echo "Output will be saved to: {{ output_file }}"

    mkdir -p "$(dirname "{{ output_file }}")"

    gcloud iam service-accounts describe "$sa_email" \
    --project="${project_id}" \
    --format=json > "{{ output_file }}"

    echo "Service account details have been saved to {{ output_file }}"

# Create an HMAC key for a service account.
# Usage: just gcp-sa create-hmac-key [--project <project-id>] --sa-name <name> --output-file <path>
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("output_file", long="output-file", short="o", help="Output file for the HMAC key")]
[group('service-account-management')]
[no-cd]
create-hmac-key project="" sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"
    echo "Creating HMAC key for service account: $sa_email in project: ${project_id}"

    temp_file=$(mktemp)
    gcloud storage hmac create --project=${project_id} "$sa_email" --format=json > $temp_file

    jq '{accessId: .metadata.accessId, secret: .secret}' "$temp_file" > "{{ output_file }}"

    rm $temp_file

    echo "HMAC key created. Access ID and secret saved to {{ output_file }}"

    echo "Access ID: $(jq -r .accessId {{ output_file }})"
    echo "Secret: [HIDDEN]"

# Create the sa-manager service account, assign all 13 manager roles, and generate a key.
# Uses the active gcloud identity — works with a personal login that has master access
# or with an already-activated service account that has sufficient IAM privileges.
#
# Key path: SA_MANAGER_KEY env var if set (takes precedence), else --key-file,
#           else credentials/<project-id>/sa-manager.json.
# Options: -p/--project-id <project-id>, -k/--key-file <path>.
# Usage: just gcp-sa setup-sa-manager [--project-id <project-id>] [--key-file <path>]
[arg("key_file", long="key-file", short="k", help="Output key path; defaults to SA_MANAGER_KEY, else credentials/<project-id>/sa-manager.json")]
[arg("project_id", long="project-id", short="p", help="GCP project id (required)")]
[group('service-account-management')]
[no-cd]
setup-sa-manager project_id="" key_file="":
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    root="{{ invocation_directory() }}"

    if [ -z "{{ project_id }}" ]; then
        echo "ERROR: --project-id is required."
        exit 1
    fi

    if [ -n "${SA_MANAGER_KEY:-}" ]; then
        key_file="${SA_MANAGER_KEY}"
    elif [ -n "{{ key_file }}" ]; then
        key_file="{{ key_file }}"
    else
        key_file="${root}/credentials/{{ project_id }}/sa-manager.json"
    fi

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
    just gcp-role assign-roles-to-sa --project {{ project_id }} --sa-name ${sa_name} --roles-file {{ invocation_directory() }}/${roles_file}

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
