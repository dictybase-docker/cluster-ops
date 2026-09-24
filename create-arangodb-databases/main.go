package main

import (
	"fmt"
	"strings"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type ArangoDBConfig struct {
	Namespace       string
	CreateDatabases *bool
	RunID           string
	ArangodbSecret  struct {
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

func (cfg *ArangoDBConfig) applyDefaults() {
	if cfg.CreateDatabases == nil {
		createDatabases := true
		cfg.CreateDatabases = &createDatabases
	}
	if cfg.RunID == "" {
		cfg.RunID = "manual"
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
	arangoConfig.applyDefaults()
	return arangoConfig, nil
}

func NewArangoDB(config *ArangoDBConfig) *ArangoDB {
	config.applyDefaults()
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
	jobName := adb.jobName()

	_, err := batchv1.NewJob(ctx, jobName, &batchv1.JobArgs{
		Metadata: adb.createMetadata(jobName),
		Spec:     adb.createJobSpec(),
	}, pulumi.DependsOn([]pulumi.Resource{secret}))
	if err != nil {
		return fmt.Errorf("error creating ArangoDB job: %w", err)
	}

	return nil
}

func (adb *ArangoDB) createsDatabases() bool {
	return adb.Config.CreateDatabases == nil || *adb.Config.CreateDatabases
}

func (adb *ArangoDB) jobName() string {
	operation := "configure-app-credentials"
	if adb.createsDatabases() {
		operation = "create-databases"
	}
	return fmt.Sprintf("%s-%s-%s", adb.Config.ArangodbSecret.Name, operation, adb.Config.RunID)
}

func (adb *ArangoDB) createJobSpec() *batchv1.JobSpecArgs {
	// Keep completed Jobs until the next Pulumi run replaces them. A TTL can
	// delete a Job while Pulumi still tracks it, making the next preview fail
	// with a missing-resource error before it can recreate the Job.
	return &batchv1.JobSpecArgs{
		BackoffLimit: pulumi.Int(0),
		Template: &corev1.PodTemplateSpecArgs{
			Spec: adb.createPodSpec(),
		},
	}
}

func (adb *ArangoDB) createPodSpec() *corev1.PodSpecArgs {
	initContainers := corev1.ContainerArray{
		adb.createContainer("ensure-user", adb.ensureUserArgs()),
	}
	if adb.createsDatabases() {
		databaseContainers := F.Pipe1(
			adb.Config.Databases,
			A.Map(adb.ensureDatabaseContainer),
		)
		initContainers = append(initContainers, databaseContainers...)
	}
	grantContainers := corev1.ContainerArray(F.Pipe1(
		adb.Config.Databases,
		A.Map(adb.ensureGrantContainer),
	))

	return &corev1.PodSpecArgs{
		RestartPolicy:  pulumi.String("Never"),
		InitContainers: initContainers,
		Containers:     grantContainers,
	}
}

func (adb *ArangoDB) ensureDatabaseContainer(dbName string) corev1.ContainerInput {
	args := adb.ensureDatabaseArgs(dbName)
	return adb.createContainer(containerName("ensure-database", dbName), args)
}

func (adb *ArangoDB) ensureGrantContainer(dbName string) corev1.ContainerInput {
	args := adb.ensureGrantArgs(dbName)
	return adb.createContainer(containerName("ensure-grant", dbName), args)
}

func containerName(prefix, dbName string) string {
	return prefix + "-" + strings.ReplaceAll(dbName, "_", "-")
}

func (adb *ArangoDB) createContainer(name string, args pulumi.StringArray) *corev1.ContainerArgs {
	return &corev1.ContainerArgs{
		Name:  pulumi.String(name),
		Image: pulumi.String(fmt.Sprintf("%s:%s", adb.Config.Image.Name, adb.Config.Image.Tag)),
		Env:   adb.createEnvironmentVariables(),
		Args:  args,
	}
}

func (adb *ArangoDB) ensureUserArgs() pulumi.StringArray {
	// Kubernetes expands these env references at container start; keeping the
	// password out of Args avoids copying it into the Pod spec and Job state.
	return F.Pipe1(
		[]string{
			"ensure-user",
			"--user",
			"$(ARANGODB_USER)",
			"--password",
			"$(ARANGODB_APP_PASSWORD)",
			"--admin-password",
			"$(ARANGODB_PASSWORD)",
			"--password-policy",
			"always",
		},
		A.Map(toPulumiString),
	)
}

func toPulumiString(value string) pulumi.StringInput {
	return pulumi.String(value)
}

func (adb *ArangoDB) ensureDatabaseArgs(dbName string) pulumi.StringArray {
	return pulumi.StringArray{
		pulumi.String("ensure-database"),
		pulumi.String("--database"),
		pulumi.String(dbName),
		pulumi.String("--admin-password"),
		pulumi.String("$(ARANGODB_PASSWORD)"),
	}
}

func (adb *ArangoDB) ensureGrantArgs(dbName string) pulumi.StringArray {
	return pulumi.StringArray{
		pulumi.String("ensure-grant"),
		pulumi.String("--user"),
		pulumi.String("$(ARANGODB_USER)"),
		pulumi.String("--database"),
		pulumi.String(dbName),
		pulumi.String("--grant"),
		pulumi.String(adb.Config.Grant),
		pulumi.String("--admin-password"),
		pulumi.String("$(ARANGODB_PASSWORD)"),
	}
}

func (adb *ArangoDB) createEnvironmentVariables() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		adb.createSecretEnvVar("ARANGODB_PASSWORD",
			adb.Config.ArangodbCredentials.Name,
			adb.Config.ArangodbCredentials.PassKey),
		adb.createSecretEnvVar("ARANGODB_USER",
			adb.Config.ArangodbSecret.Name,
			adb.Config.ArangodbSecret.UserKey),
		adb.createSecretEnvVar("ARANGODB_APP_PASSWORD",
			adb.Config.ArangodbSecret.Name,
			adb.Config.ArangodbSecret.PassKey),
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
