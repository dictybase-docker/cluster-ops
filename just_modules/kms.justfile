# Create a KMS keyring and key with a specified location and credentials file.
# Usage: just gcp-kms create-keyring-and-key [--project-id <id>] [--keyring-name <name>] [--key-name <name>] [--credentials-file <path>] [--location <zone>]
[arg("key-name", long="key-name", short="k", help="KMS key name (defaults from PULUMI_SECRET_PROVIDER)")]
[arg("location", long="location", short="l", help="GCP location/region")]
[arg("project-id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("keyring-name", long="keyring-name", short="r", help="KMS keyring name (defaults from PULUMI_SECRET_PROVIDER)")]
[arg("credentials-file", long="credentials-file", short="c", help="Service account credentials file (defaults from PULUMI_GCP_CREDENTIALS)")]
[no-cd]
create-keyring-and-key project-id="" keyring-name="" key-name="" credentials-file="" location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project-id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    keyring_name="{{ keyring-name }}"
    if [ -z "$keyring_name" ] && [ -n "${PULUMI_SECRET_PROVIDER:-}" ]; then
        keyring_name=$(echo "$PULUMI_SECRET_PROVIDER" | sed -n 's/.*\/keyRings\/\([^/]*\).*/\1/p')
    fi

    key_name="{{ key-name }}"
    if [ -z "$key_name" ] && [ -n "${PULUMI_SECRET_PROVIDER:-}" ]; then
        key_name=$(echo "$PULUMI_SECRET_PROVIDER" | sed -n 's/.*\/cryptoKeys\/\([^/]*\).*/\1/p')
    fi

    if [ -z "$keyring_name" ] || [ -z "$key_name" ]; then
        echo "ERROR: --keyring-name and --key-name required (or set PULUMI_SECRET_PROVIDER)."
        exit 1
    fi

    loc="{{ location }}"
    if [ -n "${PULUMI_SECRET_PROVIDER:-}" ]; then
        parsed_loc=$(echo "$PULUMI_SECRET_PROVIDER" | sed -n 's/.*\/locations\/\([^/]*\).*/\1/p')
        if [ -n "$parsed_loc" ]; then
            loc="$parsed_loc"
        fi
    fi

    cred_file="{{ credentials-file }}"
    if [ -z "$cred_file" ]; then
        cred_file="${PULUMI_GCP_CREDENTIALS:-${GOOGLE_APPLICATION_CREDENTIALS:-credentials/${project_id}/pulumi-manager.json}}"
    fi

    if [ ! -f "$cred_file" ]; then
        echo "ERROR: credentials file not found: $cred_file"
        exit 1
    fi

    go build -o ./bin/cluster-ops ./cmd/cluster-ops

    ./bin/cluster-ops kms create \
        --project-id "${project_id}" \
        --keyring-name "${keyring_name}" \
        --key-name "${key_name}" \
        --location "${loc}" \
        --credentials "${cred_file}"

    echo "Keyring and key creation complete."
