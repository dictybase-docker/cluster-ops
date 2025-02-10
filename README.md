- [Enabling/Disabling APIs](docs/api.md)
- [Service Account](docs/sa.md)
- [Starting the Cluster](docs/cluster.md)

# Getting Started

## Prerequisites

- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kops](https://kops.sigs.k8s.io/getting_started/install/)
- [gcloud](https://cloud.google.com/sdk/docs/install)
- [pulumi](https://www.pulumi.com/docs/get-started/install/)

## Cluster Setup

### For the Project Owner

To set up a cluster, the project owner must first provide a `service account key` for the `service account manager` service account.

As the project owner, run

```sh
just create-sa-manager <project_id>
```

This will create a service account named `sa-manager` with the necessary roles and permissions to manage the project resources. It will also create a JSON key file for the service account in the `credentials/` directory.

### Create the `kops cluster creator` Service Account

The `kops cluster creator` service account is a dedicated service account with the necessary permissions to create and manage the Kubernetes cluster using the kops tool.

It can also be used to enable / disable GCP APIs.

Create this service account with:

```sh
just gcp-sa create-sa <project_id> kops-cluster-creator gcs-files/roles-permissions/kops-cluster-creator-roles.txt credentials/kops-cluster-creator.json
```

This will create a service account named `kops-cluster-creator` with the roles defined in `gcs-files/roles-permissions/kops-cluster-creator-roles.txt`, and save the JSON key file to `credentials/kops-cluster-creator.json`.

### Enable Required APIs

Enable all required Google Cloud APIs using the predefined list:

```sh
just gcp-api enable-apis <project_id> gcs-files/apis/enabled_apis.txt
```

This enables APIs required for cluster operations including:
- Cloud Functions API
- Kubernetes Engine API
- Cloud DNS API
- Cloud Storage API
- and 30+ other essential services

See [API Management documentation](docs/api.md) for details on enabling/disabling APIs.

### Set Up kops State Store and Initialize the Cluster

kops requires a state store to keep track of the cluster configuration. We'll use a Google Cloud Storage bucket for this purpose. The `create-kops-cluster` recipe sets up the state store bucket and initializes the Kubernetes cluster:

```sh
just create-kops-cluster <project_id> <bucket_name>
```

This will:
1. Build the required Go binaries
2. Find or create the GCS bucket for kops state store
3. Build the kops-cluster-creator binary
4. Run the kops-cluster-creator to initiate cluster creation
5. Update the cluster with any pending changes
6. Validate the cluster setup
7. Display the current cluster status

After the cluster is created, you can interact with it using `kubectl` and other Kubernetes tools.
