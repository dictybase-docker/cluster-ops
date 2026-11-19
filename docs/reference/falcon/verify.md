# Falcon Sensor Verify Details

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

`verify` does read-only checks on the sensor DaemonSet. It exits non-zero when one or more checks fail.

## Command

```bash
just falcon verify
```

## Checks

| Check | Pass condition |
|-------|----------------|
| Coverage | The DaemonSet schedules one pod on each node, and the namespace has one sensor pod for each node |
| Readiness | All scheduled pods are ready |
| Image | The pod image is `properties.image.repository@properties.image.digest` |
| Registration | `falconctl -g --aid` shows an agent ID (AID) in each pod |

An AID shows that the sensor registered with the CrowdStrike cloud. An AID does not show current connectivity or protection. After the checks pass, find the hosts in the Falcon console under **Host setup and management > Host management**.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--namespace` | No | `properties.namespace` | A different value stops the recipe |
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |

## Sensor Log

Read the sensor log of one node. The command makes no change.

```bash
just falcon logs --node <node-name>
```

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--node` | Yes | none | A node name from `verify` |
| `--tail` | No | `200` | Lines to print |
