# Creating a Service Account for a Project

```
just gcp-sa create-sa PROJECT SERVICE_ACCOUNT_NAME SERVICE_ACCOUNT_DESCRIPTION
```

# Granting Cloud Manager Role to Service Account

The Cloud Manager role should provide the necessary permissions to a service account to start up and administer a cluster
```
just gcp-sa assign-roles-to-sa PROJECT SERVICE_ACCOUNT_NAME gcs-files/roles-permissions/cloud-manager-roles.txt
```

# Generating a Service Key

Once the Service Account is created and the Cloud Manager Role is assigned, use the following command to generate a service key and output it to a desired file:
```
just gcp-sa create-sa-key PROJECT SERVICE_ACCOUNT_NAME OUTPUT_FILE
```
