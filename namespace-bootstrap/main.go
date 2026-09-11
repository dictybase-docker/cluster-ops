package main

import (
	"errors"
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

// Config mirrors namespace-bootstrap/Pulumi.<stack>.yaml properties. Both
// namespaces are required — an empty value means the stack file is malformed
// and fails fast at preview instead of creating a nameless namespace.
type Config struct {
	OperatorNamespace string
	AppNamespace      string
}

func readConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	cfg := &Config{}
	if err := conf.TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read namespace-bootstrap config: %w", err)
	}
	if cfg.OperatorNamespace == "" {
		return nil, errors.New(
			"properties.operatorNamespace is empty — it is required in every Pulumi.<stack>.yaml",
		)
	}
	if cfg.AppNamespace == "" {
		return nil, errors.New(
			"properties.appNamespace is empty — it is required in every Pulumi.<stack>.yaml",
		)
	}
	return cfg, nil
}

// newNamespace creates a bare namespace: name only, no labels. Labels and
// quotas, if ever needed, belong to a dedicated policy stack, not here.
func newNamespace(ctx *pulumi.Context, name string) (*corev1.Namespace, error) {
	ns, err := corev1.NewNamespace(ctx, name, &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name: pulumi.String(name),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create namespace %s: %w", name, err)
	}
	return ns, nil
}

// run is the only writer of the shared namespaces. Operator programs
// (cloudnative-pg-operator, arangodb-operator) probe this stack via
// StackReference + a live GetNamespace read instead of creating namespaces
// themselves, and backup_secrets creates no namespaces.
func run(ctx *pulumi.Context) error {
	cfg, err := readConfig(ctx)
	if err != nil {
		return err
	}

	operators, err := newNamespace(ctx, cfg.OperatorNamespace)
	if err != nil {
		return err
	}
	app, err := newNamespace(ctx, cfg.AppNamespace)
	if err != nil {
		return err
	}

	// Consumed by operator programs through
	// pulumi.NewStackReference("namespace-bootstrap/<stack>").GetOutput(...).
	ctx.Export("operatorsNamespace", operators.Metadata.Name())
	ctx.Export("appNamespace", app.Metadata.Name())
	return nil
}

func main() {
	pulumi.Run(run)
}
