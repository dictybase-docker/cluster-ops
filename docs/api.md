# Enabling APIs

Run the follow command to enable the APIs necessary to run a cluster in GCP:
```
just gcp-api enable-apis PROJECT gcs-files/apis/enabled_apis.txt`
```

# Disabling APIs

When a project is created, some APIs are enabled by default that are not required for the operation and administration of thecluster. To disable these APIs, run:
```
just gcp-api disable-apis PROJECT gcs-files/apis/disabled_enabled_apis.txt`
```
