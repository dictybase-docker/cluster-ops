# Falcon Sensor Configuration

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

The stack value `falcon-sensor:properties` in `falcon-sensor/Pulumi.<stack>.yaml` holds all the settings. The Pulumi program rejects a missing value, a value with `<...>` placeholders, or a value that is not valid. The error names the field. It never shows the CID or the token.

## Keys

| Key | Set by | Secret | Notes |
|-----|--------|--------|-------|
| `namespace` | stack file | No | `falcon-system` |
| `clusterName` | stack file | No | The cluster name in the Falcon console. kOps is a self-managed cluster, so the chart needs this value. |
| `cloud` | operator, `set-config` | No | Falcon cloud region. No default. Refer to [region details](prerequisites.md#falcon-cloud-region). |
| `cid` | operator, `set-secret` | Yes | Customer ID with checksum |
| `provisioningToken` | operator, `set-secret` | Yes | Optional |
| `tags` | operator, `set-config` | No | Optional. Comma-separated Falcon sensor grouping tags. |
| `chart.name` / `chart.repository` / `chart.version` | stack file | No | `falcon-sensor`, `https://crowdstrike.github.io/falcon-helm`, `1.37.0` |
| `registry.location` / `registry.repository` | stack file | No | `us-central1`, `falcon` |
| `image.repository` / `image.tag` / `image.digest` | `mirror-image` | No | Do not edit by hand |

## Customer ID

The Customer ID (CID) identifies the Falcon tenant. It has 32 hexadecimal characters, a hyphen, and a 2-character checksum. Find it in the Falcon console under **Host setup and management > Sensor downloads**.

```bash
just gcp-pulumi set-secret --folder falcon-sensor --key properties.cid
```

- The recipe asks for the value at a hidden prompt.
- Pulumi writes Secret `falcon-sensor-cid` with the key `FALCONCTL_OPT_CID`. The chart reads it through `falconSecret.secretName`.
- `mirror-image` reads the CID of the API client tenant. It stops when the stack CID is different.

## Provisioning Token

**Optional. The default is off.** Some tenants require a provisioning token before a new host can register.

```bash
just gcp-pulumi set-secret --folder falcon-sensor --key properties.provisioningToken
```

When the value is set, Secret `falcon-sensor-cid` also gets the key `FALCONCTL_OPT_PROVISIONING_TOKEN`.

## Sensor Grouping Tags

**Optional. The default is off.** Tags put the hosts in Falcon host groups.

```bash
just gcp-pulumi set-config --folder falcon-sensor --key properties.tags --value dcr-kube1,kubernetes
```
