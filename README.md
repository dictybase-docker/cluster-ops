# Reference

- [Enabling/Disabling APIs](docs/api.md)
- [Service Account](docs/sa.md)
- [Starting the Cluster](docs/cluster.md)

# Getting Started

This documentation provides instructions on how to a start up a Kubernetes cluster on Google Cloud Platform for _dictyBase applications_

## Prerequisites

### asdf

We use `asdf` to install and manage the version of the required binaries for the managing the cluster. Install asdf by following the documentation below.

- [asdf](
https://asdf-vm.com/guide/getting-started.html#_1-install-asdf)

### just

just is used to run the various commands defined in the project.

First, install the `just` plugin for `asdf`:

```
asdf add plugin just
```

Now, to install just, simply run:
```
asdf install just
```
The versions of the binaries used for the project is specified in the `.tool-versions` file, and will be picked up by `asdf`.

### Install direnv

- [direnv v2.34.0]()

### Remaining binaries

Since `just` is now installed, we can run a single just recipe to install the remaining binaries: 

```
just install-asdf-plugins
```

This command will install the following binaries:

- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kops](https://kops.sigs.k8s.io/getting_started/install/)
- [gcloud](https://cloud.google.com/sdk/docs/install)
- [pulumi](https://www.pulumi.com/docs/get-started/install/)

## Cluster Setup

### 1. Acquire the Service Account Manager Key

You must acquire a key of the `Service Account Manager` service account from the owner of the Google Cloud Project where the cluster will be set up.

The Service Account Manager service account is needed to:
    - create all other services accounts needed to create and manage the cluster
    - enable the required Google Cloud APIs to create and manage the cluster

The project owner must run:

```sh
just create-sa-manager <project_id>
```

This will create a service account named `sa-manager` and create a JSON key file for the service account in their `./credentials` directory.

Have the project owner send the key file to you. Place it in your `./credentials` directory.

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
