# cluster-ops

Deploy a Kubernetes cluster on Google Cloud Platform for dictyBase applications.

## Documentation
- [APIs](docs/api.md)
- [Service Accounts](docs/sa.md)
- [Cluster](docs/cluster.md)

## Prerequisites
1. Install [asdf](https://asdf-vm.com/guide/getting-started.html#_1-install-asdf)
2. Install [just](https://just.systems/man/en/):
   Follow the installation instructions at https://just.systems/man/en/chapter_1.html
   You can install using package managers, pre-built binaries, or other methods as described in the documentation.
3. Install [direnv](https://direnv.net/):
   Install using your system's package manager or [download directly](https://direnv.net/docs/installation.html)
   **Important**: [Hook direnv into your shell](https://direnv.net/docs/hook.html) after installation
4. Install required tools (one per `.tool-versions` entry):
   ```bash
   just install-tool --name kubectl --version 1.28.8
   just install-tool --name kops --version v1.29.2
   ```
   Each call installs the exact version (creating the asdf plugin if needed) and pins it to `.tool-versions` — or the per-cluster file when `ASDF_DEFAULT_TOOL_VERSIONS_FILENAME` is set. Managed tools: [kubectl](https://kubernetes.io/docs/reference/kubectl/), [kops](https://kops.sigs.k8s.io/), [pulumi](https://www.pulumi.com/), [velero](https://velero.io/), and [mc](https://min.io/docs/minio/linux/reference/minio-mc.html).

## Cluster Setup

### Quick Setup (Recommended)
```bash
# 1. Get sa-manager.json key from GCP project owner and save to ./credentials/<project_id>/
just set-env-var --name GOOGLE_APPLICATION_CREDENTIALS --value "${PWD}/credentials/<project_id>/sa-manager.json"

# 2. Follow the phased HA workflow (see docs/kops-setup-draft.md)
just gcp-cluster create-state-bucket --project <project_id> --bucket-name <bucket_name>
just gcp-cluster create-cluster-config --project <project_id> --bucket-name <bucket_name>
just gcp-cluster update-cluster
```

### Manual Setup
If needed, perform steps individually:

1. Enable/disable APIs:
   ```bash
   just gcp-api enable-apis --project <project_id> --api-file gcs-files/apis/enabled_apis.txt
   just gcp-api disable-apis --project <project_id> --api-file gcs-files/apis/disable_enabled_apis.txt
   ```

2. Create kops service account:
   ```bash
   just gcp-sa create-sa --project <project_id> --sa-name kops-cluster-creator --roles-file gcs-files/roles-permissions/kops-cluster-creator-roles.txt --output-file credentials/kops-cluster-creator.json
   just set-env-var --name GOOGLE_APPLICATION_CREDENTIALS --value "${PWD}/credentials/kops-cluster-creator.json"
   ```

3. Initialize cluster:
   ```bash
   just gcp-cluster create-state-bucket --project <project_id> --bucket-name <bucket_name>
   just gcp-cluster create-cluster-config --project <project_id> --bucket-name <bucket_name>
   just gcp-cluster update-cluster
   ```

## Application Deployment with Pulumi

### Quick Setup (Recommended)
```bash
# Initialize Pulumi environment and deploy the initial required resources in one command
just pulumi-init-and-deploy --stack <stack> --from-stack <from-stack> --project-id <project_id> --keyring-name <keyring_name> --key-name <key_name> --bucket-name <bucket_name> [--location <location>]
```

### Manual Setup
If needed, perform steps individually:

1. Create Pulumi Manager Service Account:
   ```bash
   just gcp-sa create-sa --project <project_id> --sa-name pulumi-manager --roles-file gcs-files/roles-permissions/pulumi-manager-roles.txt --output-file credentials/pulumi-manager.json
   ```

2. Set the PULUMI_GCP_CREDENTIALS environment variable:
   ```bash
   just set-env-var --name PULUMI_GCP_CREDENTIALS --value "${PWD}/credentials/pulumi-manager.json"
   ```

3. Create Key Ring and Key for Pulumi secrets:
   ```bash
   just gcp-kms create-keyring-and-key --project-id <project_id> --keyring-name <keyring_name> --key-name <key_name> --credentials-file credentials/pulumi-manager.json [--location <location>]
   ```

4. Initialize Pulumi State Store:
   ```bash
   just gcp-pulumi pulumi-gcs-setup --sa-json-path credentials/pulumi-manager.json --gcs-bucket <bucket_name> [--location <location>]
   ```

5. Initialize Project Stack:
   ```bash
   # Set the PULUMI_SECRET_PROVIDER environment variable
   export PULUMI_SECRET_PROVIDER="gcpkms://projects/<project_id>/locations/<location>/keyRings/<keyring_name>/cryptoKeys/<key_name>"
   
   # Initialize a new stack from an existing one
   just gcp-pulumi new-stack-from --folder <folder> --stack <stack> --from-stack <from-stack>
   ```

6. Create Pulumi Resources:
   ```bash
   just gcp-pulumi create-resource --folder <folder> --stack <stack>
   ```
