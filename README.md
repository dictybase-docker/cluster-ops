- [Enabling/Disabling APIs](docs/api.md)
- [Service Account](docs/sa.md)
- [Starting the Cluster](docs/cluster.md)

# Getting Started

## Prerequisites

- [just](https://github.com/casey/just?tab=readme-ov-file#installation)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kops](https://kops.sigs.k8s.io/getting_started/install/)
- [gcloud](https://cloud.google.com/sdk/docs/install)
- [pulumi](https://www.pulumi.com/docs/get-started/install/)

## Cluster Setup

### 1. Create Service Account Manager

To set up a cluster, the project owner must first provide a `service account key` for the `service account manager` service account.

As the project owner, run

```sh
just create-sa-manager <project_id>
```

This will create a service account named `sa-manager` required to:
    - Create the `kops-cluster-creator` service account
    - Enable the required GCP APIs to create and manage the cluster

It will also create a JSON key file for the service account in the `credentials/` directory.

### 2. Enable Required APIs

Enable all required Google Cloud APIs using the predefined list:

```sh
just gcp-api enable-apis <project_id> gcs-files/apis/enabled_apis.txt
```

Disable unnecessary Cloud APIs using the predefined list:

```sh
just gcp-api disable-apis <project_id> gcs-files/apis/disable_enabled_apis.txt
```

See [API Management documentation](docs/api.md) for details on enabling/disabling APIs.

### 3. Create the `kops cluster creator` Service Account

The `kops cluster creator` service account is a dedicated service account with the necessary permissions to create and manage the Kubernetes cluster using the kops tool.

Create this service account with:

```sh
just gcp-sa create-sa <project_id> kops-cluster-creator gcs-files/roles-permissions/kops-cluster-creator-roles.txt credentials/kops-cluster-creator.json
```

This will create a service account named `kops-cluster-creator` with the roles defined in `gcs-files/roles-permissions/kops-cluster-creator-roles.txt`, and save the JSON key file to `credentials/kops-cluster-creator.json`.

### 4. Set Application Default Credentials

Set the key file as the Google Application Default Credentials in your environmental variables:

```
export GOOGLE_APPLICATION_CREDENTIALS="${PWD}/credentials/kops-cluster-creator.json"
```
This provides authentication to the Go Google Cloud client library.

### 5. Set Up kops State Store and Initialize the Cluster

The `create-kops-cluster` recipe sets up the state store bucket and initializes the Kubernetes cluster:

```sh
just create-kops-cluster <project_id> <bucket_name>
```
