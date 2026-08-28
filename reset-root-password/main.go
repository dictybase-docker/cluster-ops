package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ResetRootConfig struct {
	Namespace           string
	Server              string
	Port                int
	ArangodbCredentials struct {
		Name    string
		PassKey string
	}
	JwtSecret struct {
		Name     string
		TokenKey string
	}
	Image struct {
		Name string
		Tag  string
	}
}

func (c *ResetRootConfig) applyDefaults() {
	if c.Server == "" {
		c.Server = "arangodb"
	}
	if c.Port == 0 {
		c.Port = 8529
	}
	if c.Image.Name == "" {
		c.Image.Name = "arangodb/arangodb"
	}
	if c.Image.Tag == "" {
		c.Image.Tag = "3.12.10.1"
	}
	if c.JwtSecret.Name == "" {
		c.JwtSecret.Name = "arangodb-jwt"
	}
	if c.JwtSecret.TokenKey == "" {
		c.JwtSecret.TokenKey = "token"
	}
}

type ResetRoot struct {
	Config *ResetRootConfig
}

func ReadConfig(ctx *pulumi.Context) (*ResetRootConfig, error) {
	conf := config.New(ctx, "")
	cfg := &ResetRootConfig{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf(
			"failed to read reset-root-password config: %w",
			err,
		)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func NewResetRoot(config *ResetRootConfig) *ResetRoot {
	return &ResetRoot{
		Config: config,
	}
}

func (rr *ResetRoot) Install(ctx *pulumi.Context) error {
	if err := rr.createJob(ctx); err != nil {
		return err
	}
	return nil
}

func (rr *ResetRoot) createJob(ctx *pulumi.Context) error {
	jobName := "arangodb-reset-root-password"

	_, err := batchv1.NewJob(ctx, jobName, &batchv1.JobArgs{
		Metadata: rr.createMetadata(jobName),
		Spec:     rr.createJobSpec(),
	})
	if err != nil {
		return fmt.Errorf("error creating reset-root job: %w", err)
	}

	return nil
}

func (rr *ResetRoot) createJobSpec() *batchv1.JobSpecArgs {
	return &batchv1.JobSpecArgs{
		BackoffLimit: pulumi.Int(0),
		Template: &corev1.PodTemplateSpecArgs{
			Spec: rr.createPodSpec(),
		},
		TtlSecondsAfterFinished: pulumi.Int(900), // 15 minutes
	}
}

func (rr *ResetRoot) createPodSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		RestartPolicy: pulumi.String("Never"),
		Containers: corev1.ContainerArray{
			rr.createContainer(),
		},
		Volumes: corev1.VolumeArray{
			rr.createJwtVolume(),
		},
	}
}

func (rr *ResetRoot) createContainer() *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name:  pulumi.String("reset-root"),
		Image: pulumi.String(fmt.Sprintf("%s:%s", rr.Config.Image.Name, rr.Config.Image.Tag)),
		Env:   rr.createEnvironmentVariables(),
		Args:  rr.createArgs(),
		VolumeMounts: corev1.VolumeMountArray{
			rr.createJwtVolumeMount(),
		},
	}
}

func (rr *ResetRoot) createArgs() pulumi.StringArray {
	endpoint := fmt.Sprintf("http+tcp://%s:%d", rr.Config.Server, rr.Config.Port)
	return pulumi.StringArray{
		pulumi.String("arangosh"),
		pulumi.String("--server.endpoint"),
		pulumi.String(endpoint),
		pulumi.String("--server.jwt-secret-keyfile"),
		pulumi.String("/var/secret/jwt.secret"),
		pulumi.String("--javascript.execute-string"),
		pulumi.String(
			"require('@arangodb/users').update('root', process.env.ARANGO_NEW_PASS); " +
				"print('Root password reset successfully.');",
		),
	}
}

func (rr *ResetRoot) createEnvironmentVariables() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("ARANGO_NEW_PASS"),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name: pulumi.String(rr.Config.ArangodbCredentials.Name),
					Key:  pulumi.String(rr.Config.ArangodbCredentials.PassKey),
				},
			},
		},
	}
}

func (rr *ResetRoot) createJwtVolume() *corev1.VolumeArgs {
	return &corev1.VolumeArgs{
		Name: pulumi.String("jwt-secret"),
		Secret: &corev1.SecretVolumeSourceArgs{
			SecretName: pulumi.String(rr.Config.JwtSecret.Name),
			Items: corev1.KeyToPathArray{
				&corev1.KeyToPathArgs{
					Key:  pulumi.String(rr.Config.JwtSecret.TokenKey),
					Path: pulumi.String("jwt.secret"),
				},
			},
		},
	}
}

func (rr *ResetRoot) createJwtVolumeMount() *corev1.VolumeMountArgs {
	return &corev1.VolumeMountArgs{
		Name:      pulumi.String("jwt-secret"),
		MountPath: pulumi.String("/var/secret"),
		ReadOnly:  pulumi.Bool(true),
	}
}

func (rr *ResetRoot) createMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(rr.Config.Namespace),
		Labels: pulumi.StringMap{
			"app": pulumi.String("arangodb-reset-root-password"),
		},
	}
}

func Run(ctx *pulumi.Context) error {
	config, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	app := NewResetRoot(config)

	if err := app.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
