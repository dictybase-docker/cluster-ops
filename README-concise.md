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
4. Install required tools:
   ```bash
   just install-asdf-plugins
   ```
   This installs: [kubectl](https://kubernetes.io/docs/reference/kubectl/), [kops](https://kops.sigs.k8s.io/), [gcloud](https://cloud.google.com/sdk/gcloud), [pulumi](https://www.pulumi.com/).

## Cluster Setup

### Quick Setup (Recommended)
```bash
# 1. Get sa-manager.json key from GCP project owner and save to ./credentials/
just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"

# 2. Create cluster with single command
just init-kops-cluster <project_id> <bucket_name>
```

### Manual Setup
If needed, perform steps individually:

1. Enable/disable APIs:
   ```bash
   just gcp-api enable-apis <project_id> gcs-files/apis/enabled_apis.txt
   just gcp-api disable-apis <project_id> gcs-files/apis/disable_enabled_apis.txt
   ```

2. Create kops service account:
   ```bash
   just gcp-sa create-sa <project_id> kops-cluster-creator gcs-files/roles-permissions/kops-cluster-creator-roles.txt credentials/kops-cluster-creator.json
   just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/kops-cluster-creator.json"
   ```

3. Initialize cluster:
   ```bash
   just create-kops-cluster <project_id> <bucket_name>
   ```

## Application Deployment with Pulumi

### Quick Setup (Recommended)
```bash
# Initialize Pulumi environment and deploy the initial required resources in one command
just pulumi-init-and-deploy <stack> <from-stack> <project_id> <keyring_name> <key_name> <bucket_name> [location]
```

### Manual Setup
If needed, perform steps individually:

1. Create Pulumi Manager Service Account:
   ```bash
   just gcp-sa create-sa <project_id> pulumi-manager gcs-files/roles-permissions/pulumi-manager-roles.txt credentials/pulumi-manager.json
   ```

2. Set the PULUMI_GCP_CREDENTIALS environment variable:
   ```bash
   just set-env-var PULUMI_GCP_CREDENTIALS "${PWD}/credentials/pulumi-manager.json"
   ```

3. Create Key Ring and Key for Pulumi secrets:
   ```bash
   just gcp-kms create-keyring-and-key <project_id> <keyring_name> <key_name> credentials/pulumi-manager.json [location]
   ```

4. Initialize Pulumi State Store:
   ```bash
   just gcp-pulumi pulumi-gcs-setup credentials/pulumi-manager.json <bucket_name> "" [location]
   ```

5. Initialize Project Stack:
   ```bash
   # Set the PULUMI_SECRET_PROVIDER environment variable
   export PULUMI_SECRET_PROVIDER="gcpkms://projects/<project_id>/locations/<location>/keyRings/<keyring_name>/cryptoKeys/<key_name>"
   
   # Initialize a new stack from an existing one
   just gcp-pulumi new-stack-from <folder> <stack> <from-stack>
   ```

6. Create Pulumi Resources:
   ```bash
   just gcp-pulumi create-resource <folder> <stack>
   ```
