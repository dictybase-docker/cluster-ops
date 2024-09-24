# Create a keyring and key with a specified location (default: us-central1)
create-keyring-and-key PROJECT_ID KEYRING_NAME KEY_NAME LOCATION="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail
    # disable prompt
    gcloud config set disable_prompts true
    
    # Check if the keyring already exists
    if ! gcloud kms keyrings describe {{KEYRING_NAME}} --location={{LOCATION}} --project={{PROJECT_ID}} &>/dev/null; then
        echo "Creating keyring {{KEYRING_NAME}}..."
        gcloud kms keyrings create {{KEYRING_NAME}} --location={{LOCATION}} --project={{PROJECT_ID}}
    else
        echo "Keyring {{KEYRING_NAME}} already exists."
    fi
    
    # Create the key
    echo "Creating key {{KEY_NAME}}..."
    gcloud kms keys create {{KEY_NAME}} --location={{LOCATION}} --keyring={{KEYRING_NAME}} --purpose=encryption --project={{PROJECT_ID}}
