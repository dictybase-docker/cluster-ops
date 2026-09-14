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

// Plugin constants — single-writer house invariants.
const (
	// PluginStackProject is the Pulumi project that deploys the
	// plugin-barman-cloud Helm chart.
	PluginStackProject = "cnpg-backup-plugin"
	// PluginDeploymentName is the Helm release and Deployment name; the
	// deploy-backup-plugin recipe verifies it is live after applying.
	PluginDeploymentName = "plugin-barman-cloud"
	// PluginNamespace is the operator namespace the plugin lives in.
	PluginNamespace = "operators"
)

// ProbePlugin asserts that the cnpg-backup-plugin stack was deployed for
// this cluster, failing the preview otherwise, and returns the stack
// reference so callers can add it to DependsOn. A bare-name live deployment
// read is not used here — namespaced resources cannot be read unambiguously
// by name alone, and the plugin stack being present is the contract: the
// deploy-backup-plugin recipe verifies the rollout right after applying.
func ProbePlugin(ctx *pulumi.Context) (*pulumi.StackReference, error) {
	ref, err := pulumi.NewStackReference(
		ctx,
		fmt.Sprintf("organization/%s/%s", PluginStackProject, ctx.Stack()),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"cannot reference the cnpg-backup-plugin stack: %w — "+
				"run 'just postgres deploy-backup-plugin' first (docs/reference/postgres/backup-plugin.md)",
			err,
		)
	}
	return ref, nil
}
