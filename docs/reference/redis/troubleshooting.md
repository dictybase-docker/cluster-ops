# Redis Troubleshooting

Back to: [Redis Deploy Guide](../../redis-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| Pod Pending | No node matches `pool=database` + taint toleration, or pool missing | `just redis check-pool`; see [pool requirements](pool-requirements.md) |
| Pod CrashLoopBackOff with permission errors on `/data` | Volume ownership wrong for the image | The pod sets `fsGroup 999` and the init container `chown -R 999:999 /data` — confirm both are in the applied Deployment |
| Client `NOAUTH Authentication required` | Client connected without the password | Password is in Secret `redis-auth` (`kubectl get secret redis-auth -n prod -o jsonpath='{.data.password}' \| base64 -d`) |
| Client `WRONGPASS` | Password changed out of band | Re-run `just redis deploy --password '<password>'` — it re-applies the Secret and rolls the pod |
| `verify` fails on authenticated PING | Pod up but Secret out of sync with the container env | Same fix as WRONGPASS — redeploy with the intended password |
| Data gone after pod restart | PVC not Bound before the pod started, or wrong StorageClass | `kubectl get pvc redis-data -n prod` must be Bound on `dictycr-balanced`; AOF only persists what was written to the volume |
| Stack-module commands missing (`FT.*`, `JSON.*`) | Client expects `redis-stack-server` | Redis 8 merged all Stack modules into core — upgrade the client, or pin the lab image if the workload truly needs the EOL line |

## Reading Logs

```bash
kubectl logs -n prod -l app=redis --tail=100
kubectl describe pod -n prod -l app=redis
```
