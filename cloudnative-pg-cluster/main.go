package main

import (
	"fmt"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(CreateResources)
}

func CreateResources(ctx *pulumi.Context) error {
	// Initialize Properties using the constructor
	props, err := NewProperties(ctx)
	if err != nil {
		return err
	}

	for i, clusterWrapper := range props.Clusters {
		cluster := clusterWrapper.Cluster

		// Create GCS bucket for each cluster
		bucket, err := createBackupGCSBucket(
			ctx,
			cluster.Backup.Bucket,
			cluster.Backup.BucketLocation,
		)
		if err != nil {
			return err
		}
		ctx.Export(fmt.Sprintf("bucketName_%d", i), bucket.Name)

		// Create the secret using the receiver method
		secret, err := props.CreateBackupSecret(ctx, cluster)
		if err != nil {
			return err
		}

		// Create the basic auth secret for each cluster
		basicAuthSecret, err := props.CreateUserSecret(ctx, cluster)
		if err != nil {
			return err
		}

		// Create the PostgreSQL Cluster, passing both secrets and the bucket as dependencies
		pgCluster, err := props.CreatePostgresCluster(
			ctx,
			cluster,
			secret,
			basicAuthSecret,
			bucket,
		)
		if err != nil {
			return err
		}

		// Export the secret names and cluster name for each cluster
		ctx.Export(fmt.Sprintf("secretName_%d", i), secret.Metadata.Name())
		ctx.Export(
			fmt.Sprintf("basicAuthSecretName_%d", i),
			basicAuthSecret.Metadata.Name(),
		)
		ctx.Export(fmt.Sprintf("clusterName_%d", i), pgCluster.Metadata.Name())
	}

	return nil
}
