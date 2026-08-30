package main

import (
	"testing"

	cnpgv1 "github.com/dictybase-docker/cluster-ops/crds/kubernetes/postgresql/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testClusterName = "logto"

func TestBuildAffinityArgsEmptyPlacementReturnsNil(t *testing.T) {
	props := &Properties{}
	args := props.buildAffinityArgs(Cluster{})
	assert.Nil(t, args)
}

func TestBuildAffinityArgsProductionPlacement(t *testing.T) {
	props := &Properties{}
	cluster := Cluster{
		Placement: Placement{
			Pool:                "database",
			TopologyKey:         "topology.kubernetes.io/zone",
			PodAntiAffinityType: "preferred",
		},
	}

	args := props.buildAffinityArgs(cluster)
	require.NotNil(t, args)

	selector, ok := args.NodeSelector.(pulumi.StringMap)
	require.True(t, ok, "NodeSelector should be a concrete pulumi.StringMap")
	assert.Equal(t, pulumi.String("database"), selector["pool"])

	tolerations, ok := args.Tolerations.(cnpgv1.ClusterSpecAffinityTolerationsArray)
	require.True(t, ok, "Tolerations should be a concrete array")
	require.Len(t, tolerations, 1)
	toleration, ok := tolerations[0].(*cnpgv1.ClusterSpecAffinityTolerationsArgs)
	require.True(t, ok, "toleration should be a concrete args struct")
	assert.Equal(t, pulumi.String("dedicated"), toleration.Key)
	assert.Equal(t, pulumi.String("Equal"), toleration.Operator)
	assert.Equal(t, pulumi.String("database"), toleration.Value)
	assert.Equal(t, pulumi.String("NoSchedule"), toleration.Effect)

	assert.Equal(
		t,
		pulumi.String("topology.kubernetes.io/zone"),
		args.TopologyKey,
	)
	assert.Equal(t, pulumi.String("preferred"), args.PodAntiAffinityType)
}

func TestBuildAffinityArgsPoolOnlyLeavesOperatorDefaults(t *testing.T) {
	props := &Properties{}
	cluster := Cluster{Placement: Placement{Pool: "database"}}

	args := props.buildAffinityArgs(cluster)
	require.NotNil(t, args)
	assert.Nil(t, args.TopologyKey)
	assert.Nil(t, args.PodAntiAffinityType)
}

func recoveryCluster(targetTime string) Cluster {
	return Cluster{
		Bootstrap: Bootstrap{
			Database: testClusterName,
			Owner:    testClusterName,
			UserSecret: BootstrapSecret{
				Name:     "logto-app",
				Password: "secret",
			},
			Recovery: &RecoverySource{
				SourceCluster: testClusterName,
				Bucket:        "pgbackup-devenv",
				BucketPath:    testClusterName,
				TargetTime:    targetTime,
			},
		},
	}
}

func TestBuildBootstrapArgsDefaultsToInitdb(t *testing.T) {
	props := &Properties{}
	args := props.buildBootstrapArgs(Cluster{
		Bootstrap: Bootstrap{
			Database:   testClusterName,
			Owner:      testClusterName,
			UserSecret: BootstrapSecret{Name: "logto-app"},
		},
	})
	require.NotNil(t, args)
	require.NotNil(t, args.Initdb)
	assert.Nil(t, args.Recovery)
}

func TestBuildBootstrapArgsRecoverySwitchesSource(t *testing.T) {
	props := &Properties{}
	args := props.buildBootstrapArgs(recoveryCluster(""))
	require.NotNil(t, args)
	require.NotNil(t, args.Recovery)
	assert.Nil(t, args.Initdb)
	recovery, ok := args.Recovery.(*cnpgv1.ClusterSpecBootstrapRecoveryArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(testClusterName), recovery.Source)
	assert.Nil(t, recovery.RecoveryTarget)
}

func TestBuildBootstrapArgsRecoveryWithTargetTime(t *testing.T) {
	props := &Properties{}
	args := props.buildBootstrapArgs(recoveryCluster("2026-08-29T00:00:00Z"))
	recovery, ok := args.Recovery.(*cnpgv1.ClusterSpecBootstrapRecoveryArgs)
	require.True(t, ok)
	require.NotNil(t, recovery.RecoveryTarget)
	target, ok := recovery.RecoveryTarget.(*cnpgv1.ClusterSpecBootstrapRecoveryRecoveryTargetArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("2026-08-29T00:00:00Z"), target.TargetTime)
}

func TestBuildExternalClustersArgsNilWithoutRecovery(t *testing.T) {
	props := &Properties{}
	assert.Nil(t, props.buildExternalClustersArgs(Cluster{}))
}

func TestBuildExternalClustersArgsNilWithoutSourceSecret(t *testing.T) {
	props := &Properties{}
	assert.Nil(t, props.buildExternalClustersArgs(recoveryCluster("")))
}

type googleCredsArgs = cnpgv1.ClusterSpecExternalClustersBarmanObjectStoreGoogleCredentialsArgs
type appCredsArgs = cnpgv1.ClusterSpecExternalClustersBarmanObjectStoreGoogleCredentialsApplicationCredentialsArgs

func TestBuildExternalClustersArgsEmitsSourceStore(t *testing.T) {
	props := &Properties{
		SourceSecret: &BackupSecret{
			Name: "postgres-source-credentials",
			Key:  "gcsCredentials",
		},
	}
	out := props.buildExternalClustersArgs(recoveryCluster(""))
	require.NotNil(t, out)
	arr, ok := out.(cnpgv1.ClusterSpecExternalClustersArray)
	require.True(t, ok)
	require.Len(t, arr, 1)
	ext, ok := arr[0].(*cnpgv1.ClusterSpecExternalClustersArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(testClusterName), ext.Name)
	store, ok := ext.BarmanObjectStore.(*cnpgv1.ClusterSpecExternalClustersBarmanObjectStoreArgs)
	require.True(t, ok)
	assert.Equal(
		t,
		pulumi.String("gs://pgbackup-devenv/logto"),
		store.DestinationPath,
	)
	creds, ok := store.GoogleCredentials.(*googleCredsArgs)
	require.True(t, ok)
	appCreds, ok := creds.ApplicationCredentials.(*appCredsArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("postgres-source-credentials"), appCreds.Name)
	assert.Equal(t, pulumi.String("gcsCredentials"), appCreds.Key)
}
