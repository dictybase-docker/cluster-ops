package main

import (
	"fmt"
	"os"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"

	v1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/postgresql/v1"
)

type Image struct {
	Name string `pulumi:"name"`
	Tag  string `pulumi:"tag"`
}

type Storage struct {
	Class string `pulumi:"class"`
	Size  string `pulumi:"size"`
}

type Cluster struct {
	Image     Image   `pulumi:"image"`
	Instances int     `pulumi:"instances"`
	Name      string  `pulumi:"name"`
	Storage   Storage `pulumi:"storage"`
}

type Secret struct {
	Filepath string `pulumi:"filepath"`
	Key      string `pulumi:"key"`
	Name     string `pulumi:"name"`
}

type Properties struct {
	Namespace string  `pulumi:"namespace"`
	Cluster   Cluster `pulumi:"cluster"`
	Secret    Secret  `pulumi:"secret"`
}

func NewProperties(ctx *pulumi.Context) (*Properties, error) {
	props := &Properties{}
	cfg := config.New(ctx, "")
	if err := cfg.TryObject("properties", props); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return props, nil
}

func (prop *Properties) CreateSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	// Read the file content
	fileContent, err := os.ReadFile(prop.Secret.Filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Create the secret
	secret, err := corev1.NewSecret(ctx, prop.Secret.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(prop.Secret.Name),
			Namespace: pulumi.String(prop.Namespace),
		},
		StringData: pulumi.StringMap{
			prop.Secret.Key: pulumi.String(string(fileContent)),
		},
	})
	if err != nil {
		return nil, err
	}

	return secret, nil
}

func createResources(ctx *pulumi.Context) error {
	// Initialize Properties using the constructor
	props, err := NewProperties(ctx)
	if err != nil {
		return err
	}

	// Create the secret using the receiver method
	secret, err := props.CreateSecret(ctx)
	if err != nil {
		return err
	}

	// Create the PostgreSQL Cluster, passing the secret as a dependency
	cluster, err := props.CreatePostgresCluster(ctx, secret)
	if err != nil {
		return err
	}

	// Export the secret name and cluster name
	ctx.Export("secretName", secret.Metadata.Name())
	ctx.Export("clusterName", cluster.Metadata.Name())

	return nil
}

func (prop *Properties) CreatePostgresCluster(
	ctx *pulumi.Context,
	secret *corev1.Secret,
) (*v1.Cluster, error) {
	clusterArgs := prop.buildClusterArgs()
	cluster, err := v1.NewCluster(
		ctx, prop.Cluster.Name,
		clusterArgs,
		pulumi.DependsOn([]pulumi.Resource{secret}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL cluster: %w", err)
	}
	return cluster, nil
}

func (prop *Properties) buildClusterArgs() *v1.ClusterArgs {
	return &v1.ClusterArgs{
		ApiVersion: pulumi.String("postgresql.cnpg.io/v1"),
		Kind:       pulumi.String("Cluster"),
		Metadata:   prop.buildMetadata(),
		Spec:       prop.buildClusterSpec(),
	}
}

func (prop *Properties) buildMetadata() *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(prop.Cluster.Name),
		Namespace: pulumi.String(prop.Namespace),
	}
}

func (prop *Properties) buildClusterSpec() *v1.ClusterSpecArgs {
	return &v1.ClusterSpecArgs{
		Instances: pulumi.Int(prop.Cluster.Instances),
		ImageName: pulumi.String(
			fmt.Sprintf(
				"%s:%s",
				prop.Cluster.Image.Name,
				prop.Cluster.Image.Tag,
			),
		),
		Storage: prop.buildStorageArgs(),
	}
}

func (prop *Properties) buildStorageArgs() *v1.ClusterSpecStorageArgs {
	return &v1.ClusterSpecStorageArgs{
		StorageClass: pulumi.String(prop.Cluster.Storage.Class),
		Size:         pulumi.String(prop.Cluster.Storage.Size),
	}
}

func main() {
	pulumi.Run(createResources)
}
