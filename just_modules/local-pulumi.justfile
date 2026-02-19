# Set up Pulumi with local filesystem backend
# Parameters:
#   path: The custom folder path to store Pulumi state (default: pulumi-files/local-state)
[group('pulumi-local')]
pulumi-local-setup path="pulumi-files/local-state":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{ path }}"
    pulumi login "file://$(realpath "{{ path }}")"
    echo "Pulumi has been set up to use local directory {{ path }} as the backend."

# Create a local stack with copied config
# Parameters:
#   folder: The folder containing the Pulumi project
#   stack: The name of the new stack (default: local)
#   from-stack: The stack to copy config from (default: experiments)
#   pass_entry: The pass entry for the stack passphrase (default: pulumi/local-passphrase)
[group('pulumi-local')]
[no-cd]
setup-local-stack folder stack="local" from-stack="experiments" pass_entry="pulumi/local-passphrase":
    #!/usr/bin/env bash
    set -euo pipefail

    # Fetch passphrase from pass
    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"
    cd "{{ folder }}"

    if pulumi stack ls --json | jq -e --arg s "{{ stack }}" 'any(.[]; .name == $s)' > /dev/null 2>&1; then
        echo "Stack '{{ stack }}' already exists."
    else
        echo "Creating stack '{{ stack }}' from '{{ from-stack }}'..."
        pulumi stack init "{{ stack }}" \
            --copy-config-from "{{ from-stack }}" \
            --secrets-provider passphrase
    fi
