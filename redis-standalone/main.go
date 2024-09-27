package main

import (
	"fmt"

	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type RedisStandaloneConfig struct {
	Image struct {
		Name string
		Tag  string
	}
	Name      string
	Namespace string
	Storage   struct {
		Class string
		Size  string
	}
}

type RedisStandalone struct {
	Config *RedisStandaloneConfig
}

func ReadConfig(ctx *pulumi.Context) (*RedisStandaloneConfig, error) {
	conf := config.New(ctx, "")
	redisConfig := &RedisStandaloneConfig{}
	if err := conf.TryObject("properties", redisConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read redis-standalone config: %w",
			err,
		)
	}
	return redisConfig, nil
}

func NewRedisStandalone(config *RedisStandaloneConfig) *RedisStandalone {
	return &RedisStandalone{
		Config: config,
	}
}

func (rds *RedisStandalone) Install(ctx *pulumi.Context) error {
	pvc, err := rds.createPersistentVolumeClaim(ctx)
	if err != nil {
		return err
	}

	err = rds.createDeployment(ctx, pvc)
	if err != nil {
		return err
	}

	err = rds.createService(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (rds *RedisStandalone) createService(ctx *pulumi.Context) error {
	_, err := corev1.NewService(ctx, rds.Config.Name, &corev1.ServiceArgs{
		Metadata: rds.createMetadata(),
		Spec: &corev1.ServiceSpecArgs{
			Selector: rds.createLabels(),
			Ports: corev1.ServicePortArray{
				&corev1.ServicePortArgs{
					Port:       pulumi.Int(6379),
					TargetPort: pulumi.Int(6379),
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("error creating Service: %w", err)
	}
	return nil
}

func (rds *RedisStandalone) createPersistentVolumeClaim(
	ctx *pulumi.Context,
) (*corev1.PersistentVolumeClaim, error) {
	pvc, err := corev1.NewPersistentVolumeClaim(
		ctx,
		fmt.Sprintf("%s-data", rds.Config.Name),
		&corev1.PersistentVolumeClaimArgs{
			Metadata: rds.createMetadata(),
			Spec: &corev1.PersistentVolumeClaimSpecArgs{
				AccessModes: pulumi.StringArray{
					pulumi.String("ReadWriteOnce"),
				},
				StorageClassName: pulumi.String(rds.Config.Storage.Class),
				Resources: &corev1.VolumeResourceRequirementsArgs{
					Requests: pulumi.StringMap{
						"storage": pulumi.String(rds.Config.Storage.Size),
					},
				},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error creating PersistentVolumeClaim: %w", err)
	}
	return pvc, nil
}

func (rds *RedisStandalone) createDeployment(
	ctx *pulumi.Context,
	pvc *corev1.PersistentVolumeClaim,
) error {
	_, err := appsv1.NewDeployment(ctx, rds.Config.Name, &appsv1.DeploymentArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(rds.Config.Name),
			Namespace: pulumi.String(rds.Config.Namespace),
			Labels:    rds.createLabels(),
		},
		Spec: rds.createDeploymentSpec(pvc),
	})
	if err != nil {
		return fmt.Errorf("error creating Deployment: %w", err)
	}
	return nil
}

func (rds *RedisStandalone) createDeploymentSpec(
	pvc *corev1.PersistentVolumeClaim,
) *appsv1.DeploymentSpecArgs {
	return &appsv1.DeploymentSpecArgs{
		Replicas: pulumi.Int(1),
		Selector: &metav1.LabelSelectorArgs{
			MatchLabels: rds.createLabels(),
		},
		Template: &corev1.PodTemplateSpecArgs{
			Metadata: &metav1.ObjectMetaArgs{
				Labels: rds.createLabels(),
			},
			Spec: rds.createPodSpec(pvc),
		},
	}
}

func (rds *RedisStandalone) createPodSpec(
	pvc *corev1.PersistentVolumeClaim,
) *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		InitContainers: corev1.ContainerArray{
			rds.createInitContainer(),
		},
		Containers: corev1.ContainerArray{
			rds.createRedisContainer(),
		},
		Volumes: corev1.VolumeArray{
			rds.createRedisVolume(pvc),
		},
	}
}

func (rds *RedisStandalone) createInitContainer() *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name:  pulumi.String("init-redis"),
		Image: pulumi.String("busybox"),
		Command: pulumi.StringArray{
			pulumi.String("sh"),
			pulumi.String("-c"),
			pulumi.String("chown -R 1000 /data"),
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name: pulumi.String(
					fmt.Sprintf("%s-data", rds.Config.Name),
				),
				MountPath: pulumi.String("/data"),
			},
		},
	}
}

func (rds *RedisStandalone) createRedisContainer() *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String(rds.Config.Name),
		Image: pulumi.String(
			fmt.Sprintf("%s:%s", rds.Config.Image.Name, rds.Config.Image.Tag),
		),
		Ports: corev1.ContainerPortArray{
			&corev1.ContainerPortArgs{ContainerPort: pulumi.Int(6379)},
		},
		VolumeMounts: corev1.VolumeMountArray{
			&corev1.VolumeMountArgs{
				Name: pulumi.String(
					fmt.Sprintf("%s-data", rds.Config.Name),
				),
				MountPath: pulumi.String("/data"),
			},
		},
	}
}

func (rds *RedisStandalone) createRedisVolume(
	pvc *corev1.PersistentVolumeClaim,
) *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String(fmt.Sprintf("%s-data", rds.Config.Name)),
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSourceArgs{
			ClaimName: pvc.Metadata.Name().Elem(),
		},
	}
}

func (rds *RedisStandalone) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(rds.Config.Name),
		Namespace: pulumi.String(rds.Config.Namespace),
		Labels:    rds.createLabels(),
	}
}

func (rds *RedisStandalone) createLabels() pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String(rds.Config.Name),
	}
}

func Run(ctx *pulumi.Context) error {
	redisConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	redisStandalone := NewRedisStandalone(redisConfig)

	if err := redisStandalone.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
