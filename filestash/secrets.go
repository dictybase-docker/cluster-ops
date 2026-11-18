package main

import (
	"fmt"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateSecrets makes Secret filestash (ADMIN_PASSWORD, CONFIG_SECRET) and
// Secret filestash-users (the four MinIO key pairs) from the encrypted stack
// config. Both values stay Pulumi secrets in the state.
func (f *Filestash) CreateSecrets(ctx *pulumi.Context) ([]pulumi.Resource, error) {
	cfg := f.Config
	app, err := f.newSecret(ctx, "secret", cfg.SecretName, map[string]string{
		adminPasswordKey: cfg.Secrets.AdminPasswordHash,
		configSecretKey:  cfg.Secrets.ConfigSecret,
	})
	if err != nil {
		return nil, err
	}
	users, err := f.newSecret(ctx, "users", f.resourceName("users"),
		F.Pipe1(userRoles, A.Reduce(F.Bind1of3(addUserKeys)(cfg.Users), map[string]string{})))
	if err != nil {
		return nil, err
	}
	return []pulumi.Resource{app, users}, nil
}

// addUserKeys adds <role>AccessKey and <role>SecretKey to data.
func addUserKeys(users map[string]UserKeys, data map[string]string, role string) map[string]string {
	data[role+"AccessKey"] = users[role].AccessKey
	data[role+"SecretKey"] = users[role].SecretKey
	return data
}

func (f *Filestash) newSecret(
	ctx *pulumi.Context,
	suffix, name string,
	data map[string]string,
) (*corev1.Secret, error) {
	sec, err := corev1.NewSecret(ctx, f.resourceName(suffix), &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(f.Config.Namespace),
			Labels:    f.labels(),
		},
		Type: pulumi.String("Opaque"),
		// ToSecret keeps the StringMap type: the assertion cannot fail.
		StringData: pulumi.ToSecret(pulumi.ToStringMap(data)).(pulumi.StringMapOutput), //nolint:errcheck // see above
	})
	if err != nil {
		return nil, fmt.Errorf("error creating secret %s: %w", name, err)
	}
	return sec, nil
}
