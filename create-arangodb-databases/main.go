package main

import (
	"fmt"

	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ArangoDBConfig struct {
	Namespace      string
	ArangodbSecret struct {
		Name    string
		User    string
		Pass    string
		UserKey string
		PassKey string
	}
	ArangodbCredentials struct {
		Name    string
		PassKey string
	}
	Databases []string
	Grant     string
	Image     struct {
		Name string
		Tag  string
	}
}

type ArangoDB struct {
	Config *ArangoDBConfig
}

func ReadConfig(ctx *pulumi.Context) (*ArangoDBConfig, error) {
	conf := config.New(ctx, "")
	arangoConfig := &ArangoDBConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read create-arangodb-databases config: %w",
			err,
		)
	}
	return arangoConfig, nil
}

func NewArangoDB(config *ArangoDBConfig) *ArangoDB {
	return &ArangoDB{
		Config: config,
	}
}

func (adb *ArangoDB) Install(ctx *pulumi.Context) error {
	if err := adb.createSecret(ctx); err != nil {
		return err
	}

	if err := adb.createJob(ctx); err != nil {
		return err
	}

	return nil
}

func (adb *ArangoDB) createSecret(ctx *pulumi.Context) error {
	secretName := adb.Config.ArangodbSecret.Name
	secretArgs := &corev1.SecretArgs{
		Metadata: adb.createMetadata(secretName),
		StringData: pulumi.StringMap{
			adb.Config.ArangodbSecret.UserKey: pulumi.String(
				adb.Config.ArangodbSecret.User,
			),
			adb.Config.ArangodbSecret.PassKey: pulumi.String(
				adb.Config.ArangodbSecret.Pass,
			),
		},
		Type: pulumi.String("Opaque"),
	}

	_, err := corev1.NewSecret(ctx, secretName, secretArgs)
	if err != nil {
		return fmt.Errorf("error creating ArangoDB secret: %w", err)
	}

	return nil
}

func (adb *ArangoDB) createJob(ctx *pulumi.Context) error {
	jobName := fmt.Sprintf(
		"%s-create-databases",
		adb.Config.ArangodbSecret.Name,
	)

	_, err := batchv1.NewJob(ctx, jobName, &batchv1.JobArgs{
		Metadata: adb.createMetadata(jobName),
		Spec:     adb.createJobSpec(),
	})
	if err != nil {
		return fmt.Errorf("error creating ArangoDB job: %w", err)
	}

	return nil
}

func (adb *ArangoDB) createJobSpec() *batchv1.JobSpecArgs {
	return &batchv1.JobSpecArgs{
		BackoffLimit: pulumi.Int(0),
		Template: &corev1.PodTemplateSpecArgs{
			Spec: adb.createPodSpec(),
		},
		TtlSecondsAfterFinished: pulumi.Int(900),
	}
}

func (adb *ArangoDB) createPodSpec() *corev1.PodSpecArgs {
	return &corev1.PodSpecArgs{
		RestartPolicy: pulumi.String("Never"),
		Containers: corev1.ContainerArray{
			adb.createJobContainer(),
		},
	}
}

func (adb *ArangoDB) createJobContainer() *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String("create-databases"),
		Image: pulumi.String(
			fmt.Sprintf("%s:%s", adb.Config.Image.Name, adb.Config.Image.Tag),
		),
		Env:  adb.createEnvironmentVariables(),
		Args: pulumi.StringArray{},
	}
}

func (adb *ArangoDB) createEnvironmentVariables() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		adb.createSecretEnvVar("ARANGODB_PASSWORD",
			adb.Config.ArangodbCredentials.Name,
			adb.Config.ArangodbCredentials.PassKey),
	}
}

func (adb *ArangoDB) createSecretEnvVar(
	envName, secretName, secretKey string,
) *corev1.EnvVarArgs {
	return &corev1.EnvVarArgs{
		Name: pulumi.String(envName),
		ValueFrom: &corev1.EnvVarSourceArgs{
			SecretKeyRef: &corev1.SecretKeySelectorArgs{
				Name: pulumi.String(secretName),
				Key:  pulumi.String(secretKey),
			},
		},
	}
}

func (adb *ArangoDB) createMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(adb.Config.Namespace),
		Labels:    adb.createLabels(),
	}
}

func (adb *ArangoDB) createLabels() pulumi.StringMap {
	return pulumi.StringMap{
		"app": pulumi.String("arangodb-create-databases"),
	}
}

func Run(ctx *pulumi.Context) error {
	arangoConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	arangoDB := NewArangoDB(arangoConfig)

	if err := arangoDB.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
