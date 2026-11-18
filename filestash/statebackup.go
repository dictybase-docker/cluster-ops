package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	rbacv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/rbac/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	backupName   = "filestash-state-backup"
	workPath     = "/work"
	workVolume   = "work"
	hostnameKey  = "kubernetes.io/hostname"
	jobLabelKey  = "app.kubernetes.io/component"
	jobLabel     = "state-backup"
	scaleTimeout = 180
)

// scaleDownScript stops Filestash so that config.json and every SQLite file
// are copied at one point in time. It never exits non-zero: the scale-up
// container must always run.
const scaleDownScript = `ns=%[1]s
echo fail > %[4]s/scale.status
kubectl -n "$ns" scale deployment/filestash --replicas=0 || exit 0
i=0
while [ "$i" -lt %[3]d ]; do
  if [ -z "$(kubectl -n "$ns" get pods -l %[2]s=filestash -o name)" ]; then
    echo ok > %[4]s/scale.status
    exit 0
  fi
  i=$((i + 2)); sleep 2
done
echo "filestash pod still running after %[3]ds"
exit 0`

// copyScript writes a complete copy into backups/.partial-<ts>, checks it,
// and renames it to backups/<ts> only after every check passes. Prune keeps
// the newest completed copies (with manifest.json). Partial folders go away
// before each run and after a failed run, so a full PVC cannot fill up
// with them. Filestash is stopped, so no other writer uses backups/. It never exits non-zero (see scaleDownScript).
const copyScript = `W=%[1]s; S=%[2]s; B=$S/backups; KEEP=%[3]d
[ "$(cat $W/scale.status 2>/dev/null)" = ok ] || { echo skipped > $W/copy.status; exit 0; }
run() {
  set -e
  mkdir -p "$B"
  find "$B" -maxdepth 1 -name '.partial-*' -exec rm -rf {} +
  TS=$(date -u +%%Y%%m%%dT%%H%%M%%SZ); P="$B/.partial-$TS"
  mkdir -p "$P/db" "$P/config"
  for f in "$S"/db/*.sql; do
    [ -e "$f" ] || continue
    n=$(basename "$f")
    sqlite3 "$f" ".backup '$P/db/$n'"
    r=$(sqlite3 "$P/db/$n" "PRAGMA integrity_check")
    [ "$r" = ok ] || { echo "integrity_check $n: $r"; return 1; }
    for t in $(sqlite3 "$P/db/$n" "SELECT name FROM sqlite_master WHERE type='table'"); do
      echo "$n $t $(sqlite3 "$P/db/$n" "SELECT count(*) FROM \"$t\"")" >> "$P/counts.txt"
    done
  done
  cp "$S/config/config.json" "$P/config/config.json"
  (cd "$P" && find db config -type f | sort | xargs sha256sum > SHA256SUMS)
  printf '{"timestamp":"%%s","files":%%s}\n' "$TS" "$(wc -l < "$P/SHA256SUMS")" > "$P/manifest.json"
  mv "$P" "$B/$TS"
  for d in $(ls -1d "$B"/2* 2>/dev/null | sort -r | tail -n +$((KEEP + 1))); do
    [ -f "$d/manifest.json" ] && rm -rf "$d"
  done
  echo "completed copy $B/$TS"
}
(run)
rc=$?
if [ "$rc" -eq 0 ]; then
  echo ok > $W/copy.status
else
  rm -rf "$B"/.partial-*
  echo fail > $W/copy.status
fi
exit 0`

// scaleUpScript always brings Filestash back (five tries, rising delay, for a
// passing API error), then fails the Job when an earlier step failed.
const scaleUpScript = `ns=%[1]s
for i in 1 2 3 4 5; do
  kubectl -n "$ns" scale deployment/filestash --replicas=1 && break
  echo "scale up attempt $i failed"; sleep $((i * 5))
done
s=$(cat %[2]s/scale.status 2>/dev/null); c=$(cat %[2]s/copy.status 2>/dev/null)
echo "scale-down=$s copy=$c"
[ "$s" = ok ] && [ "$c" = ok ]`

