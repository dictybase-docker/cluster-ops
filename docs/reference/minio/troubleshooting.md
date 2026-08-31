# MinIO Troubleshooting

Back to: [MinIO Deploy Guide](../../minio-deploy.md)

| Symptom | Cause | Fix |
|---------|-------|-----|
| `Error: no stack name` | Recipe run outside the cluster-env sub-shell | Enter `just cluster-env --env prod --cluster <prod-cluster>` first, or pass `--stack <name>` |
| Pod Pending | No node matches `pool=database` + taint toleration, or pool missing | `just minio check-pool`; see [pool requirements](pool-requirements.md) |
| `ImagePullBackOff` | Chart 17 image moved to `bitnamilegacy/minio` | `minio/Pulumi.prod.yaml` already pins the legacy repo — lab configs missing `image.repository` default to it; check for typos in a hand-set override |
| Helm values rejected on apply | Lab-era values leaked in (`disableWebUI`) | Chart 17 removed `disableWebUI`; this program no longer emits it — confirm the stack is on the chart 17 program and `Pulumi.prod.yaml` |
| App cannot connect | Wrong Service or Secret | API is `minio.prod.svc.cluster.local:9000`; credentials in Secret `minio-root` (`kubectl get secret minio-root -n prod -o jsonpath='{.data.rootPassword}' \| base64 -d`) |
| Import stalls at start | Port-forward to `svc/minio` not up (port 19000 busy) | Free the local port (`lsof -i :19000`) and re-run `import-bucket` |
| Import fails auth on target | Secret keys not `rootUser`/`rootPassword` | The recipe reads those exact keys from `minio-root` — check the Secret with `kubectl get secret minio-root -n prod -o yaml` |
| Import fails auth on source | Source credentials rejected | Verify with `mc alias set test <url> <user> <pass> && mc ls test` outside the recipe |
| Console/UI missing | Intentional | Prod sets `webui: false`; use `mc` — see [install](install.md#chart-17-changes-vs-the-lab-stacks) |

## Reading Logs

```bash
kubectl logs -n prod -l app.kubernetes.io/name=minio --tail=100
kubectl describe pod -n prod -l app.kubernetes.io/name=minio
```
