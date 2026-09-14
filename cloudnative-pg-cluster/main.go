package main

import (
	"fmt"

	barmancloudv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/barmancloud/v1"
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
		if err := props.deployCluster(ctx, clusterWrapper.Cluster, i); err != nil {
			return err
		}
	}
	return nil
}

// deployCluster creates every resource of one configured cluster: the GCS
// backup bucket, the backup-credentials and app-user Secrets, the Barman
// Cloud plugin ObjectStore (plus a read-only source store when a cross-
// project recovery source is configured), the Cluster CR, and the
// ScheduledBackup. Stack exports carry the resource names for inspection.
func (prop *Properties) deployCluster(
	ctx *pulumi.Context,
	cluster Cluster,
	i int,
) error {
	bucket, err := createBackupGCSBucket(
		ctx,
		cluster.Backup.Bucket,
		cluster.Backup.BucketLocation,
	)
	if err != nil {
		return err
	}
	ctx.Export(fmt.Sprintf("bucketName_%d", i), bucket.Name)

	secret, err := prop.CreateBackupSecret(ctx, cluster)
	if err != nil {
		return err
	}

	basicAuthSecret, err := prop.CreateUserSecret(ctx, cluster)
	if err != nil {
		return err
	}

	// ObjectStore for the cluster's own WAL archive + backups. The Barman
	// Cloud plugin reads it; retentionPolicy lives here, not on the
	// Cluster (the deprecated in-tree backup block is gone).
	objectStore, err := createObjectStore(
		ctx,
		objectStoreNameFor(cluster.Name),
		cluster.Namespace,
		fmt.Sprintf("gs://%s/%s", cluster.Backup.Bucket, cluster.Backup.BucketPath),
		cluster.Backup.Retention,
		&cluster.WalBackup,
		prop.BackupSecret.Name,
		prop.BackupSecret.Key,
		[]pulumi.Resource{bucket, secret},
	)
	if err != nil {
		return err
	}
	ctx.Export(
		fmt.Sprintf("objectStoreName_%d", i),
		pulumi.String(objectStoreNameFor(cluster.Name)),
	)

	// The source-reader Secret and ObjectStore exist only when a
	// cross-project recovery source is configured.
	var sourceObjectStore *barmancloudv1.ObjectStore
	if cluster.Bootstrap.Recovery != nil {
		sourceObjectStore, err = prop.createSourceObjectStore(ctx, cluster, i)
		if err != nil {
			return err
		}
	}

	// Create the PostgreSQL Cluster with secrets and object stores as
	// dependencies
	pgCluster, err := prop.CreatePostgresCluster(
		ctx,
		cluster,
		secret,
		basicAuthSecret,
		objectStore,
		sourceObjectStore,
	)
	if err != nil {
		return err
	}

	ctx.Export(fmt.Sprintf("secretName_%d", i), secret.Metadata.Name())
	ctx.Export(
		fmt.Sprintf("basicAuthSecretName_%d", i),
		basicAuthSecret.Metadata.Name(),
	)
	ctx.Export(fmt.Sprintf("clusterName_%d", i), pgCluster.Metadata.Name())
	return nil
}

// createSourceObjectStore wires the cross-project recovery source: the
// reader-key Secret plus a read-only ObjectStore for the source bucket,
// referenced by the externalClusters plugin entry. Requires
// properties.sourceSecret, set by `just postgres configure-source`.
func (prop *Properties) createSourceObjectStore(
	ctx *pulumi.Context,
	cluster Cluster,
	i int,
) (*barmancloudv1.ObjectStore, error) {
	recovery := cluster.Bootstrap.Recovery
	if prop.SourceSecret == nil {
		return nil, fmt.Errorf(
			"bootstrap.recovery is set but properties.sourceSecret is missing — " +
				"run `just postgres configure-source` first",
		)
	}
	sourceSecret, err := prop.CreateSourceSecret(ctx, cluster)
	if err != nil {
		return nil, err
	}
	storeName := sourceObjectStoreNameFor(cluster.Name, recovery.SourceCluster)
	store, err := createObjectStore(
		ctx,
		storeName,
		cluster.Namespace,
		fmt.Sprintf(
			"gs://%s/%s",
			recovery.Bucket,
			recovery.BucketPath,
		),
		"", // read-only source: no retention GC here
		nil,
		prop.SourceSecret.Name,
		prop.SourceSecret.Key,
		[]pulumi.Resource{sourceSecret},
	)
	if err != nil {
		return nil, err
	}
	ctx.Export(
		fmt.Sprintf("sourceObjectStoreName_%d", i),
		pulumi.String(storeName),
	)
	return store, nil
}
