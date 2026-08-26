# Modules
set minimum-version := '1.46.0'

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
#     `just cluster-env --env <env> --cluster <cluster>` for per-cluster pins); falls back to `.tool-versions`.
#   - Auto-installs the asdf plugin when missing.
#   - Installs the binary WITHOUT setting any version (installs are shared across clusters).
#   - Sets the version by editing only that tool's line in the target file —
#     `asdf set` updates or appends the single entry; the recipe never copies,
#     generates, or rewrites the manifest. The target file must already exist.
#   - Always runs at the repo root (where the version files live), regardless of invocation dir.

# Usage: just install-tool --name <tool> --version <v>
[arg("name", long="name", short="n", help="Tool name")]
[arg("version", long="version", short="v", help="Tool version")]
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

# Install every tool pinned in the active tool versions file.
#   - Resolves the file from ASDF_DEFAULT_TOOL_VERSIONS_FILENAME (per-cluster)
#     or falls back to the repo-root .tool-versions (dev default).
#   - Adds each asdf plugin, then runs `asdf install` (no args) to install
#     every pinned binary in one pass.
# Usage: just install-tools  (at repo root for dev, or inside `just cluster-env`)
[group('setup-tools')]
install-tools:
    #!/usr/bin/env bash
    set -euo pipefail
    cd "{{ justfile_directory() }}"
    file="${ASDF_DEFAULT_TOOL_VERSIONS_FILENAME:-.tool-versions}"
    if [[ ! -f "${file}" ]]; then
        echo "Error: tool versions file '${file}' does not exist."
        echo "Create it first — see Section 1.4 of docs/kops-setup-draft.md."
        exit 1
    fi
    echo "Installing plugins from ${file}..."
    grep -vE '^\s*(#|$)' "${file}" | cut -d' ' -f1 | while read -r tool; do
        asdf plugin add "${tool}" 2>/dev/null || true
    done
    echo "Installing all pinned versions from ${file}..."
    asdf install
    echo "Done: every tool in ${file} installed."

# --- Development & Testing ---

# Run Golang tests using Dagger
[group('dev-tools')]
test:
    dagger -m github.com/dictybase-docker/dagger-of-dcr/golang@main call with-golang-version with-gotest-sum-formatter test --src "."

# Run bootstrap-bundle contract tests
[group('dev-tools')]
test-bootstrap:
    ./scripts/test-bootstrap-bundle.sh

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

# Set an environment variable in .envrc.
# Usage: just set-env-var --name <name> --value <value>
[arg("name", long="name", short="n", help="Env var name")]
[arg("value", long="value", short="v", help="Env var value")]
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

# Build and publish the backup image to Docker Hub.
# Usage: just build-publish-backup-image --ref <ref> --user <user> --pass <pass>
[arg("ref", long="ref", short="r", help="Git ref (branch, tag, or commit)")]
[arg("user", long="user", short="u", help="Docker Hub username")]
[arg("pass", long="pass", short="p", help="Docker Hub password")]
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

