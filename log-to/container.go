package main

import (
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) ContainerEnvArgsArray() corev1.EnvVarArray {
	return corev1.EnvVarArray{
		&corev1.EnvVarArgs{
			Name: pulumi.String("DB_URL"),
			Value: pulumi.String(lt.Config.Database.User),
    },
		&corev1.EnvVarArgs{
			Name: pulumi.String("PGPASSWORD"),
			Value: pulumi.String(lt.Config.Database.Password),
    },
		&corev1.EnvVarArgs{
			Name: pulumi.String("ENDPOINT"),
			Value: pulumi.String(lt.Config.Endpoint),
    },
		&corev1.EnvVarArgs{
			Name: pulumi.String("TRUST_PROXY_HEADER"),
			Value: pulumi.String("1"),
    },
  }
}
