# Set up Pulumi with a local filesystem backend.
# Parameters:
# path: The custom folder path to store Pulumi state (default: pulumi-files/local-state)
[group('pulumi-local')]
[no-cd]
pulumi-local-setup path="pulumi-files/local-state":
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p "{{ path }}"
    pulumi login "file://$(realpath "{{ path }}")"
    echo "Pulumi has been set up to use local directory {{ path }} as the backend."

# Create a new Pulumi Go project using the local backend.
# Usage: just local-pulumi new-project --folder <dir> [--stack <name>] [--pass-entry <entry>]
[arg("stack", long="stack", short="s", help="Initial stack name")]
[arg("folder", long="folder", short="f", help="Folder to create the project in")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for the stack passphrase")]
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

# Create a new local stack in a given folder.
# Usage: just local-pulumi new-stack --folder <dir> [--stack <name>] [--pass-entry <entry>]
[arg("stack", long="stack", short="s", help="New stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for the stack passphrase")]
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

# Deploy resources for a local stack.
# Usage: just local-pulumi create-resource --folder <dir> [--stack <name>] [--pass-entry <entry>]
[arg("stack", long="stack", short="s", help="Stack to deploy")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for the stack passphrase")]
[group('pulumi-local')]
[no-cd]
create-resource folder stack="local" pass_entry="pulumi/local-passphrase": pulumi-local-setup
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"
    pulumi -C "{{ folder }}" up -s "{{ stack }}" -f -y

# Destroy resources for a local stack.
# Usage: just local-pulumi remove-resource --folder <dir> [--stack <name>] [--pass-entry <entry>]
[arg("stack", long="stack", short="s", help="Stack to destroy")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for the stack passphrase")]
[group('pulumi-local')]
[no-cd]
remove-resource folder stack="local" pass_entry="pulumi/local-passphrase": pulumi-local-setup
    #!/usr/bin/env bash
    set -euo pipefail

    if ! PASSPHRASE=$(pass show "{{ pass_entry }}" | head -n 1); then
        echo "Error: Failed to retrieve passphrase from '{{ pass_entry }}'"
        exit 1
    fi

    export PULUMI_CONFIG_PASSPHRASE="$PASSPHRASE"
    pulumi -C "{{ folder }}" destroy -s "{{ stack }}" -f -y

# Create a new local stack copied from an existing stack's config.
# Usage: just local-pulumi new-stack-from --folder <dir> [--stack <name>] [--from-stack <name>] [--pass-entry <entry>]
[arg("stack", long="stack", short="s", help="New stack name")]
[arg("folder", long="folder", short="f", help="Folder containing the Pulumi project")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[arg("pass_entry", long="pass-entry", short="p", help="pass entry for the stack passphrase")]
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
    cd "{{ folder }}"

    if [[ ! -d "{{ folder }}" ]]; then
        echo "Error: folder '{{ folder }}' does not exist"
        exit 1
    fi

    if pulumi -C "{{ folder }}" stack ls --json | jq -e --arg s "{{ stack }}" 'any(.[]; .name == $s)' > /dev/null 2>&1; then
        echo "Stack '{{ stack }}' already exists."
    else
        echo "Creating stack '{{ stack }}' from '{{ from-stack }}'..."
        pulumi stack init "{{ stack }}" \
            --copy-config-from "{{ from-stack }}" \
            --secrets-provider passphrase
    fi