# Create a per-cluster env file for credentials and local paths only.
# Does NOT write Git-owned identity (cluster name, state store, project, kubernetesVersion).
# Those live in config/kops/<cluster>/*.yaml.
# Usage: just create-cluster-env --env <env> --cluster <cluster> --project <id> [--credentials <sa.json>] [--ssh-key <pub>] [--kubeconfig <path>] [--force yes]
# Writes PROJECT_ID plus any credential/path args that exist. Credentials may be
# added later with just cluster-cred. Does not write Git-owned identity
# (KOPS_CLUSTER_NAME, KOPS_STATE_STORE, BUCKET_NAME, KUBERNETES_VERSION).
[arg("env", long="env", short="e", help="Environment name (dev, staging, prod)")]
[arg("cluster", long="cluster", short="c", help="Short cluster name; used only as the env filename suffix")]
[arg("project", long="project", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("credentials", long="credentials", help="Path to the GCP service-account JSON (defaults to GOOGLE_APPLICATION_CREDENTIALS)")]
[arg("ssh_key", long="ssh-key", help="SSH public key path (defaults to SSH_KEY)")]
[arg("kubeconfig", long="kubeconfig", help="Kubeconfig path (defaults to KUBECONFIG or clusters/<cluster>/kubeconfig)")]
[arg("pulumi_gcp_credentials", long="pulumi-gcp-credentials", help="Pulumi GCP SA JSON path (defaults to PULUMI_GCP_CREDENTIALS)")]
[arg("pulumi_secret_provider", long="pulumi-secret-provider", help="Pulumi secrets provider URI (defaults to PULUMI_SECRET_PROVIDER)")]
[arg("pulumi_backend_url", long="pulumi-backend-url", help="Pulumi state backend URI (defaults to PULUMI_BACKEND_URL)")]
[arg("force", long="force", pattern="yes|no", help="Overwrite an existing env file")]
[group('cluster-ops')]
create-cluster-env env="" cluster="" project="" credentials="" ssh_key="" kubeconfig="" pulumi_gcp_credentials="" pulumi_secret_provider="" pulumi_backend_url="" force="no":
    #!/usr/bin/env bash
    set -euo pipefail

    env_name="{{ env }}"
    cluster_name="{{ cluster }}"
    if [ -z "${env_name}" ] || [ -z "${cluster_name}" ]; then
        echo "ERROR: --env and --cluster are required."
        exit 1
    fi

    dest="{{ justfile_directory() }}/.env.${env_name}.${cluster_name}"
    if [ -e "${dest}" ] && [ "{{ force }}" != "yes" ]; then
        echo "ERROR: ${dest} already exists. Pass --force yes to overwrite."
        exit 1
    fi

    project_id="${PROJECT_ID:-{{ project }}}"
    if [ -z "${project_id}" ]; then
        cluster_manifest="{{ justfile_directory() }}/config/kops/${cluster_name}/cluster.yaml"
        if [ -f "${cluster_manifest}" ]; then
            project_id="$(grep -E '^[[:space:]]*project:[[:space:]]*' "${cluster_manifest}" | head -n 1 | awk '{print $2}' | tr -d '"'\''')"
        fi
    fi

    if [ -z "${project_id}" ]; then
        echo "ERROR: Could not determine GCP project id. Pass --project <id>, set PROJECT_ID, or define 'project:' in config/kops/${cluster_name}/cluster.yaml."
        exit 1
    fi

    cred="{{ credentials }}"
    if [ -z "${cred}" ]; then
        cred="${GOOGLE_APPLICATION_CREDENTIALS:-}"
    fi
    if [ -z "${cred}" ]; then
        if [ -f "{{ justfile_directory() }}/credentials/${project_id}/kops-cluster-creator.json" ]; then
            cred="{{ justfile_directory() }}/credentials/${project_id}/kops-cluster-creator.json"
        elif [ -f "{{ justfile_directory() }}/credentials/kops-cluster-creator.json" ]; then
            cred="{{ justfile_directory() }}/credentials/kops-cluster-creator.json"
        fi
    fi
    if [ -n "${cred}" ]; then
        if [ ! -f "${cred}" ]; then
            echo "ERROR: credentials file not found: ${cred}"
            exit 1
        fi
        cred="$(cd "$(dirname "${cred}")" && pwd)/$(basename "${cred}")"
    fi

    ssh_key="${SSH_KEY:-{{ ssh_key }}}"
    if [ -z "${ssh_key}" ]; then
        if [ -f "{{ justfile_directory() }}/clusters/${cluster_name}/${cluster_name}.pub" ]; then
            ssh_key="{{ justfile_directory() }}/clusters/${cluster_name}/${cluster_name}.pub"
        elif [ -f "{{ justfile_directory() }}/clusters/${project_id}/${project_id}.pub" ]; then
            ssh_key="{{ justfile_directory() }}/clusters/${project_id}/${project_id}.pub"
        elif [ -f "{{ justfile_directory() }}/credentials/${project_id}/k8sVM.pub" ]; then
            ssh_key="{{ justfile_directory() }}/credentials/${project_id}/k8sVM.pub"
        fi
    fi
    if [ -n "${ssh_key}" ] && [ ! -f "${ssh_key}" ]; then
        echo "ERROR: SSH public key not found: ${ssh_key}"
        exit 1
    fi
    if [ -n "${ssh_key}" ]; then
        ssh_key="$(cd "$(dirname "${ssh_key}")" && pwd)/$(basename "${ssh_key}")"
    fi

    kubeconfig="${KUBECONFIG:-{{ kubeconfig }}}"
    if [ -z "${kubeconfig}" ]; then
        if [ -f "{{ justfile_directory() }}/clusters/${cluster_name}/kubeconfig" ]; then
            kubeconfig="{{ justfile_directory() }}/clusters/${cluster_name}/kubeconfig"
        elif [ -f "{{ justfile_directory() }}/clusters/${project_id}/kubeconfig" ]; then
            kubeconfig="{{ justfile_directory() }}/clusters/${project_id}/kubeconfig"
        else
            kubeconfig="{{ justfile_directory() }}/clusters/${cluster_name}/kubeconfig"
        fi
    fi

    pulumi_creds="${PULUMI_GCP_CREDENTIALS:-{{ pulumi_gcp_credentials }}}"
    if [ -z "${pulumi_creds}" ]; then
        pulumi_creds="{{ justfile_directory() }}/credentials/${project_id}/pulumi-manager.json"
    fi

    pulumi_kms="${PULUMI_SECRET_PROVIDER:-{{ pulumi_secret_provider }}}"
    if [ -z "${pulumi_kms}" ]; then
        pulumi_kms="gcpkms://projects/${project_id}/locations/us-central1/keyRings/${cluster_name}/cryptoKeys/${cluster_name}"
    fi

    pulumi_backend="${PULUMI_BACKEND_URL:-{{ pulumi_backend_url }}}"
    if [ -z "${pulumi_backend}" ]; then
        pulumi_backend="gs://pulumi-state-${project_id}"
    fi

    write_kv() {
        local key="$1" val="$2"
        if [ -z "${val}" ]; then
            return 0
        fi
        if [[ "${val}" == *$'\n'* ]]; then
            echo "ERROR: ${key} value must not contain newlines."
            exit 1
        fi
        printf '%s=%s\n' "${key}" "$(printf '%q' "${val}")"
    }

    umask 077
    tmp="$(mktemp "{{ justfile_directory() }}/.env.create.XXXXXX")"
    trap 'rm -f "${tmp}"' EXIT

    {
        echo "# Per-cluster operator environment for ${env_name}/${cluster_name}"
        echo "# PROJECT_ID plus credentials/paths. After bootstrap, cluster identity lives in config/kops/${cluster_name}/."
        echo "# Do not store KOPS_CLUSTER_NAME, KOPS_STATE_STORE, BUCKET_NAME, or KUBERNETES_VERSION here."
        echo "# Gitignored. Do not commit."
        write_kv PROJECT_ID "${project_id}"
        write_kv GOOGLE_APPLICATION_CREDENTIALS "${cred}"
        write_kv SSH_KEY "${ssh_key}"
        write_kv KUBECONFIG "${kubeconfig}"
        write_kv PULUMI_GCP_CREDENTIALS "${pulumi_creds}"
        write_kv PULUMI_SECRET_PROVIDER "${pulumi_kms}"
        write_kv PULUMI_BACKEND_URL "${pulumi_backend}"
    } > "${tmp}"

    mv "${tmp}" "${dest}"
    trap - EXIT
    echo "Wrote ${dest}"
    echo "Activate with: just cluster-env --env ${env_name} --cluster ${cluster_name}"

# Activate a per-cluster env file by sourcing it into a sub-shell.
# This is the recommended way to switch between clusters (dev/staging/prod).
# The sub-shell inherits credential/path vars; 'exit' or Ctrl-D returns to the parent shell.
#
# Usage: just cluster-env --env <env> --cluster <cluster>
# Example: just cluster-env --env dev --cluster my-cluster
[arg("env", long="env", short="e", help="Environment name (.env.<env>.<cluster>)")]
[arg("cluster", long="cluster", short="c", help="Cluster name")]
[group('cluster-ops')]
cluster-env env cluster:
    #!/usr/bin/env bash
    set -euo pipefail
    env_file="{{ justfile_directory() }}/.env.{{ env }}.{{ cluster }}"
    if [ ! -f "${env_file}" ]; then
        echo "ERROR: env file not found: ${env_file}"
        echo "Create it with: just create-cluster-env --env {{ env }} --cluster {{ cluster }} --project <id>"
        exit 1
    fi
    set -a; source "${env_file}"; set +a
    echo "Env file: ${env_file}"
    echo "PROJECT_ID=${PROJECT_ID:-UNSET}"
    echo "GOOGLE_APPLICATION_CREDENTIALS=${GOOGLE_APPLICATION_CREDENTIALS:-UNSET}"
    echo "SSH_KEY=${SSH_KEY:-UNSET}"
    echo "KUBECONFIG=${KUBECONFIG:-UNSET}"
    echo "Type 'exit' or Ctrl-D to leave this environment."
    exec "$SHELL"

# Build the unified cluster-ops binary.
[group('setup-tools')]
build:
    go build -o bin/cluster-ops ./cmd/cluster-ops

# Update the GOOGLE_APPLICATION_CREDENTIALS line in a per-cluster env file.
# Thin wrapper — delegates platform-safe credential update to cluster-ops.
#
# Usage: just cluster-cred --env <env> --cluster <cluster> --key <path>
# Example: just cluster-cred --env dev --cluster my-cluster --key credentials/kops-cluster-creator.json
[arg("env", long="env", short="e", help="Environment name")]
[arg("key", long="key", short="k", help="Service account key path")]
[arg("cluster", long="cluster", short="c", help="Cluster name")]
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
[arg("key_name", long="key-name", short="k", help="KMS key name")]
[arg("location", long="location", short="l", help="Google Cloud region")]
[arg("project_id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="GCS bucket for Pulumi state")]
[arg("keyring_name", long="keyring-name", short="r", help="KMS keyring name")]
[group('pulumi-ops')]
initialize-pulumi project_id="" keyring_name key_name bucket_name location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project_id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    echo "Step 1: Creating Pulumi Manager Service Account Key"
    just gcp-sa create-sa --project "${project_id}" --sa-name pulumi-manager --roles-file gcs-files/roles-permissions/pulumi-manager-roles.txt --output-file credentials/pulumi-manager.json

    echo "Step 2: Setting PULUMI_GCP_CREDENTIALS environment variable"
    export PULUMI_GCP_CREDENTIALS="${PWD}/credentials/pulumi-manager.json"

    echo "Step 3: Creating Key Ring and Key for Pulumi secrets encryption"
    just gcp-kms create-keyring-and-key --project-id "${project_id}" --keyring-name {{ keyring_name }} --key-name {{ key_name }} --credentials-file credentials/pulumi-manager.json --location {{ location }}

    echo "Step 4: Initializing Pulumi State Store"
    just gcp-pulumi pulumi-gcs-setup --sa-json-path credentials/pulumi-manager.json --gcs-bucket {{ bucket_name }} --location {{ location }}
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
[arg("stack", long="stack", short="s", help="Initial stack name")]
[arg("key_name", long="key-name", short="k", help="KMS key name")]
[arg("location", long="location", short="l", help="Google Cloud region")]
[arg("from-stack", long="from-stack", short="F", help="Stack to copy config from")]
[arg("project_id", long="project-id", short="p", help="GCP project id (defaults to PROJECT_ID env var)")]
[arg("bucket_name", long="bucket-name", short="b", help="GCS bucket for Pulumi state")]
[arg("keyring_name", long="keyring-name", short="r", help="KMS keyring name")]
[group('pulumi-ops')]
pulumi-init-and-deploy stack from-stack project_id="" keyring_name key_name bucket_name location="us-central1":
    #!/usr/bin/env bash
    set -euo pipefail

    project_id="${PROJECT_ID:-{{ project_id }}}"
    if [ -z "$project_id" ]; then
        echo "ERROR: no project id — set PROJECT_ID or pass --project-id."
        exit 1
    fi

    just initialize-pulumi --project-id "${project_id}" --keyring-name {{ keyring_name }} --key-name {{ key_name }} --bucket-name {{ bucket_name }} --location {{ location }}
    export PULUMI_SECRET_PROVIDER="gcpkms://projects/${project_id}/locations/{{ location }}/keyRings/{{ keyring_name }}/cryptoKeys/{{ key_name }}"

    echo "Creating Initial Resources"
    just gcp-pulumi create-multiple-resources --stack {{ stack }} --from-stack {{ from-stack }} --resources-file "./pulumi-files/initial-resources.txt"
    just gcp-pulumi create-multiple-resources --stack {{ stack }} --from-stack {{ from-stack }} --resources-file "./pulumi-files/database-and-storage-resources.txt"
