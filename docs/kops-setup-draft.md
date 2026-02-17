# Kops Cluster Creation Guide

## Table of Contents
- [1. Prerequisites & Tooling](#1-prerequisites--tooling)
- [2. Authentication Setup](#2-authentication-setup)
- [3. Cluster Bootstrap](#3-cluster-bootstrap)
- [4. Verification & Access](#4-verification--access)
- [5. Post-Provisioning (Pulumi)](#5-post-provisioning-pulumi)

## 1. Prerequisites & Tooling
We use `asdf` to manage tool versions. The workflow is to bootstrap the task runner (`just`) manually, then use it to auto-configure the remaining toolset (kubectl, kops, gcloud, pulumi, etc.).

1.  **Install `asdf`**: [Installation Guide](https://asdf-vm.com/guide/getting-started.html)
2.  **Bootstrap `just`**:
    ```bash
    asdf add plugin just
    asdf install just
    ```
3.  **Install Remaining Tools**:
    This automated target adds the required `asdf` plugins and installs the versions defined in the `Justfile`. It includes:
    - [kubectl](https://kubernetes.io/docs/reference/kubectl/kubectl/) - CLI for cluster management.
    - [kops](https://kops.sigs.k8s.io/) - Kubernetes Operations for cluster provisioning.
    - [pulumi](https://www.pulumi.com/docs/) - Infrastructure as Code tool.
    - [gcloud](https://cloud.google.com/sdk/gcloud) - Google Cloud CLI.
    - [velero](https://velero.io/docs/) - Disaster recovery tool.
    - [mc](https://min.io/docs/minio/linux/reference/minio-mc.html) - MinIO Client.

    ```bash
    just install-asdf-plugins
    ```

    To upgrade a specific tool version (note: the tool must be defined in `.tool-versions`):
    ```bash
    just install-tool <tool_name> <version>
    # Example: just install-tool kubectl 1.28.9
    ```

## 2. Authentication Setup
You need the **Service Account Manager** key to bootstrap the environment.

1.  **Obtain Key**: Request the `sa-manager` JSON key from the project owner.
2.  **Save Key**: Place it in `./credentials/sa-manager.json`.
3.  **Configure Environment**:
    ```bash
    just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
    ```
    *Note: `direnv` will auto-load this from the generated `.envrc`.*

## 3. Cluster Bootstrap
Use the single-command recipe to enable APIs, configure IAM, and initialize the cluster.

**Command**:
```bash
just init-kops-cluster <PROJECT_ID> <BUCKET_NAME>
```

**What this does:**
*   **APIs**: Enables required GCP APIs (defined in `gcs-files/apis/enabled_apis.txt`) and disables unused ones.
*   **IAM**: Creates the `kops-cluster-creator` service account with permissions from `gcs-files/roles-permissions/`.
*   **Credentials**: Switches `GOOGLE_APPLICATION_CREDENTIALS` to the new `kops-cluster-creator` key.
*   **State Store**: Finds or creates the GCS bucket (`<BUCKET_NAME>`) for kops state.
*   **Provisioning**: Runs `kops create cluster`, builds the manifest, and applies it.

## 4. Verification & Access
Once the bootstrap command completes:

1.  **Validate Health**:
    ```bash
    just validate-cluster
    ```
    *Retries until the cluster is healthy.*

2.  **Status Overview**:
    ```bash
    just cluster-status
    ```

3.  **Interactive Access**:
    ```bash
    just k9s
    ```

4.  **Export Kubeconfig** (if needed manually):
    ```bash
    just export-kubeconfig
    # OR for a specific duration/name
    just export-named-kubeconfig <NAME> <HOURS>
    ```

## 5. Post-Provisioning (Pulumi)
To deploy the application stack after the cluster is running:

1.  **Initialize & Deploy**:
    ```bash
    just pulumi-init-and-deploy <STACK_NAME> <FROM_STACK> <PROJECT_ID> <KEYRING> <KEY> <BUCKET>
    ```
    *Creates Pulumi IAM, KMS keys, state bucket, and deploys initial resources.*
