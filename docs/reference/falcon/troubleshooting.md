# Falcon Sensor Troubleshooting

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## Error Table

| Error | Cause | Fix |
|-------|-------|-----|
| `FALCON_CLIENT_ID is not set` | The API client is not in the environment | Export it. Refer to [API client details](prerequisites.md#falcon-api-client). |
| `properties.cloud is not set` | The stack has no Falcon cloud region | Set it. Refer to [region details](prerequisites.md#falcon-cloud-region). |
| `Falcon OAuth request ... failed` | The client ID, the secret, or the region is wrong | Make sure that `properties.cloud` agrees with the Falcon console address |
| `could not read the tenant CID` | The API client has no Sensor Download scope | Add **Sensor Download: Read** to the API client |
| `the Falcon API returned no registry token` | The API client has no Falcon Images Download scope | Add **Falcon Images Download: Read** to the API client |
| `is not the CID of the tenant` | `properties.cid` comes from a different tenant | Set the correct CID with `set-secret` |
| `Mutable tags such as latest are rejected` / `older than sensor 7.40` | The tag is not a supported version | Use a 7.40 or later tag from `just falcon list-tags` |
| `tag '...' is not in` | The tag does not exist | Run `just falcon list-tags` |
| `not sa-manager@` | `GOOGLE_APPLICATION_CREDENTIALS` is a different key | Enter the cluster-env sub-shell again |
| `no cloud-platform or devstorage access scope` | A node cannot pull from Artifact Registry | Change the instance group scopes, then roll the nodes |
| `Enable artifactregistry.googleapis.com` | The Artifact Registry API is off | Turn on the API with `just gcp-api enable-apis` |
| `does not match source digest` | The copy did not complete | Run `mirror-image` again |
| `falcon-sensor:properties is invalid in ...` | Preview found a missing or bad value | Set the named field |
| `not ready on every node` | A pod is `Pending`, cannot pull, or fails | Run `just falcon verify`, then refer to the sections below |

## Image Pull Fails

`deploy` shows `a sensor pod cannot pull its image`. The pod has the status `ErrImagePull` or `ImagePullBackOff`.

1. Make sure that the node service account has `roles/artifactregistry.reader` on the repository. Run `just falcon mirror-image` again with the same tag. It adds a binding that is missing.
2. Make sure that the node has a `cloud-platform` or `devstorage` access scope.
3. Make sure that the kubelet on the node has the GCP image credential provider. A node without it cannot pull from `*.pkg.dev`. Read the pod events in the `deploy` output for the registry error.

## Pod Is Pending

The node has no free CPU or memory for one more pod. Refer to [node resources](install.md#node-resources).

## No Agent ID

`verify` shows `has no agent ID`. The sensor started but did not register.

1. Make sure that the node can connect to the CrowdStrike cloud on TCP port 443.
2. Make sure that `properties.cloud` is the region of the tenant.
3. Make sure that the CID is correct and, if the tenant requires it, that the provisioning token is set.
4. Read the sensor log of the node:

```bash
just falcon logs --node <node-name>
```
