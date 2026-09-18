# NATS Troubleshooting

Back to: [NATS Deploy Guide](../../nats-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| `deploy` fails `namespace 'prod' does not exist` | `namespace-bootstrap` stack never applied on this cluster | Run `just gcp-pulumi apply-namespaces` ([pulumi setup §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces)) |
| `deploy` fails `--namespace does not match properties.namespace` | Readiness target drifted from the stack config | Re-run with the namespace from `properties.namespace`, or fix the stack config |
| Pod Pending | No schedulable node matches the pod spec (with `placement.pool` set: no node carries `pool=<pool>` + the taint) | `kubectl describe pod -n prod -l app.kubernetes.io/name=nats`; see [instance group requirements](pool-requirements.md) |
| Client `-ERR 'authorization violation'` | Client connected without the token | Token is in Secret `nats-auth` (`kubectl get secret nats-auth -n prod -o jsonpath='{.data.token}' \| base64 -d`); connect with `nats://<token>@nats...` |
| Auth worked before, token changed out of band | Secret updated without a pod roll | Re-run `just nats deploy --token '<token>'` — it restarts the StatefulSet so the server resolves the new Secret value |
| `verify` fails on authenticated rtt | Pod up but Secret/env out of sync | Same fix as the token-change row — redeploy with the intended token |
| `verify` fails `nats-box deployment missing` | Chart deployed with `natsBox.enabled: false` | The auth handshake runs through `nats-box`; re-enable it in the stack config or run the handshake from any pod with the `nats` CLI |
| `nats-box` default context rejected | The chart's default context carries no credentials | Pass the server URL with the token inline: `nats --server nats://nats...:4222 --token <token>` |
| Messages lost after pod restart | Expected — core pub/sub keeps no message persistence | Clients must republish after a restart; JetStream persistence is [reserved for a future revision](install.md#single-server-no-clustering) |

## Reading Logs

```bash
kubectl logs -n prod -l app.kubernetes.io/name=nats --tail=100
kubectl describe pod -n prod -l app.kubernetes.io/name=nats
```

The server also exposes `/healthz` on the monitor port (pod-local `:8222`) —
the chart's probes read it, and it is the quickest liveness signal:

```bash
kubectl exec -n prod nats-0 -- wget -qO- http://localhost:8222/healthz
```
