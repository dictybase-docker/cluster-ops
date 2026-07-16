# Modules
mod gcp-analysis 'just_modules/analysis.justfile'
mod gcp-sa 'just_modules/sa.justfile'
mod gcp-role 'just_modules/role.justfile'
mod gcp-api 'just_modules/api.justfile'
mod gcp-image 'just_modules/image.justfile'
mod gcp-kms 'just_modules/kms.justfile'
mod gcp-pulumi 'just_modules/pulumi.justfile'
mod local-pulumi 'just_modules/local-pulumi.justfile'
mod gcp-cluster 'just_modules/cluster.justfile'
mod k3d 'just_modules/k3d.justfile'
mod arangodb 'just_modules/arangodb.justfile'
mod docker 'just_modules/docker.justfile'

# Variables

dagger_version := "v0.11.9"
container_module := "github.com/dictybase-docker/dagger-of-dcr/container-image@main"
bin_path := `mktemp -d`
action_bin := bin_path + "/actions"
dagger_bin := bin_path + "/dagger"
base_gha_download_url := "https://github.com/dictybase-docker/github-actions/releases/download/v2.10.0/action_2.10.0_"
gha_download_url := if os() == "macos" { base_gha_download_url + "darwin_arm64" } else { base_gha_download_url + "linux_amd64" }
file_suffix := ".tar.gz"
dagger_file := if os() == "macos" { "darwin_arm64" + file_suffix } else { "linux_amd64" + file_suffix }

# Optional legacy fallback superseded by `just cluster-env <env> <cluster>`.
# Kept for backward compatibility with recipes that don't have an active cluster-env sub-shell.
set dotenv-filename := x"${CLUSTER_ENV_FILE:-.env}"

# Main setup recipe
setup: install-gha-binary install-dagger-binary

# --- Setup Tools ---

[group('setup-tools')]
install-gha-binary:
    @curl -L -o {{ action_bin }} {{ gha_download_url }}
    @chmod +x {{ action_bin }}

[group('setup-tools')]
install-dagger-binary:
    {{ action_bin }} sd --dagger-version {{ dagger_version }} --dagger-bin-dir {{ bin_path }} --dagger-file {{ dagger_file }}

[group('setup-tools')]
install-asdf-plugins:
    asdf plugin add kubectl || true
    asdf plugin add kops || true
    asdf plugin add pulumi || true
    asdf plugin add velero || true
    asdf plugin add mc || true
    asdf plugin add gcloud || true
    asdf install kubectl 1.28.8
    asdf set kubectl 1.28.8
    asdf install kops v1.29.2
    asdf set kops v1.29.2
    asdf install pulumi 3.108.0
    asdf set pulumi 3.108.0
    asdf install velero 1.14.0
    asdf set velero 1.14.0
    asdf install mc 2024-02-09T22-18-24Z
    asdf set mc 2024-02-09T22-18-24Z
    asdf install gcloud 537.0.0
    asdf set gcloud 537.0.0

# Install or upgrade a tool version

# Usage: just install-tool <name> <version>
[group('setup-tools')]
install-tool name version:
    #!/usr/bin/env bash
    set -euo pipefail

    # Check if the tool is in .tool-versions
    if ! grep -q "^{{ name }} " .tool-versions; then
        echo "Error: Tool '{{ name }}' is not defined in .tool-versions file."
        exit 1
    fi

    echo "Installing {{ name }} version {{ version }}..."
    asdf install {{ name }} {{ version }}
    asdf set {{ name }} {{ version }}
    echo "Successfully installed and set {{ name }} {{ version }}"

# --- Development & Testing ---

# Run Golang tests using Dagger
[group('dev-tools')]
test:
    dagger -m github.com/dictybase-docker/dagger-of-dcr/golang@main call with-golang-version with-gotest-sum-formatter test --src "."

# Run aider AI coding assistant with specific configuration
[group('dev-tools')]
aider:
    #!/usr/bin/env bash
    set -euxo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="{{ invocation_directory() }}/credentials/dcr-experiments-cloud-manager.json"
    aider --architect --model 'vertex_ai/claude-3-5-sonnet-v2@20241022' \
                   --no-auto-commits \
                   --no-auto-lint \
                   --vim --cache-prompts \
                   --cache-keepalive-pings 3 \
                   --watch-files

[group('dev-tools')]
set-env-var name value:
    #!/usr/bin/env bash
    set -euo pipefail

    # Build the binary
    go build -o ./bin/util ./cmd/util

    ./bin/util set-env-var \
      --name={{ name }} \
      --value={{ value }} \

    direnv allow
    # Verify the service account was created
    echo "Environmental variable {{ name }} has been set to {{ value }}"

# --- Build & Publish ---
# ref: Git reference (branch, tag, or commit hash) to use for the build
# user: Docker Hub username
# pass: Docker Hub password

