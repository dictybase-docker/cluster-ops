# Extract custom roles for a service account to an output file.
# Usage: just gcp-role extract-roles-custom [--project-id <project-id>] --sa-name <name> --output-file <path>
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("project_id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("output_file", long="output-file", short="o", help="Output file for extracted roles")]
[group('role-management')]
extract-roles-custom project_id="" sa_name output_file:
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project_id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"
    echo "Extracting roles for service account: $sa_email in project: ${project_id}"
    echo "Output will be saved to: {{ output_file }}"

    mkdir -p $(dirname {{ output_file }})

    gcloud projects get-iam-policy ${project_id} --format=json | \
    jq -r '.bindings[] |
    select(.members[] | contains("serviceAccount:'"$sa_email"'")) |
    select(.role | startswith("roles/")) |
    .role' > {{ output_file }}

    echo "Roles have been extracted and saved to {{ output_file }}"

# Add a single role to a service account.
# Usage: just gcp-role add-role-to-sa [--project <project-id>] --sa-name <name> --role <role>
[arg("role", long="role", short="r", help="IAM role to add (e.g. roles/storage.admin)")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[group('role-management')]
add-role-to-sa project="" sa_name role:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"

    echo "Adding role {{ role }} to service account $sa_email in project ${project_id}"
    gcloud projects add-iam-policy-binding ${project_id} \
        --member="serviceAccount:$sa_email" \
        --role="{{ role }}"
    echo "Role {{ role }} successfully added to $sa_email"

    echo "Verifying role assignment..."
    gcloud projects get-iam-policy ${project_id} \
        --flatten="bindings[].members" \
        --format='table(bindings.role,bindings.members)' \
        --filter="bindings.members:$sa_email AND bindings.role:{{ role }}"

# Assign multiple roles to an existing service account from a file.
# Usage: just gcp-role assign-roles-to-sa [--project <project-id>] --sa-name <name> --roles-file <path>
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("sa_name", long="sa-name", short="s", help="Service account name")]
[arg("roles_file", long="roles-file", short="r", help="File listing roles (one per line)")]
[group('role-management')]
[no-cd]
assign-roles-to-sa project="" sa_name roles_file:
    #!/usr/bin/env bash
    set -euo pipefail
    gcloud config set disable_prompts true

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project."
        exit 1
    fi

    sa_email="{{ sa_name }}@${project_id}.iam.gserviceaccount.com"

    echo "Assigning roles to service account $sa_email in project ${project_id} from file {{ roles_file }}"

    if [ ! -f "{{ roles_file }}" ]; then
        echo "Error: File {{ roles_file }} not found"
        exit 1
    fi

    while IFS= read -r role || [ -n "$role" ]; do
        role=$(echo "$role" | xargs)
        [ -z "$role" ] && continue

        echo "Assigning role: $role"
        gcloud projects add-iam-policy-binding ${project_id} \
            --member="serviceAccount:$sa_email" \
            --role="$role"
    done < "{{ roles_file }}"

    echo "Finished assigning roles to $sa_email"

    echo "Roles assigned to $sa_email in project ${project_id}:"
    gcloud projects get-iam-policy ${project_id} \
        --flatten="bindings[].members" \
        --format='table(bindings.role)' \
        --filter="bindings.members:$sa_email"
