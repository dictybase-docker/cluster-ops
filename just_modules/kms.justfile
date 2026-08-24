# Create a KMS keyring and key with a specified location and credentials file.
# Usage: just gcp-kms create-keyring-and-key [--project-id <id>] --keyring-name <name> --key-name <name> --credentials-file <path> [--location <zone>]
[arg("key-name", long="key-name", short="k", help="KMS key name")]
[arg("location", long="location", short="l", help="GCP location/region")]
[arg("project-id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("keyring-name", long="keyring-name", short="r", help="KMS keyring name")]
[arg("credentials-file", long="credentials-file", short="c", help="Service account credentials file")]
[no-cd]
create-keyring-and-key project-id="" keyring-name key-name credentials-file location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project-id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    go build -o bin/gcp-tools cmd/gcp/main.go

    ./bin/gcp-tools create-keyring-and-key \
        --project-id "${project_id}" \
        --keyring-name {{ keyring-name }} \
        --key-name {{ key-name }} \
        --location {{ location }} \
        --credentials {{ credentials-file }}

    echo "Keyring and key creation complete."
