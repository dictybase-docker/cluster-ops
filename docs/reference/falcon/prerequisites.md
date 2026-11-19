# Falcon Sensor Prerequisites

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## Falcon API Client

The recipes use a Falcon API client to read the tenant CID and to get a token for `registry.crowdstrike.com`. The registry does not accept a login with the raw API client. It accepts only a short-lived token that the Falcon API supplies.

Create the API client in the Falcon console under **Support and resources > API clients and keys**. Give it these scopes:

| Scope | Access | Use |
|-------|--------|-----|
| Sensor Download | Read | Read the CID of the tenant |
| Falcon Images Download | Read | Get the registry token for the sensor image |

Export the client ID and the secret in the cluster-env sub-shell:

```bash
read -rs FALCON_CLIENT_ID && export FALCON_CLIENT_ID
read -rs FALCON_CLIENT_SECRET && export FALCON_CLIENT_SECRET
```

- The recipes never accept the client ID or the secret as flags. Flags show in `ps` output and in the shell history.
- The recipes send the secret to `curl` through a private temporary file. They remove the file when they stop.
- The API client never goes into the cluster. Only `list-tags` and `mirror-image` use it.

## Falcon Cloud Region

A Falcon tenant lives in one CrowdStrike data center. The sensor and the API calls must use that data center. This value is not related to the GCP region `us-central1`.

The Falcon console address shows the region:

| Console address | `properties.cloud` | API host |
|-----------------|--------------------|----------|
| `falcon.crowdstrike.com` | `us-1` | `api.crowdstrike.com` |
| `falcon.us-2.crowdstrike.com` | `us-2` | `api.us-2.crowdstrike.com` |
| `falcon.us-3.crowdstrike.com` | `us-3` | `api.us-3.crowdstrike.com` |
| `falcon.eu-1.crowdstrike.com` | `eu-1` | `api.eu-1.crowdstrike.com` |
| `falcon.laggar.gcw.crowdstrike.com` | `us-gov-1` | `api.laggar.gcw.crowdstrike.com` |
| `falcon.us-gov-2.crowdstrike.mil` | `us-gov-2` | `api.us-gov-2.crowdstrike.mil` |

The stack file has no default value for `properties.cloud`. The recipes and the Pulumi program stop with an error when the value is missing.

## Artifact Registry Mirror

The nodes do not pull the sensor image from `registry.crowdstrike.com`. `mirror-image` copies the image into an Artifact Registry repository in the cluster project. The nodes then pull the copy with their own GCE service accounts.

This design has these results:

- No CrowdStrike registry token goes into the cluster. The token has a limited life, and a cluster copy must be replaced each time it expires.
- The DaemonSet pulls one digest only. A new sensor version starts only when you run `mirror-image` again.
- The sensor still connects to the CrowdStrike cloud. The mirror changes only the image source.

The mirror needs these items:

- The `artifactregistry.googleapis.com` API is on in the project.
- The sa-manager service account can create a repository and can change its IAM policy.
- Each node has a GCE service account with a `cloud-platform` or `devstorage` access scope. `mirror-image` stops when a node has no such scope.
- The kubelet on each node must have the GCP image credential provider for `*.pkg.dev`. This procedure does not examine the kubelet. The first `deploy` shows the result. If the pull fails, refer to [image pull fails](troubleshooting.md#image-pull-fails).

## Tools

The recipes need `curl`, `jq`, `yq`, `skopeo`, `gcloud`, `kubectl`, and `pulumi`. `skopeo` copies the image and keeps its digest. Each recipe stops with `required tool '<name>' is not installed` when a tool is missing.

## Sensor Version and Nodes

- Chart 1.37.0 supports sensor version **7.40 and later** only. `mirror-image` rejects older tags and mutable tags such as `latest`.
- The nodes use Ubuntu 24.04. Before an upgrade of the node image, make sure that the CrowdStrike support matrix includes the new kernel.
- The chart accepts resource requests of at least 250m CPU and 500Mi memory. This value is a configuration minimum. It is not a measured consumption. The stack sets no requests or limits.
- In the Falcon console, turn off **Uninstall and maintenance protection** for these hosts. Sensor 7.34 and later ignores it for DaemonSet hosts.
