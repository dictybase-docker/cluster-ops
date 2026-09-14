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
		Name: testClusterName,
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

type externalClustersPluginArgs = cnpgv1.ClusterSpecExternalClustersPluginArgs

func TestBuildExternalClustersArgsEmitsPluginSource(t *testing.T) {
	props := &Properties{}
	out := props.buildExternalClustersArgs(recoveryCluster(""))
	require.NotNil(t, out)
	arr, ok := out.(cnpgv1.ClusterSpecExternalClustersArray)
	require.True(t, ok)
	require.Len(t, arr, 1)
	ext, ok := arr[0].(*cnpgv1.ClusterSpecExternalClustersArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(testClusterName), ext.Name)
	require.NotNil(t, ext.Plugin)
	plugin, ok := ext.Plugin.(*externalClustersPluginArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(BarmanCloudPluginName), plugin.Name)
	params, ok := plugin.Parameters.(pulumi.StringMap)
	require.True(t, ok, "Parameters should be a concrete pulumi.StringMap")
	assert.Equal(
		t,
		pulumi.String("logto-source-logto-store"),
		params["barmanObjectName"],
	)
	assert.Equal(t, pulumi.String(testClusterName), params["serverName"])
	assert.Nil(t, ext.BarmanObjectStore)
}

func TestSourceObjectStoreNameDoesNotCollideWithTarget(t *testing.T) {
	assert.Equal(t, "logto-store", objectStoreNameFor("logto"))
	assert.Equal(t, "logto-source-logto-store", sourceObjectStoreNameFor("logto", "logto"))
	assert.NotEqual(
		t,
		objectStoreNameFor("logto"),
		sourceObjectStoreNameFor("logto", "logto"),
	)
}

func TestBuildClusterSpecEmitsPluginWALArchiver(t *testing.T) {
	props := &Properties{}
	cluster := Cluster{Name: "logto"}
	spec := props.buildClusterSpec(cluster, "logto-store")
	require.NotNil(t, spec)
	arr, ok := spec.Plugins.(cnpgv1.ClusterSpecPluginsArray)
	require.True(t, ok)
	require.Len(t, arr, 1)
	plugin, ok := arr[0].(*cnpgv1.ClusterSpecPluginsArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(BarmanCloudPluginName), plugin.Name)
	walArchiver, ok := plugin.IsWALArchiver.(pulumi.Bool)
	require.True(t, ok, "IsWALArchiver should be a concrete pulumi.Bool")
	assert.True(t, bool(walArchiver))
	params, ok := plugin.Parameters.(pulumi.StringMap)
	require.True(t, ok, "Parameters should be a concrete pulumi.StringMap")
	assert.Equal(t, pulumi.String("logto-store"), params["barmanObjectName"])
	assert.Nil(t, spec.Backup)
}

func TestBuildScheduledBackupArgsUsesPluginMethod(t *testing.T) {
	props := &Properties{}
	args := props.buildScheduledBackupArgs(Cluster{Name: "logto"})
	require.NotNil(t, args)
	require.NotNil(t, args.Spec)
	spec, ok := args.Spec.(*cnpgv1.ScheduledBackupSpecArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String("plugin"), spec.Method)
	require.NotNil(t, spec.PluginConfiguration)
	plugin, ok := spec.PluginConfiguration.(*cnpgv1.ScheduledBackupSpecPluginConfigurationArgs)
	require.True(t, ok)
	assert.Equal(t, pulumi.String(BarmanCloudPluginName), plugin.Name)
	assert.Equal(t, pulumi.Bool(true), spec.Immediate)
}

func TestObjectStoreNameForSuffixesStore(t *testing.T) {
	assert.Equal(t, "logto-store", objectStoreNameFor("logto"))
	assert.Equal(t, "pgbackup-source-store", objectStoreNameFor("pgbackup-source"))
}
