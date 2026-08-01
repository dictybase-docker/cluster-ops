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

# Install a tool version in the active (or default) tool versions file.
#   - Target file resolves from ASDF_DEFAULT_TOOL_VERSIONS_FILENAME (exported by
#     `just cluster-env <env> <cluster>` for per-cluster pins); falls back to `.tool-versions`.
#   - Auto-installs the asdf plugin when missing.
#   - Installs the binary WITHOUT setting any version (installs are shared across clusters).
#   - Sets the version by editing only that tool's line in the target file —
#     `asdf set` updates or appends the single entry; the recipe never copies,
#     generates, or rewrites the manifest. The target file must already exist.
#   - Always runs at the repo root (where the version files live), regardless of invocation dir.

# Usage: just install-tool <name> <version>
[group('setup-tools')]
install-tool name version:
    #!/usr/bin/env bash
    set -euo pipefail

    # Version files live at the repo root; write there regardless of invocation dir.
    cd "{{ justfile_directory() }}"

    versions_file="${ASDF_DEFAULT_TOOL_VERSIONS_FILENAME:-.tool-versions}"

    # Target must pre-exist — asdf set on a missing file would create a
    # one-tool partial manifest, violating the complete-copy invariant.
    if [[ ! -f "${versions_file}" ]]; then
        echo "Error: tool versions file '${versions_file}' does not exist."
        exit 1
    fi

    # 1. Plugin present? Auto-install if missing.
    # Note: no grep -q here — -q exits on first match, SIGPIPEs asdf (rc 141),
    # and pipefail would flip the branch. -x + >/dev/null reads all output.
    if ! asdf plugin list | grep -x '{{ name }}' >/dev/null; then
        echo "Installing asdf plugin {{ name }}..."
        asdf plugin add '{{ name }}'
    fi

    # 2. Binary installed? Install WITHOUT setting any version.
    if ! asdf list '{{ name }}' '{{ version }}' 2>/dev/null | tr -d ' *' | grep -x '{{ version }}' >/dev/null; then
        echo "Installing {{ name }} {{ version }}..."
        asdf install '{{ name }}' '{{ version }}'
    else
        echo "{{ name }} {{ version }} already installed"
    fi

    # 3. Set version in the active tool versions file — single-line edit only.
    echo "Setting {{ name }} {{ version }} in ${versions_file}..."
    asdf set '{{ name }}' '{{ version }}'
    echo "Done: {{ name }} {{ version }} → ${versions_file}"

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
set-env-var name value: build
    ./bin/cluster-ops env set-var --name={{ name }} --value={{ value }}
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
    set -a; source ".env.{{ env }}.{{ cluster }}"; set +a
    echo "Cluster: ${KOPS_CLUSTER_NAME}"
    echo "State  : ${KOPS_STATE_STORE}"
    echo "Type 'exit' or Ctrl-D to leave this environment."
    exec "$SHELL"

# Build the unified cluster-ops binary.
[group('setup-tools')]
build:
    go build -o bin/cluster-ops ./cmd/cluster-ops

# Update the GOOGLE_APPLICATION_CREDENTIALS line in a per-cluster env file.
# Thin wrapper — delegates platform-safe credential update to cluster-ops.
#
# Usage: just cluster-cred <env> <cluster> <key-path>
# Example: just cluster-cred dev my-cluster credentials/kops-cluster-creator.json
[group('cluster-ops')]
cluster-cred env cluster key: build
    ./bin/cluster-ops env set-cred {{ env }} {{ cluster }} {{ key }}

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
