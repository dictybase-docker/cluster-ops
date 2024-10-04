package main

import (
	"fmt"

	databasev1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/database/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type (
	VolumeClaimTemplateSpec     = databasev1.ArangoDeploymentSpecSingleVolumeClaimTemplateArgs
	VolumeClaimTemplateSpecArgs = databasev1.ArangoDeploymentSpecSingleVolumeClaimTemplateSpecArgs
	ResourcesSpec               = databasev1.ArangoDeploymentSpecSingleVolumeClaimTemplateSpecResourcesRequestsArgs
	ResourcesArgs               = databasev1.ArangoDeploymentSpecSingleVolumeClaimTemplateSpecResourcesArgs
)

type ArangoDeploymentConfig struct {
	Namespace string
	Storage   struct {
		Class string
		Size  string
	}
	Version string
	Secret  struct {
		Name     string
		Password string
	}
}

type ArangoDeployment struct {
	Config *ArangoDeploymentConfig
}

func ReadConfig(ctx *pulumi.Context) (*ArangoDeploymentConfig, error) {
	conf := config.New(ctx, "")
	arangoConfig := &ArangoDeploymentConfig{}
	if err := conf.TryObject("properties", arangoConfig); err != nil {
		return nil, fmt.Errorf(
			"failed to read arangodb-cluster config: %w",
			err,
		)
	}
	return arangoConfig, nil
}

func NewArangoDeployment(config *ArangoDeploymentConfig) *ArangoDeployment {
	return &ArangoDeployment{
		Config: config,
	}
}

func (adp *ArangoDeployment) Install(ctx *pulumi.Context) error {
	secret, err := corev1.NewSecret(ctx, "arangodb-secret", &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(adp.Config.Secret.Name),
			Namespace: pulumi.String(adp.Config.Namespace),
			Labels: pulumi.StringMap{
				"velero.io/exclude-from-backup": pulumi.String("true"),
			},
		},
		StringData: pulumi.StringMap{
			"password": pulumi.String(adp.Config.Secret.Password),
		},
	})
	if err != nil {
		return fmt.Errorf("error creating Secret: %w", err)
	}

	arango, err := databasev1.NewArangoDeployment(
		ctx,
		"arangodb",
		&databasev1.ArangoDeploymentArgs{
			Metadata: adp.createMetadata(),
			Spec:     adp.createArangoSpec(),
		},
		pulumi.DependsOn([]pulumi.Resource{secret}),
	)
	if err != nil {
		return fmt.Errorf("error creating ArangoDeployment: %w", err)
	}

	ctx.Export("arangoName", arango.Metadata.Name())
	ctx.Export("secretName", secret.Metadata.Name())
	return nil
}

func (adp *ArangoDeployment) createMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String("arangodb"),
		Namespace: pulumi.String(adp.Config.Namespace),
		Labels: pulumi.StringMap{
			"velero.io/exclude-from-backup": pulumi.String("true"),
		},
	}
}

func (adp *ArangoDeployment) createArangoSpec() *databasev1.ArangoDeploymentSpecArgs {
	return &databasev1.ArangoDeploymentSpecArgs{
		Mode:            pulumi.String("Single"),
		Image:           adp.createImageSpec(),
		ImagePullPolicy: pulumi.String("IfNotPresent"),
		Environment:     pulumi.String("Development"),
		Single:          adp.createSingleSpec(),
		ExternalAccess: &databasev1.ArangoDeploymentSpecExternalAccessArgs{
			Type: pulumi.String("NodePort"),
		},
		Tls: &databasev1.ArangoDeploymentSpecTlsArgs{
			CaSecretName: pulumi.String("None"),
		},
		Bootstrap: &databasev1.ArangoDeploymentSpecBootstrapArgs{
			PasswordSecretNames: pulumi.StringMap{
				"root": pulumi.String(adp.Config.Secret.Name),
			},
		},
	}
}

func (adp *ArangoDeployment) createImageSpec() pulumi.StringInput {
	return pulumi.String(fmt.Sprintf("arangodb:%s", adp.Config.Version))
}

func (adp *ArangoDeployment) createSingleSpec() *databasev1.ArangoDeploymentSpecSingleArgs {
	return &databasev1.ArangoDeploymentSpecSingleArgs{
		VolumeClaimTemplate: &VolumeClaimTemplateSpec{
			Spec: adp.createVolumeClaimTemplateSpec(),
		},
	}
}

func (adp *ArangoDeployment) createVolumeClaimTemplateSpec() *VolumeClaimTemplateSpecArgs {
	return &VolumeClaimTemplateSpecArgs{
		StorageClassName: pulumi.String(adp.Config.Storage.Class),
		VolumeMode:       pulumi.String("Filesystem"),
		AccessModes: pulumi.StringArray{
			pulumi.String("ReadWriteOnce"),
		},
		Resources: &ResourcesArgs{
			Requests: &ResourcesSpec{
				Storage: pulumi.String(adp.Config.Storage.Size),
			},
		},
	}
}

func Run(ctx *pulumi.Context) error {
	arangoConfig, err := ReadConfig(ctx)
	if err != nil {
		return err
	}

	arangoDeployment := NewArangoDeployment(arangoConfig)

	if err := arangoDeployment.Install(ctx); err != nil {
		return err
	}

	return nil
}

func main() {
	pulumi.Run(Run)
}
