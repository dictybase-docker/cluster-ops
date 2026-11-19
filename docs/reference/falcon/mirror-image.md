# Falcon Sensor Image Mirror Details

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

`list-tags` shows the node sensor tags that the tenant can pull. `mirror-image` copies one tag from `registry.crowdstrike.com/falcon-sensor/release/falcon-sensor` into `<location>-docker.pkg.dev/<project>/<repository>/falcon-sensor`. It then writes the image path, the tag, and the digest into the stack file. Refer to [the mirror design](prerequisites.md#artifact-registry-mirror) for the reasons.

## Commands

```bash
just falcon list-tags
just falcon mirror-image --tag <sensor-tag>
```

## Behavior

`list-tags` makes no change. `mirror-image` does all the checks before it makes the first change.

**Checks:**

1. Make sure that `--tag` is a versioned tag of sensor 7.40 or later. The recipe rejects `latest`.
2. Make sure that `PROJECT_ID`, `KUBECONFIG`, the sa-manager key, `properties.cloud`, and the API client are set.
3. Read the GCE service account of each node. Stop when a node is in a different project, when the cluster has no nodes, or when a node has no storage access scope.
4. Get a Falcon OAuth token, the tenant CID, and a registry token.
5. Compare the tenant CID with `properties.cid`, when the stack has a CID.
6. Read the digest of the source tag. Stop when the tag does not exist.

**Changes:**

1. Initialize the stack when it does not exist.
2. Create the Artifact Registry repository when it does not exist.
3. Give `roles/artifactregistry.reader` on the repository to each node service account that does not have it.
4. Copy the image with `skopeo copy --all --preserve-digests`.
5. Compare the mirror digest with the source digest. Stop, with no change to the stack file, when they are different.
6. Write `properties.image.repository`, `properties.image.tag`, and `properties.image.digest`.

Commit the stack file after the recipe completes.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--tag` | Yes (`mirror-image`) | none | A tag from `list-tags`. Use version 7.40 or later for chart 1.37. |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Credentials

- The Falcon API client comes from `FALCON_CLIENT_ID` and `FALCON_CLIENT_SECRET`. Refer to [API client details](prerequisites.md#falcon-api-client).
- All `gcloud` calls use the sa-manager key in `GOOGLE_APPLICATION_CREDENTIALS`. The recipe stops when the key is not `sa-manager@<project>`.
- The registry logins go into a private `auth.json` file for `skopeo`. The recipe removes the file when it stops.

## Upgrade

To upgrade the sensor, run `list-tags`, then `mirror-image` with the new tag, then `just falcon deploy`. The DaemonSet replaces one pod at a time.
