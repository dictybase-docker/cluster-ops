package main

import (
	"fmt"
	"strconv"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	healthzPath         = "/healthz"
	initialDelaySeconds = 5
	probePeriodSeconds  = 10
)

var buildGraphQLEndpointFlags = F.Pipe1(
	S.IsNonEmpty,
	P.Fold(F.Constant1[string, []string](nil), graphqlEndpointFlagPair),
)

func graphqlEndpointFlagPair(endpoint string) []string {
	return []string{"--graphql-endpoint", endpoint}
}

func originFlagPair(origin string) []string {
	return []string{"--allowed-origins", origin}
}

func (m *DcrMcp) graphqlEndpointFlags() []string {
	return buildGraphQLEndpointFlags(m.Config.GraphQLEndpoint)
}

func (m *DcrMcp) allowedOriginsFlags() []string {
	return F.Pipe1(
		m.Config.AllowedOrigins,
		A.Chain(originFlagPair),
	)
}

func (m *DcrMcp) ContainerArgs() pulumi.StringArray {
	cfg := m.Config
	baseArgs := []string{cfg.Command}
	hostAndServerArgs := []string{
		"--host",
		cfg.Host,
		"--port",
		strconv.Itoa(cfg.Port),
		fmt.Sprintf("--stateless=%t", cfg.StatelessValue()),
		"--rate-limit",
		strconv.Itoa(cfg.RateLimit),
	}
	args := A.ArrayConcatAll(
		baseArgs,
		m.graphqlEndpointFlags(),
		hostAndServerArgs,
		m.allowedOriginsFlags(),
	)
	return pulumi.ToStringArray(args)
}

func (m *DcrMcp) ContainerPortArray() corev1.ContainerPortArray {
	cfg := m.Config
	return corev1.ContainerPortArray{
		&corev1.ContainerPortArgs{
			Name:          pulumi.String(fmt.Sprintf("%s-api", cfg.Name)),
			ContainerPort: pulumi.Int(cfg.Port),
			Protocol:      pulumi.String("TCP"),
		},
	}
}

func (m *DcrMcp) httpProbe() *corev1.ProbeArgs {
	return &corev1.ProbeArgs{
		HttpGet: &corev1.HTTPGetActionArgs{
			Path: pulumi.String(healthzPath),
			Port: pulumi.Int(m.Config.Port),
		},
		InitialDelaySeconds: pulumi.Int(initialDelaySeconds),
		PeriodSeconds:       pulumi.Int(probePeriodSeconds),
	}
}

func (m *DcrMcp) ContainerArray() corev1.ContainerArray {
	cfg := m.Config
	probe := m.httpProbe()
	return corev1.ContainerArray{
		&corev1.ContainerArgs{
			Name: pulumi.String(fmt.Sprintf("%s-container", cfg.Name)),
			Image: pulumi.String(
				fmt.Sprintf("%s:%s", cfg.Image.Name, cfg.Image.Tag),
			),
			Args:           m.ContainerArgs(),
			Ports:          m.ContainerPortArray(),
			ReadinessProbe: probe,
			LivenessProbe:  probe,
		},
	}
}
