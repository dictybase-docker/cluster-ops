package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	filestashUID        = 1000
	busyboxImage        = "busybox:1.37.0@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec"
	adminPasswordKey    = "ADMIN_PASSWORD"
	configSecretKey     = "CONFIG_SECRET"
	probeInitialDelay   = 10
	probePeriodSeconds  = 20
	probeTimeoutSeconds = 10
)

// CreateDeployment creates the one-replica Filestash Deployment.
// The strategy is Recreate because the state PVC is ReadWriteOnce.
func (f *Filestash) CreateDeployment(
	ctx *pulumi.Context,
	deps ...pulumi.Resource,
) (*appsv1.Deployment, error) {
	cfg := f.Config
	dep, err := appsv1.NewDeployment(ctx, cfg.Name, &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(cfg.Name),
			Namespace: pulumi.String(cfg.Namespace),
			Labels:    f.labels(),
		},
		Spec: &appsv1.DeploymentSpecArgs{
			Replicas: pulumi.Int(cfg.Replicas),
			Strategy: &appsv1.DeploymentStrategyArgs{Type: pulumi.String("Recreate")},
			Selector: &metav1.LabelSelectorArgs{MatchLabels: f.labels()},
			Template: &corev1.PodTemplateSpecArgs{
				Metadata: &metav1.ObjectMetaArgs{Labels: f.labels()},
				Spec:     f.podSpec(),
			},
		},
	}, pulumi.DependsOn(deps))
	if err != nil {
		return nil, fmt.Errorf("error creating filestash deployment: %w", err)
	}
	return dep, nil
}

func (f *Filestash) podSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		SecurityContext: &corev1.PodSecurityContextArgs{
			RunAsNonRoot: pulumi.Bool(true),
			RunAsUser:    pulumi.Int(filestashUID),
			RunAsGroup:   pulumi.Int(filestashUID),
			FsGroup:      pulumi.Int(filestashUID),
		},
		InitContainers: f.initContainers(),
		Containers:     corev1.ContainerArray{f.mainContainer()},
		Volumes:        f.volumes(),
	}
}

func (f *Filestash) initContainers() corev1.ContainerArray {
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name:    pulumi.String("plugins"),
			Image:   pulumi.String(f.Config.Plugins.Image.Ref()),
			Command: pulumi.StringArray{pulumi.String("sh"), pulumi.String("-c"), pulumi.String(pluginCopyScript())},
			VolumeMounts: corev1.VolumeMountArray{
				f.mount(stateVolume, statePath),
			},
		},
		&corev1.ContainerArgs{
			Name:    pulumi.String("seed"),
			Image:   pulumi.String(busyboxImage),
			Command: pulumi.StringArray{pulumi.String("sh"), pulumi.String("-c"), pulumi.String(seedCopyScript())},
			VolumeMounts: corev1.VolumeMountArray{
				f.mount(stateVolume, statePath),
				f.mount(seedVolume, seedPath),
			},
		},
	}
}

// pluginCopyScript replaces every ZIP in the plugins folder with the bundle.
// A ZIP that staff copy by hand does not survive a restart.
func pluginCopyScript() string {
	return fmt.Sprintf(
		"mkdir -p %[1]s/plugins && rm -f %[1]s/plugins/*.zip && cp /plugins/*.zip %[1]s/plugins/",
		statePath,
	)
}

func (f *Filestash) mainContainer() *corev1.ContainerArgs {
	cfg := f.Config
	return &corev1.ContainerArgs{
		Name:  pulumi.String("filestash"),
		Image: pulumi.String(cfg.Image.Ref()),
		Ports: corev1.ContainerPortArray{
			&corev1.ContainerPortArgs{Name: pulumi.String("http"), ContainerPort: pulumi.Int(cfg.Port)},
		},
		Env: corev1.EnvVarArray{
			f.secretEnv(adminPasswordKey),
			f.secretEnv(configSecretKey),
			&corev1.EnvVarArgs{Name: pulumi.String("APPLICATION_URL"), Value: pulumi.String(cfg.ApplicationURL)},
		},
		ReadinessProbe: f.probe(),
		LivenessProbe:  f.probe(),
		VolumeMounts: corev1.VolumeMountArray{
			f.mount(stateVolume, statePath),
			f.mount(cacheVolume, cachePath),
		},
	}
}

func (f *Filestash) secretEnv(key string) *corev1.EnvVarArgs {
	return &corev1.EnvVarArgs{
		Name: pulumi.String(key),
		ValueFrom: &corev1.EnvVarSourceArgs{
			SecretKeyRef: &corev1.SecretKeySelectorArgs{
				Name: pulumi.String(f.Config.SecretName),
				Key:  pulumi.String(key),
			},
		},
	}
}

func (f *Filestash) probe() *corev1.ProbeArgs {
	return &corev1.ProbeArgs{
		HttpGet: &corev1.HTTPGetActionArgs{
			Path: pulumi.String("/healthz"),
			Port: pulumi.String("http"),
		},
		InitialDelaySeconds: pulumi.Int(probeInitialDelay),
		PeriodSeconds:       pulumi.Int(probePeriodSeconds),
		TimeoutSeconds:      pulumi.Int(probeTimeoutSeconds),
	}
}

func (f *Filestash) mount(name, path string) *corev1.VolumeMountArgs {
	return &corev1.VolumeMountArgs{Name: pulumi.String(name), MountPath: pulumi.String(path)}
}

func (f *Filestash) volumes() corev1.VolumeArray {
	return corev1.VolumeArray{
		&corev1.VolumeArgs{
			Name: pulumi.String(stateVolume),
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSourceArgs{
				ClaimName: pulumi.String(f.resourceName("state")),
			},
		},
		&corev1.VolumeArgs{
			Name:     pulumi.String(cacheVolume),
			EmptyDir: &corev1.EmptyDirVolumeSourceArgs{SizeLimit: pulumi.String(f.Config.CacheSizeLimit)},
		},
		&corev1.VolumeArgs{
			Name:      pulumi.String(seedVolume),
			ConfigMap: &corev1.ConfigMapVolumeSourceArgs{Name: pulumi.String(f.resourceName("seed"))},
		},
	}
}
