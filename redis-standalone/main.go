package main

import (
	"fmt"

	redisv1beta2 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/redis/v1beta2"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type StorageResourcesArgs = redisv1beta2.RedisSpecStorageVolumeClaimTemplateSpecResourcesArgs

type RedisStandaloneConfig struct {
	Image struct {
		Name string
		Tag  string
	}
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
	conf := config.New(ctx, "redis-standalone")
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
	redis, err := redisv1beta2.NewRedis(
		ctx,
		"redis-standalone",
		&redisv1beta2.RedisArgs{
			Metadata: rds.createMetadata(),
			Spec:     rds.createRedisSpec(),
		},
	)
	if err != nil {
		return fmt.Errorf("error creating Redis standalone: %w", err)
	}

	ctx.Export("redisName", redis.Metadata.Name())
	return nil
}

func (rds *RedisStandalone) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String("redis-standalone"),
		Namespace: pulumi.String(rds.Config.Namespace),
	}
}

func (rds *RedisStandalone) createRedisSpec() *redisv1beta2.RedisSpecArgs {
	return &redisv1beta2.RedisSpecArgs{
		Storage:          rds.createStorageSpec(),
		KubernetesConfig: rds.createKubernetesConfig(),
		LivenessProbe:    rds.createLivenessProbe(),
		ReadinessProbe:   rds.createReadinessProbe(),
		SecurityContext:  rds.createSecurityContext(),
	}
}

func (rds *RedisStandalone) createSecurityContext() *redisv1beta2.RedisSpecSecurityContextArgs {
	return &redisv1beta2.RedisSpecSecurityContextArgs{
		RunAsUser: pulumi.Int(1000),
	}
}

func (rds *RedisStandalone) createStorageSpec() *redisv1beta2.RedisSpecStorageArgs {
	return &redisv1beta2.RedisSpecStorageArgs{
		VolumeClaimTemplate: &redisv1beta2.RedisSpecStorageVolumeClaimTemplateArgs{
			Spec: &redisv1beta2.RedisSpecStorageVolumeClaimTemplateSpecArgs{
				AccessModes: pulumi.StringArray{
					pulumi.String("ReadWriteOnce"),
				},
				StorageClassName: pulumi.String(rds.Config.Storage.Class),
				Resources:        rds.createStorageResources(),
			},
		},
	}
}

func (rds *RedisStandalone) createStorageResources() *StorageResourcesArgs {
	return &StorageResourcesArgs{
		Requests: pulumi.Map{
			"storage": pulumi.String(rds.Config.Storage.Size),
		},
	}
}

func (rds *RedisStandalone) createKubernetesConfig() *redisv1beta2.RedisSpecKubernetesConfigArgs {
	return &redisv1beta2.RedisSpecKubernetesConfigArgs{
		Image: pulumi.String(
			fmt.Sprintf("%s:%s", rds.Config.Image.Name, rds.Config.Image.Tag),
		),
		ImagePullPolicy: pulumi.String("IfNotPresent"),
	}
}

func (rds *RedisStandalone) createLivenessProbe() *redisv1beta2.RedisSpecLivenessProbeArgs {
	return &redisv1beta2.RedisSpecLivenessProbeArgs{
		FailureThreshold:    pulumi.Int(5),
		InitialDelaySeconds: pulumi.Int(15),
		PeriodSeconds:       pulumi.Int(15),
		SuccessThreshold:    pulumi.Int(1),
		TimeoutSeconds:      pulumi.Int(5),
	}
}

func (rds *RedisStandalone) createReadinessProbe() *redisv1beta2.RedisSpecReadinessProbeArgs {
	return &redisv1beta2.RedisSpecReadinessProbeArgs{
		FailureThreshold:    pulumi.Int(5),
		InitialDelaySeconds: pulumi.Int(15),
		PeriodSeconds:       pulumi.Int(15),
		SuccessThreshold:    pulumi.Int(1),
		TimeoutSeconds:      pulumi.Int(5),
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
