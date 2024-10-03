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
	secret, err := adb.createSecret(ctx)
	if err != nil {
		return err
	}

	if err := adb.createJob(ctx, secret); err != nil {
		return err
	}

	return nil
}

func (adb *ArangoDB) createSecret(ctx *pulumi.Context) (*corev1.Secret, error) {
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

	secret, err := corev1.NewSecret(ctx, secretName, secretArgs)
	if err != nil {
		return nil, fmt.Errorf("error creating ArangoDB secret: %w", err)
	}

	return secret, nil
}

func (adb *ArangoDB) createJob(ctx *pulumi.Context, secret *corev1.Secret) error {
	jobName := fmt.Sprintf(
		"%s-create-databases",
		adb.Config.ArangodbSecret.Name,
	)

	_, err := batchv1.NewJob(ctx, jobName, &batchv1.JobArgs{
		Metadata: adb.createMetadata(jobName),
		Spec:     adb.createJobSpec(),
	}, pulumi.DependsOn([]pulumi.Resource{secret}))
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

func (adb *ArangoDB) createContainerArgs() pulumi.StringArray {
	// Create the dbnames slice
	dbnames := make(pulumi.StringArray, 0, len(adb.Config.Databases)*2)
	for _, name := range adb.Config.Databases {
		dbnames = append(
			dbnames,
			pulumi.String("--database"),
			pulumi.String(name),
		)
	}

	args := pulumi.StringArray{
		pulumi.String("--log-level"),
		pulumi.String("info"),
		pulumi.String("create-database"),
		pulumi.String("--admin-user"),
		pulumi.String("root"),
		pulumi.String("--admin-password"),
		pulumi.String("$(ARANGODB_PASSWORD)"),
		pulumi.String("--user"),
		pulumi.String(adb.Config.ArangodbSecret.User),
		pulumi.String("--password"),
		pulumi.String(adb.Config.ArangodbSecret.Pass),
		pulumi.String("--grant"),
		pulumi.String(adb.Config.Grant),
	}
	// Concatenate args and dbnames
	return append(args, dbnames...)
}

func (adb *ArangoDB) createJobContainer() *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name: pulumi.String("create-databases"),
		Image: pulumi.String(
			fmt.Sprintf("%s:%s", adb.Config.Image.Name, adb.Config.Image.Tag),
		),
		Env:  adb.createEnvironmentVariables(),
		Args: adb.createContainerArgs(),
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
