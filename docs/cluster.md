# Starting the Cluster with Kops

1. Configure Default Credentials with gcloud

```
export GOOGLE_DEFAULT_APPLICATION=CLOUD_MANAGER_SERVICE_KEY_LOCATION
```

2. Start the Cluster
```
just gcp-cluster create-kops-cluster PROJECT BUCKET_NAME
```
