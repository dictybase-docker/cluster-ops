# Redis Troubleshooting

Back to: [Redis Deploy Guide](../../redis-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| Pod Pending | No node matches `pool=database` + taint toleration, or pool missing | `just redis check-pool`; see [pool requirements](pool-requirements.md) |
| Pod CrashLoopBackOff with permission errors on `/data` | Volume ownership wrong for the image | The pod sets `fsGroup 999` and the init container `chown -R 999:999 /data` — confirm both are in the applied Deployment |
| Client `NOAUTH Authentication required` | Server still runs with `--requirepass` from a previous deploy | Re-run `just redis deploy` — the unauthenticated spec replaces it and rolls the pod |
| `verify` fails on PING | Pod up but unresponsive | `kubectl logs -n prod -l app=redis --tail=50`; check PVC is Bound (AOF replay can delay readiness) |
| Data gone after pod restart | PVC not Bound before the pod started, or wrong StorageClass | `kubectl get pvc redis-data -n prod` must be Bound on `dictycr-balanced`; AOF only persists what was written to the volume |
| Stack-module commands missing (`FT.*`, `JSON.*`) | Client expects `redis-stack-server` | Redis 8 merged all Stack modules into core — upgrade the client, or pin the lab image if the workload truly needs the EOL line |

| `deploy-backup` Job fails with `storage.objects.create` denied | Wrong SA key (ArangoDB `backup-gcs-sa` or postgres `postgres-backup-sa`), or IAM condition pinned to a different bucket | Use the `redis-backup-sa` key; re-run `just redis configure-backup-secrets` if the bucket name changed — the condition is pinned to the exact bucket |
| `configure-backup-secrets` fails `service account key ... does not exist` | `--gcs-key-file` points at a path not minted on this machine | Run `just redis setup-backup-sa` first, or pass the existing key with `--gcs-key-file` |
| `deploy-backup` Job fails with restic `wrong password` | `resticPass` changed or the bucket was re-created with a different password | Keep one restic password per bucket; recover the old value from the stack config history |
| `deploy-backup` reports `job ... did not finish` | Bucket in the wrong project, SA missing, or redis service down | Check the tail logs in the recipe output; verify `kubectl get svc redis -n prod` and the IAM condition |

## Reading Logs

```bash
kubectl logs -n prod -l app=redis --tail=100
kubectl describe pod -n prod -l app=redis
```
