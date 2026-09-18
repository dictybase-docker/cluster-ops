# NATS Troubleshooting

Back to: [NATS Deploy Guide](../../nats-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| `deploy` fails `namespace 'prod' does not exist` | `namespace-bootstrap` stack never applied on this cluster | Run `just gcp-pulumi apply-namespaces` ([pulumi setup §5](../../pulumi-setup.md#5-first-apply--storageclass-and-namespaces)) |
| `deploy` fails `--namespace does not match properties.namespace` | Readiness target drifted from the stack config | Re-run with the namespace from `properties.namespace`, or fix the stack config |
| Pod Pending | No schedulable node matches the pod spec (with `placement.pool` set: no node carries `pool=<pool>` + the taint) | `kubectl describe pod -n prod -l app.kubernetes.io/name=nats`; see [instance group requirements](pool-requirements.md) |
| `verify` fails `nats-box deployment missing` | Chart deployed with `natsBox.enabled: false` | The handshake runs through `nats-box`; re-enable it in the stack config or run `nats rtt` from any pod with the `nats` CLI |
| Client `-ERR 'authorization violation'` | Server was deployed with an authorization block out of band | The prod stack runs unauthenticated — remove the `authorization` block from the deployed ConfigMap or redeploy with `just nats deploy` |
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
