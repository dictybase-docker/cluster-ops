package main

import (
	"fmt"

	barmancloudv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/barmancloud/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// objectStoreNameFor derives the ObjectStore CR name for a CNPG cluster
// name. The Cluster's plugins block references the store by exactly this
// derivation.
func objectStoreNameFor(clusterName string) string {
	return fmt.Sprintf("%s-store", clusterName)
}

// sourceObjectStoreNameFor derives the READ-ONLY ObjectStore CR name for a
// recovery source. The target and source clusters usually share the same
// name (both are typically `logto`), so the source store must be
// distinguishable from the target's own store or the two CRs collide.
func sourceObjectStoreNameFor(target string, source string) string {
	return fmt.Sprintf("%s-source-%s-store", target, source)
}

// createObjectStore creates the ObjectStore CR (barmancloud.cnpg.io/v1)
// consumed by the Barman Cloud CNPG-I plugin. The .spec.configuration
// schema mirrors the deprecated in-tree .spec.backup.barmanObjectStore —
// destinationPath, googleCredentials, wal, data — while retentionPolicy
// moved from the Cluster into the ObjectStore itself.
//
// destinationPath must be the full gs://<bucket>/<path> URI. retention may
// be empty (no GC of old backups). walBackup is only set for the cluster's
// own archiving store; a recovery source store is read-only and skips it.
// credsSecretName/credsSecretKey reference the Kubernetes Secret holding
// the GCS service account JSON key; the Secret must exist in the same
// namespace and be listed in depends.
func createObjectStore(
	ctx *pulumi.Context,
	name string,
	namespace string,
	destinationPath string,
	retention string,
	walBackup *WalBackup,
	credsSecretName string,
	credsSecretKey string,
	depends []pulumi.Resource,
) (*barmancloudv1.ObjectStore, error) {
	configuration := &barmancloudv1.ObjectStoreSpecConfigurationArgs{
		DestinationPath: pulumi.String(destinationPath),
		GoogleCredentials: &barmancloudv1.ObjectStoreSpecConfigurationGoogleCredentialsArgs{
			ApplicationCredentials: &barmancloudv1.ObjectStoreSpecConfigurationGoogleCredentialsApplicationCredentialsArgs{
				Name: pulumi.String(credsSecretName),
				Key:  pulumi.String(credsSecretKey),
			},
		},
	}
	if walBackup != nil {
		configuration.Wal = &barmancloudv1.ObjectStoreSpecConfigurationWalArgs{
			Compression: pulumi.String(walBackup.Compression),
			MaxParallel: pulumi.Int(walBackup.MaxParallel),
		}
		configuration.Data = &barmancloudv1.ObjectStoreSpecConfigurationDataArgs{
			Compression: pulumi.String(walBackup.Compression),
		}
	}

	spec := &barmancloudv1.ObjectStoreSpecArgs{
		Configuration: configuration,
	}
	if retention != "" {
		spec.RetentionPolicy = pulumi.String(retention)
	}

	store, err := barmancloudv1.NewObjectStore(ctx, name, &barmancloudv1.ObjectStoreArgs{
		ApiVersion: pulumi.String("barmancloud.cnpg.io/v1"),
		Kind:       pulumi.String("ObjectStore"),
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(namespace),
		},
		Spec: spec,
	}, pulumi.DependsOn(depends))
	if err != nil {
		return nil, fmt.Errorf("failed to create ObjectStore %s: %w", name, err)
	}
	return store, nil
}
