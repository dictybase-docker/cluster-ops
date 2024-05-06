package k8s

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

	return corev1.EnvVarArgs{
		Name: pulumi.String(name),
		ValueFrom: corev1.EnvVarSourceArgs{
			SecretKeyRef: CreateSecretKeySelector(key, secret),
		},
	}
}

func CreateEnvVar(name, value string) corev1.EnvVarArgs {
	return corev1.EnvVarArgs{
		Name:  pulumi.String(name),
		Value: pulumi.String(value),
	}
}

func CreateSecretKeySelector(key, secret string) corev1.SecretKeySelectorArgs {
	return corev1.SecretKeySelectorArgs{
		Name: pulumi.StringPtr(secret),
		Key:  pulumi.String(key),
	}
}
