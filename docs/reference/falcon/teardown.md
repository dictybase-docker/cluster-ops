# Falcon Sensor Teardown Details

Back to: [Falcon Deploy Guide](../../falcon-deploy.md)

## What It Does

**Destructive. All nodes lose Falcon protection.** `teardown` destroys the `falcon-sensor` stack. Pulumi removes the Helm release, Secret `falcon-sensor-cid`, and namespace `falcon-system`.

## Command

```bash
just falcon teardown
```

## Behavior

1. Run `remove-resource` on the `falcon-sensor` stack.
2. The chart cleanup hook runs a cleanup DaemonSet. It removes `/opt/CrowdStrike` from each node.

## What Stays

- The Artifact Registry repository, the mirrored image, and the reader IAM bindings.
- The stack file and its encrypted CID.
- The host entries in the Falcon console. Remove them in the console when you do not need them.

## Flags

| Flag | Required | Default | Notes |
|------|----------|---------|-------|
| `--stack` | No | `$PULUMI_STACK` | No dev fallback |
