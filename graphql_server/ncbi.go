package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateNcbiSecret creates the Secret that holds the NCBI identity values of
// the publication fetch. The Kubernetes SDK marks the Secret data as a Pulumi
// secret, so the values stay masked in previews and state. The install test
// asserts this.
func (gs *GraphqlServer) CreateNcbiSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	ncbi := gs.Config.Secrets.Ncbi
	secret, err := corev1.NewSecret(ctx, ncbi.Name, &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(ncbi.Name),
			Namespace: pulumi.String(gs.Config.Namespace),
		},
		StringData: pulumi.StringMap{
			ncbi.Keys.APIKey: pulumi.String(ncbi.Values.APIKey),
			ncbi.Keys.Email:  pulumi.String(ncbi.Values.Email),
			ncbi.Keys.Tool:   pulumi.String(ncbi.Values.Tool),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating graphql ncbi secret: %w", err)
	}
	return secret, nil
}

// ncbiEnvVars references the NCBI Secret from the container environment. The
// server reads NCBI_API_KEY, NCBI_EMAIL and NCBI_TOOL itself. Every item is
// required, so a pod does not start without one of them. It returns nothing
// for a stack without the NCBI Secret.
func (gs *GraphqlServer) ncbiEnvVars() corev1.EnvVarArray {
	if gs.NcbiSecret == nil {
		return nil
	}
	keys := gs.Config.Secrets.Ncbi.Keys
	var envVarArray corev1.EnvVarArray
	for _, envVar := range []struct {
		name string
		key  string
	}{
		{"NCBI_API_KEY", keys.APIKey},
		{"NCBI_EMAIL", keys.Email},
		{"NCBI_TOOL", keys.Tool},
	} {
		envVarArray = append(envVarArray, &corev1.EnvVarArgs{
			Name: pulumi.String(envVar.name),
			ValueFrom: &corev1.EnvVarSourceArgs{
				SecretKeyRef: &corev1.SecretKeySelectorArgs{
					Name:     gs.NcbiSecret.Metadata.Name(),
					Key:      pulumi.String(envVar.key),
					Optional: pulumi.BoolPtr(false),
				},
			},
		})
	}
	return envVarArray
}
