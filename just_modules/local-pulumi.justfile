# Set up Pulumi with local filesystem backend
# Parameters:
#   path: The custom folder path to store Pulumi state (default: pulumi-files/local-state)
[group('pulumi-local')]
[no-cd]
pulumi-local-setup path="pulumi-files/local-state":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{ path }}"
    pulumi login "file://$(realpath "{{ path }}")"
    echo "Pulumi has been set up to use local directory {{ path }} as the backend."

# Create a new Pulumi Go project using the local backend
# Parameters:
#   folder: The folder to create the project in (will be created if it doesn't exist)
#   stack: The initial stack name (default: local)
#   pass_entry: The pass entry for the stack passphrase (default: pulumi/local-passphrase)
[group('pulumi-local')]
[no-cd]
new-project folder stack="local" pass_entry="pulumi/local-passphrase": pulumi-local-setup
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    mkdir -p "{{ folder }}"

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    pulumi new go \
        --name "$(basename "{{ folder }}")" \
        --description "A minimal Go Pulumi program" \
        --stack "{{ stack }}" \
        --secrets-provider passphrase \
        --dir "{{ folder }}" \
        --yes

    echo "Project '$(basename "{{ folder }}")' created in {{ folder }} with stack '{{ stack }}'."

# Create a new local stack in a given folder
# Parameters:
#   folder: The folder containing the Pulumi project
#   stack: The name of the new stack (default: local)
#   pass_entry: The pass entry for the stack passphrase (default: pulumi/local-passphrase)
[group('pulumi-local')]
[no-cd]
new-stack folder stack="local" pass_entry="pulumi/local-passphrase": pulumi-local-setup
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    if [[ ! -d "{{ folder }}" ]]; then
        echo "Error: folder '{{ folder }}' does not exist"
        exit 1
    fi

    if pulumi -C "{{ folder }}" stack ls --json | jq -e --arg s "{{ stack }}" 'any(.[]; .name == $s)' > /dev/null 2>&1; then
        echo "Stack '{{ stack }}' already exists."
    else
        echo "Creating stack '{{ stack }}'..."
        pulumi -C "{{ folder }}" stack init "{{ stack }}" --secrets-provider passphrase
    fi

# Create a new local stack copied from an existing stack's config
# Parameters:
#   folder: The folder containing the Pulumi project
#   stack: The name of the new stack (default: local)
#   from-stack: The stack to copy config from (default: experiments)
#   pass_entry: The pass entry for the stack passphrase (default: pulumi/local-passphrase)
[group('pulumi-local')]
[no-cd]
new-stack-from folder stack="local" from-stack="experiments" pass_entry="pulumi/local-passphrase": pulumi-local-setup
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"

    if [[ ! -d "{{ folder }}" ]]; then
        echo "Error: folder '{{ folder }}' does not exist"
        exit 1
    fi

    if pulumi -C "{{ folder }}" stack ls --json | jq -e --arg s "{{ stack }}" 'any(.[]; .name == $s)' > /dev/null 2>&1; then
        echo "Stack '{{ stack }}' already exists."
    else
        echo "Creating stack '{{ stack }}' from '{{ from-stack }}'..."
        pulumi -C "{{ folder }}" stack init "{{ stack }}" \
            --copy-config-from "{{ from-stack }}" \
            --secrets-provider passphrase
    fi
