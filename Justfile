mod gcp-analysis 'just_modules/analysis.justfile'
mod gcp-sa 'just_modules/sa.justfile'
mod gcp-role 'just_modules/role.justfile'
mod gcp-api 'just_modules/api.justfile'
mod gcp-image 'just_modules/image.justfile'
mod gcp-kms 'just_modules/kms.justfile'
mod gcp-pulumi 'just_modules/pulumi.justfile'
mod gcp-cluster 'just_modules/cluster.justfile'

dagger_version := "v0.11.9"
container_module := "github.com/dictybase-docker/dagger-of-dcr/container-image@main"
bin_path := `mktemp -d`
action_bin := bin_path + "/actions"
dagger_bin := bin_path + "/dagger"
base_gha_download_url := "https://github.com/dictybase-docker/github-actions/releases/download/v2.10.0/action_2.10.0_"
gha_download_url := if os() == "macos" { base_gha_download_url + "darwin_arm64" } else { base_gha_download_url + "linux_amd64" }
file_suffix := ".tar.gz"
dagger_file := if os() == "macos" { "darwin_arm64" + file_suffix } else { "linux_amd64" + file_suffix }

set dotenv-filename := x"${CLUSTER_ENV_FILE:-.env}"

# Run Golang tests using Dagger
test:
    dagger -m github.com/dictybase-docker/dagger-of-dcr/golang@main call with-golang-version with-gotest-sum-formatter test --src "."

setup: install-gha-binary install-dagger-binary

[group('setup-tools')]
install-gha-binary:
    @curl -L -o {{ action_bin }} {{ gha_download_url }}
    @chmod +x {{ action_bin }}

[group('setup-tools')]
install-dagger-binary:
    {{ action_bin }} sd --dagger-version {{ dagger_version }} --dagger-bin-dir {{ bin_path }} --dagger-file {{ dagger_file }}

# ref: Git reference (branch, tag, or commit hash) to use for the build
# user: Docker Hub username
# pass: Docker Hub password

# Build and publish the backup image to Docker Hub
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

# Run aider AI coding assistant with specific configuration
aider:
    #!/usr/bin/env bash
    set -euxo pipefail
    export GOOGLE_APPLICATION_CREDENTIALS="{{ invocation_directory()}}/credentials/dcr-experiments-cloud-manager.json"
    aider --architect --model 'vertex_ai/claude-3-5-sonnet-v2@20241022' \
                   --no-auto-commits \
                   --no-auto-lint \
                   --vim --cache-prompts \
                   --cache-keepalive-pings 3 \
                   --watch-files

install-asdf-plugins:
  asdf plugin add gcloud
  asdf plugin add kubectl
  asdf plugin add kops
  asdf plugin add pulumi
  asdf install

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

# Initialize a Kubernetes cluster with kops
init-kops-cluster project_id bucket_name:
    # Update GOOGLE_APPLICATION_CREDENTIALS env var
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
    # Enable required APIs
    just gcp-api enable-apis {{ project_id }} gcs-files/apis/enabled_apis.txt
    
    # Disable unnecessary APIs  
    just gcp-api disable-apis {{ project_id }} gcs-files/apis/disable_enabled_apis.txt
    
    # Create kops cluster creator service account
    just gcp-sa create-sa {{ project_id }} kops-cluster-creator gcs-files/roles-permissions/kops-cluster-creator-roles.txt credentials/kops-cluster-creator.json
    
    # Update GOOGLE_APPLICATION_CREDENTIALS env var
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/kops-cluster-creator.json"
    
    # Set up kops state store and initialize cluster
    just gcp-cluster create-kops-cluster {{ project_id }} {{ bucket_name }}

# Setup Pulumi deployment environment
# Parameters:
#   project_id: GCP project ID
#   keyring_name: Name of the KMS keyring
#   key_name: Name of the KMS key
#   bucket_name: Name of the GCS bucket for Pulumi state
#   location: Google Cloud region (optional, defaults to us-central1)
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
#   project_id: GCP project ID
#   keyring_name: Name of the KMS keyring
#   key_name: Name of the KMS key
#   bucket_name: Name of the GCS bucket for Pulumi state
#   location: Google Cloud region (optional, defaults to us-central1)
pulumi-init-and-deploy stack from-stack project_id keyring_name key_name bucket_name location="us-central1":
    #!/usr/bin/env bash
    just initialize-pulumi {{ project_id }} {{ keyring_name }} {{ key_name }} {{ bucket_name }} {{ location }}
    export PULUMI_SECRET_PROVIDER="gcpkms://projects/{{ project_id }}/locations/{{ location }}/keyRings/{{ keyring_name }}/cryptoKeys/{{ key_name }}"
    
    echo "Creating Initial Resources"
    just gcp-pulumi create-multiple-resources {{ stack }} {{ from-stack }} "./pulumi-files/initial-resources.txt"
