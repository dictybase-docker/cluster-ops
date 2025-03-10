# Reference

- [Enabling/Disabling APIs](docs/api.md)
- [Service Account](docs/sa.md)
- [Starting the Cluster](docs/cluster.md)

# Getting Started

This documentation provides instructions on how to a start up a Kubernetes cluster on Google Cloud Platform for _dictyBase applications_

To begin, first clone this repository: 

```
git clone https://github.com/dictybase-docker/cluster-ops.git
```
## Required Binaries

### asdf

We use `asdf` to install and manage the version of the required binaries for the managing the cluster. Install asdf by following the documentation below.

- [asdf](
https://asdf-vm.com/guide/getting-started.html#_1-install-asdf)

### just

`just` is used to run the various commands defined in the project. These include commands for setting up Google Cloud Platform resources and initializing the kubernetes cluster with kops.

First, install the `just` plugin for `asdf`:

```
asdf add plugin just
```

Now, to install just, simply run:
```
asdf install just
```
The versions of `just` and the other required binaries used for the project are specified in the `.tool-versions` file, and will be picked up by `asdf`.

### Remaining binaries

Since `just` is now installed, we can run a single just recipe to install the remaining binaries: 

```
just install-asdf-plugins
```

This command will install the following binaries:

- [kubectl](https://kubernetes.io/docs/reference/kubectl/kubectl/)
- [kops](https://kops.sigs.k8s.io/)
- [gcloud](https://cloud.google.com/sdk/gcloud)
- [pulumi](https://www.pulumi.com/docs/)
- [direnv](https://direnv.net/)

## Environmental Variables

Environmental variables for the project are managed by `direnv`, which will load the variables defined in the `.envrc` file at the root of the project.

This file will be created automatically when running the `just set-env-var` just recipe later on, so there is no need to create it yourself.

## Cluster Setup

### Acquire the Service Account Manager Key

You must acquire a key of the `Service Account Manager` service account from the owner of the Google Cloud Project where the cluster will be set up.

The Service Account Manager service account is needed to:

- create all other services accounts needed to create and manage the cluster
- enable the required Google Cloud APIs to create and manage the cluster

The project owner must run:
```sh
just create-sa-manager <project_id>
```

This will create a service account named `sa-manager` and create a JSON key file for the service account in their `./credentials` directory.

Have the project owner send the key file to you. Save it as `./credentials/sa-manager.json` directory.

Then, you can set the `GOOGLE_APPLICATION_CREDENTIALS` environmental variable by running 
```
just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/sa-manager.json"
```

Google Application Default Credentials (ADC) is used by the Go Google Cloud client libraryto authenticate requests to your Google Cloud project. For service account keys, the use of environmental variables is the [prescribed method](https://cloud.google.com/docs/authentication/set-up-adc-local-dev-environment#local-key) of setting up ADC.

From here, you will be able to continue with the cluster setup on your own.

### Set up Cluster with a Single Command

This command can be used to initialize the required Google Cloud services and set up the `kops` cluster.

Running this command will complete all of the steps below
```sh
just init-kops-cluster <project_id> <bucket_name>:
```

### 1. Enable Required APIs

To create the kubernetes cluster and run all the `dictyBase` applications, certain Google Cloud APIs need to be enabled first. 

running the following command will enable the APIs from the list defined in `enabled_apis.txt`

```sh
just gcp-api enable-apis <project_id> gcs-files/apis/enabled_apis.txt
```

While not required for running the cluster, it is helpful to disable unneeded Google Cloud APIs to prevent unwanted charges to the GCP account.

running the following command will disable unnecessary Google Cloud APIs using the predefined list:

```sh
just gcp-api disable-apis <project_id> gcs-files/apis/disable_enabled_apis.txt
```

### 2. Create the `kops cluster creator` Service Account

The `kops cluster creator` service account is a dedicated service account with the necessary permissions to create and manage the Kubernetes cluster using the `kops` tool.

Create this service account with:

```sh
just gcp-sa create-sa <project_id> kops-cluster-creator gcs-files/roles-permissions/kops-cluster-creator-roles.txt credentials/kops-cluster-creator.json
```

This will create a service account named `kops-cluster-creator` with the roles defined in `gcs-files/roles-permissions/kops-cluster-creator-roles.txt`, and save the JSON key file to `credentials/kops-cluster-creator.json`.

### 3. Change Application Default Credentials
After creating the `kops-cluster-creator` key, you will need to update the value of the `GOOGLE_APPLICATION_CREDENTIAL` environmental variable to point to the key.


Run:

```
just set-env-var GOOGLE_APPLICATION_CREDENTIALS "${PWD}/credentials/kops-cluster-creator.json"
```

Now, the Go Google Cloud client libraries will use the `kops-cluster-creator` service key, 

### 4. Set Up kops State Store and Initialize the Cluster

The `create-kops-cluster` recipe sets up the state store bucket and initializes the Kubernetes cluster:

```sh
just create-kops-cluster <project_id> <bucket_name>
```
