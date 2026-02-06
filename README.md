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

### Remaining binaries

Since `just` is now installed, we can run a single just recipe to install the remaining binaries. This will install specific versions of the tools as defined in the `Justfile`:

```
just install-asdf-plugins
```

This command will install the following binaries:

- [kubectl](https://kubernetes.io/docs/reference/kubectl/kubectl/)
- [kops](https://kops.sigs.k8s.io/)
- [gcloud](https://cloud.google.com/sdk/gcloud)
- [pulumi](https://www.pulumi.com/docs/)
- [velero](https://velero.io/docs/)
- [mc (MinIO Client)](https://min.io/docs/minio/linux/reference/minio-mc.html)

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

A single command can be used to initialize the required Google Cloud services and set up the `kops` cluster.

Running the following:
```sh
just init-kops-cluster <project_id> <bucket_name>
```
will execute the steps listed below. There is no need to run them individually. They have been preserved here for documentation.

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
export GOOGLE_APPLICATION_CREDENTIALS="${PWD}/credentials/kops-cluster-creator.json"
```

Now, the Go Google Cloud client libraries will use the `kops-cluster-creator` service key, 

### 4. Set Up kops State Store and Initialize the Cluster

The `create-kops-cluster` recipe sets up the state store bucket and initializes the Kubernetes cluster:

```sh
just create-kops-cluster <project_id> <bucket_name>
```
## Application Deployment with Pulumi

Applications are deployed to the Kubernetes cluster using [Pulumi](https://www.pulumi.com/), an infrastructure as code tool. Pulumi allows us to define, deploy, and manage cloud resources using familiar programming languages like Go.

### 1. Create Pulumi Manager Service Account Key
```sh
just gcp-sa create-sa <project_id> pulumi-manager gcs-files/roles-permissions/pulumi-manager-roles.txt credentials/pulumi-manager.json
```

### 2. Set the PULUMI_GCP_CREDENTIALS environmental variable
```sh
export PULUMI_GCP_CREDENTIALS="${PWD}/credentials/pulumi-manager.json"
```

### 3. Create Key Ring and Key
Creates a Google [Cloud Key](https://cloud.google.com/kms/docs/resource-hierarchy#keys) used to encrypt secrets in a Pulumi project's stack
```sh
just gcp-kms create-keyring-and-key <project-id> <keyring-name> <key-name> credentials/pulumi-manager.json <location>
```
Then,
```
export PULUMI_SECRET_PROVIDER=<GCLOUD_KMS_KEY>
```

Arguments:
- `location`: Optional. The Google Cloud region where the bucket will be created. Defaults to "us-central1"

### 4. Initialize Pulumi State Store
The following command sets up a Google Cloud Storage bucket to store Pulumi state:

```sh
just gcp-pulumi pulumi-gcs-setup credentials/pulumi-manager.json <pulumi_bucket_name> <lifecycle_config> <location>
```

Arguments:
- `pulumi_bucket_name`: Name of the gcs bucket to create for storing pulumi state
- `lifecycle_config`: Optional. Path to a lifecycle configuration file for the bucket (controls object retention/deletion policies)
- `location`: Optional. The Google Cloud region where the bucket will be created. Defaults to "us-central1"

### 5. Initialize Project Stack
Initialize stack:
```
just gcp-pulumi new-stack-from <folder> <stack> <from-stack>
```
Example: 
```
just gcp-pulumi new-stack from graphql_server production staging
```
This would initialize a new stack called `production` in the `graphql_server` project. It will copy the configuration from the `staging` stack, if it exists.

### 6. Create Pulumi Resources
Create the project resources for the desired stack
```
just gcp-pulumi create-resource <folder> <stack>
```

## Simplified Pulumi Setup and Deployment

For convenience, we provide two just recipes that simplify the Pulumi setup and deployment process:

### Initialize Pulumi Environment

The `initialize-pulumi` recipe combines steps 1-4 of the `Application Deployment with Pulumi` section into a single command:

```sh
just initialize-pulumi <project_id> <keyring_name> <key_name> <bucket_name> [location]
```

Arguments:
- `project_id`: Your GCP project ID
- `keyring_name`: Name for the KMS keyring to create
- `key_name`: Name for the KMS key to create
- `bucket_name`: Name for the GCS bucket to store Pulumi state
- `location`: (Optional) Google Cloud region. Defaults to "us-central1"

This command will:
1. Create the Pulumi Manager service account with necessary permissions
2. Set the PULUMI_GCP_CREDENTIALS environment variable
3. Create a KMS keyring and key for Pulumi secrets encryption
4. Initialize the Pulumi state store in GCS

### Initialize and Deploy Initial Resources

The `pulumi-init-and-deploy` recipe combines the Pulumi environment setup with deploying the initial resources:

```sh
just pulumi-init-and-deploy <stack> <from-stack> <project_id> <keyring_name> <key_name> <bucket_name> [location]
```

Arguments:
- `stack`: Name of the stack to create
- `from-stack`: Name of the existing stack to copy configuration from
- `project_id`: Your GCP project ID
- `keyring_name`: Name for the KMS keyring to create
- `key_name`: Name for the KMS key to create
- `bucket_name`: Name for the GCS bucket to store Pulumi state
- `location`: (Optional) Google Cloud region. Defaults to "us-central1"

This command will:
1. Set up the complete Pulumi environment (steps 1-4)
2. Set the PULUMI_SECRET_PROVIDER environment variable
3. Create resources for all projects listed in the specified resources files

The initial resources are defined in the `initial-resources.txt` file and include:
- ArangoDB single instance
- ArangoDB database creation
- MinIO object storage
- CloudNative PostgreSQL operator
- Backup secrets
- Event messenger

The database and storage management resources are defined in the `database-and-storage-resources.txt` file and include:
- ArangoDB Backup
- ArangoDB Data Loader
- ArangoDB Operation
- CloudNative PostgreSQL cluster
- Velero Installation
- Redis Standalone
- Storage Class
