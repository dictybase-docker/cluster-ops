// Package nsprobe verifies that the namespace-bootstrap stack has been
// deployed for the current cluster, and returns the namespace name from its
// exported output. Only namespace-bootstrap writes the shared namespaces;
// every other program probes.
package nsprobe

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Probe asserts three things, all failing fast at preview instead of deep
// inside a Helm release create:
//
//  1. The StackReference to namespace-bootstrap/<stack> fails when the
//     bootstrap stack was never deployed for this cluster.
//  2. Reading the exported output named by key ("operatorsNamespace" or
//     "appNamespace") fails when the bootstrap stack predates that export.
//  3. The live GetNamespace read fails when the namespace existed once but
//     was deleted afterwards.
//
// It returns the probe resource and the namespace name taken from the
// bootstrap stack itself — the single source of truth. Callers must wire the
// release/secret to the probe with pulumi.DependsOn so nothing runs ahead of
// it, and use the returned output as the target namespace.
func Probe(ctx *pulumi.Context, key string) (*corev1.Namespace, pulumi.StringInput, error) {
	ref, err := pulumi.NewStackReference(
		ctx,
		// Self-managed backends (GCS here) require the literal organization
		// segment — a two-segment project/stack name fails preview with
		// "organization name must be 'organization'".
		fmt.Sprintf("organization/namespace-bootstrap/%s", ctx.Stack()),
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"cannot reference the namespace-bootstrap stack: %w — "+
				"run 'just gcp-pulumi apply-namespaces' first (docs/pulumi-setup.md)",
			err,
		)
	}

	name := ref.GetStringOutput(pulumi.String(key))

	// GetNamespace takes an IDInput; StringOutput alone does not implement it.
	idOutput := name.ApplyT(
		func(s string) pulumi.ID { return pulumi.ID(s) },
	)
	nameID, ok := idOutput.(pulumi.IDOutput)
	if !ok {
		return nil, nil, fmt.Errorf(
			"unexpected output type converting namespace %q to an id: %T",
			key,
			idOutput,
		)
	}

	ns, err := corev1.GetNamespace(
		ctx,
		fmt.Sprintf("%s-probe", key),
		nameID,
		nil,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"namespace from the namespace-bootstrap export %q is missing on the cluster: %w — "+
				"run 'just gcp-pulumi apply-namespaces' first (docs/pulumi-setup.md)",
			key,
			err,
		)
	}
	return ns, name, nil
}
