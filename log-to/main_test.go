package main

import (
	"reflect"
	"testing"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/require"
)

func TestContainerHasLogtoReadinessProbe(t *testing.T) {
	logto := NewLogto(&LogtoConfig{
		Name:      "logto",
		APIPort:   3001,
		AdminPort: 3002,
		Image: ImageConfig{
			Name: "svhd/logto",
			Tag:  "1.43.0",
		},
	})

	container, ok := logto.ContainerArray("logto-app")[0].(*corev1.ContainerArgs)
	require.True(t, ok)
	probe, ok := container.ReadinessProbe.(*corev1.ProbeArgs)
	require.True(t, ok)
	httpGet, ok := probe.HttpGet.(*corev1.HTTPGetActionArgs)
	require.True(t, ok)
	require.NotNil(t, httpGet.Path)
	require.Equal(t, "/api/status", reflect.ValueOf(httpGet.Path).Elem().String())
	port, ok := httpGet.Port.(pulumi.Int)
	require.True(t, ok)
	require.Equal(t, pulumi.Int(3001), port)
}
