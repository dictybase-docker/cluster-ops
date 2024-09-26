# Create a keyring and key with a specified location (default: us-central1) and credentials file
[no-cd]
create-keyring-and-key project-id keyring-name key-name credentials-file location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    # Build the Go binary
    go build -o bin/gcp-tools cmd/gcp/main.go

    # Run the Go command to create keyring and key
    ./bin/gcp-tools create-keyring-and-key \
        --project-id {{project-id}} \
        --keyring-name {{keyring-name}} \
        --key-name {{key-name}} \
        --location {{location}} \
        --credentials {{credentials-file}}

    echo "Keyring and key creation complete."