// CreateStateBackup creates the CronJob and the RBAC that lets it scale the
// Filestash Deployment.
func (f *Filestash) CreateStateBackup(ctx *pulumi.Context, deps ...pulumi.Resource) error {
	cfg := f.Config
	meta := &metav1.ObjectMetaArgs{
		Name:      pulumi.String(backupName),
		Namespace: pulumi.String(cfg.Namespace),
		Labels:    f.labels(),
	}
	sa, err := corev1.NewServiceAccount(ctx, backupName+"-sa", &corev1.ServiceAccountArgs{Metadata: meta})
	if err != nil {
		return fmt.Errorf("error creating filestash backup service account: %w", err)
	}
	role, err := rbacv1.NewRole(ctx, backupName+"-role", &rbacv1.RoleArgs{
		Metadata: meta,
		Rules: rbacv1.PolicyRuleArray{
			&rbacv1.PolicyRuleArgs{
				ApiGroups: pulumi.StringArray{pulumi.String("apps")},
				Resources: pulumi.StringArray{pulumi.String("deployments"), pulumi.String("deployments/scale")},
				Verbs:     pulumi.ToStringArray([]string{"get", "patch", "update"}),
			},
			&rbacv1.PolicyRuleArgs{
				ApiGroups: pulumi.StringArray{pulumi.String("")},
				Resources: pulumi.StringArray{pulumi.String("pods")},
				Verbs:     pulumi.ToStringArray([]string{"get", "list", "watch"}),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("error creating filestash backup role: %w", err)
	}
	binding, err := rbacv1.NewRoleBinding(ctx, backupName+"-binding", &rbacv1.RoleBindingArgs{
		Metadata: meta,
		RoleRef: &rbacv1.RoleRefArgs{
			ApiGroup: pulumi.String("rbac.authorization.k8s.io"),
			Kind:     pulumi.String("Role"),
			Name:     pulumi.String(backupName),
		},
		Subjects: rbacv1.SubjectArray{
			&rbacv1.SubjectArgs{
				Kind:      pulumi.String("ServiceAccount"),
				Name:      pulumi.String(backupName),
				Namespace: pulumi.String(cfg.Namespace),
			},
		},
	}, pulumi.DependsOn([]pulumi.Resource{sa, role}))
	if err != nil {
		return fmt.Errorf("error creating filestash backup role binding: %w", err)
	}
	_, err = batchv1.NewCronJob(ctx, backupName, &batchv1.CronJobArgs{
		Metadata: meta,
		Spec: &batchv1.CronJobSpecArgs{
			Schedule:                   pulumi.String(cfg.Backup.Schedule),
			ConcurrencyPolicy:          pulumi.String("Forbid"),
			SuccessfulJobsHistoryLimit: pulumi.Int(3),
			FailedJobsHistoryLimit:     pulumi.Int(3),
			JobTemplate: &batchv1.JobTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{
					Labels: pulumi.StringMap{jobLabelKey: pulumi.String(jobLabel)},
				},
				Spec: &batchv1.JobSpecArgs{
					BackoffLimit: pulumi.Int(0),
					Template: &corev1.PodTemplateSpecArgs{
						Metadata: &metav1.ObjectMetaArgs{
							Labels: pulumi.StringMap{jobLabelKey: pulumi.String(jobLabel)},
						},
						Spec: f.backupPodSpec(),
					},
				},
			},
		},
	}, pulumi.DependsOn(append(deps, binding)))
	if err != nil {
		return fmt.Errorf("error creating filestash state backup cronjob: %w", err)
	}
	return nil
}

// backupPodSpec runs on the node of the Filestash pod: the state PVC is
// ReadWriteOnce, so the volume can attach only there.
func (f *Filestash) backupPodSpec() *corev1.PodSpecArgs {
	cfg := f.Config
	return &corev1.PodSpecArgs{
		ServiceAccountName: pulumi.String(backupName),
		RestartPolicy:      pulumi.String("Never"),
		SecurityContext: &corev1.PodSecurityContextArgs{
			RunAsNonRoot: pulumi.Bool(true),
			RunAsUser:    pulumi.Int(filestashUID),
			RunAsGroup:   pulumi.Int(filestashUID),
			FsGroup:      pulumi.Int(filestashUID),
		},
		Affinity: &corev1.AffinityArgs{
			PodAffinity: &corev1.PodAffinityArgs{
				RequiredDuringSchedulingIgnoredDuringExecution: corev1.PodAffinityTermArray{
					&corev1.PodAffinityTermArgs{
						TopologyKey:   pulumi.String(hostnameKey),
						LabelSelector: &metav1.LabelSelectorArgs{MatchLabels: f.labels()},
					},
				},
			},
		},
		InitContainers: corev1.ContainerArray{
			f.shellContainer("scale-down", cfg.Backup.KubectlImage,
				fmt.Sprintf(scaleDownScript, cfg.Namespace, nameLabel, scaleTimeout, workPath)),
			f.shellContainer("copy", cfg.Backup.SqliteImage,
				fmt.Sprintf(copyScript, workPath, statePath, cfg.Backup.Keep)),
		},
		Containers: corev1.ContainerArray{
			f.shellContainer("scale-up", cfg.Backup.KubectlImage,
				fmt.Sprintf(scaleUpScript, cfg.Namespace, workPath)),
		},
		Volumes: corev1.VolumeArray{
			&corev1.VolumeArgs{
				Name: pulumi.String(stateVolume),
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSourceArgs{
					ClaimName: pulumi.String(f.resourceName("state")),
				},
			},
			&corev1.VolumeArgs{Name: pulumi.String(workVolume), EmptyDir: &corev1.EmptyDirVolumeSourceArgs{}},
		},
	}
}

func (f *Filestash) shellContainer(name, image, script string) *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name:    pulumi.String(name),
		Image:   pulumi.String(image),
		Command: pulumi.StringArray{pulumi.String("sh"), pulumi.String("-c"), pulumi.String(script)},
		VolumeMounts: corev1.VolumeMountArray{
			f.mount(stateVolume, statePath),
			f.mount(workVolume, workPath),
		},
	}
}
