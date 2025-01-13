# Service Account Reference
Below are the various service accounts defined in the project and how they're meant to be used.

## cloud-manager

## cluster-backup 

## database-backup

## deploy-manager

## kops-cluster-creator

# Creating a Service Account Manager Service Account for a Project
The Service Account Manager Service Account can create and manage other service accounts

Running the following command will:

1. Create the Service Account Manager Service Account
2. Generate the Service Account Key in the `credentials` folder
3. Authenticate gcloud with the Service Account
```
just gcp-sa create-sa-manager PROJECT_ID
```

# Creating the Service Accounts for the Cluster

Running the following command will:
1. Create the service accounts required to set up and run the cluster:
2. Activate the GCP APIs required for the cluster
```
just gcp-cluster sa-accounts-setup PROJECT:
```
