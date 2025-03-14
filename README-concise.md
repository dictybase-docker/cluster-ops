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