# Build and publish the backup image to Docker Hub
[group('build')]
build-publish-backup-image ref user pass: setup
    #!/usr/bin/env bash
    set -euxo pipefail

    {{ dagger_bin }} call -m {{ container_module }} \
    with-ref --ref={{ ref }} \
    with-repository --repository dictybase-docker/cluster-ops \
    with-dockerfile --docker-file build/package/Dockerfile \
    with-image --image database-backup \
    with-namespace publish-from-repo \
    --user={{ user }} --password={{ pass }}

# --- Cluster Operations ---

# Activate a per-cluster env file by sourcing it into a sub-shell.
# This is the recommended way to switch between clusters (dev/staging/prod).
# The sub-shell inherits all cluster vars; 'exit' or Ctrl-D returns to the parent shell.
#
# Usage: just cluster-env <env> <cluster>
# Example: just cluster-env dev my-cluster
[group('cluster-ops')]
cluster-env env cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    source ".env.{{ env }}.{{ cluster }}"
    echo "Cluster: ${KOPS_CLUSTER_NAME}"
    echo "State  : ${KOPS_STATE_STORE}"
    echo "Type 'exit' or Ctrl-D to leave this environment."
    exec "$SHELL"

# Update the GOOGLE_APPLICATION_CREDENTIALS line in a per-cluster env file.
# Inserts it if absent, replaces it if already present.
#
# Usage: just cluster-cred <env> <cluster> <key-path>
# Example: just cluster-cred dev my-cluster credentials/kops-cluster-creator.json
[group('cluster-ops')]
cluster-cred env cluster key:
    #!/usr/bin/env bash
    set -euo pipefail
    env_file=".env.{{ env }}.{{ cluster }}"
    abs_key="${PWD}/{{ key }}"

    if grep -q '^export GOOGLE_APPLICATION_CREDENTIALS=' "${env_file}" 2>/dev/null; then
        sed -i '' "s|^export GOOGLE_APPLICATION_CREDENTIALS=.*|export GOOGLE_APPLICATION_CREDENTIALS=${abs_key}|" "${env_file}"
    else
        echo "export GOOGLE_APPLICATION_CREDENTIALS=${abs_key}" >> "${env_file}"
    fi
    echo "Updated ${env_file}: GOOGLE_APPLICATION_CREDENTIALS → ${abs_key}"

# --- Pulumi Operations ---
# Setup Pulumi deployment environment
# Parameters:
#   project_id: GCP project ID
#   keyring_name: Name of the KMS keyring
#   key_name: Name of the KMS key
#   bucket_name: Name of the GCS bucket for Pulumi state

# location: Google Cloud region (optional, defaults to us-central1)
[group('pulumi-ops')]
initialize-pulumi project_id keyring_name key_name bucket_name location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    echo "Step 1: Creating Pulumi Manager Service Account Key"
    just gcp-sa create-sa {{ project_id }} pulumi-manager gcs-files/roles-permissions/pulumi-manager-roles.txt credentials/pulumi-manager.json

    echo "Step 2: Setting PULUMI_GCP_CREDENTIALS environment variable"
    export PULUMI_GCP_CREDENTIALS="${PWD}/credentials/pulumi-manager.json"

    echo "Step 3: Creating Key Ring and Key for Pulumi secrets encryption"
    just gcp-kms create-keyring-and-key {{ project_id }} {{ keyring_name }} {{ key_name }} credentials/pulumi-manager.json {{ location }}

    echo "Step 4: Initializing Pulumi State Store"
    just gcp-pulumi pulumi-gcs-setup credentials/pulumi-manager.json {{ bucket_name }} "" {{ location }}
    echo "Pulumi deployment environment setup completed successfully!"

# Setup Pulumi deployment environment
# Parameters:
#   stack: Name of desired stack for initial pulumi projects
#   from-stack: Name of stack whose config to copy in the new stack
#   project_id: GCP project ID
#   keyring_name: Name of the KMS keyring
#   key_name: Name of the KMS key
#   bucket_name: Name of the GCS bucket for Pulumi state

# location: Google Cloud region (optional, defaults to us-central1)
[group('pulumi-ops')]
pulumi-init-and-deploy stack from-stack project_id keyring_name key_name bucket_name location="us-central1":
    #!/usr/bin/env bash
    just initialize-pulumi {{ project_id }} {{ keyring_name }} {{ key_name }} {{ bucket_name }} {{ location }}
    export PULUMI_SECRET_PROVIDER="gcpkms://projects/{{ project_id }}/locations/{{ location }}/keyRings/{{ keyring_name }}/cryptoKeys/{{ key_name }}"

    echo "Creating Initial Resources"
    just gcp-pulumi create-multiple-resources {{ stack }} {{ from-stack }} "./pulumi-files/initial-resources.txt"
    just gcp-pulumi create-multiple-resources {{ stack }} {{ from-stack }} "./pulumi-files/database-and-storage-resources.txt"
