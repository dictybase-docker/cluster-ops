package main

import (
	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	PR "github.com/IBM/fp-go/v2/pair"
	R "github.com/IBM/fp-go/v2/record"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// HelmValues renders the falcon-sensor chart values: node DaemonSet only,
// CID from Secret falcon-sensor-cid, the digest-pinned Artifact Registry
// image, and one toleration that matches every taint. A security sensor
// must run on every node, control plane and dedicated pools included.
//
// The chart sets falcon.cloud as FALCONCTL_OPT_CLOUD. Chart 1.37 ignores
// node.backend, so it is not set: the sensor selects eBPF itself.
// No resource limits: the chart advises against them in kernel fallback.
func HelmValues(c *FalconConfig) pulumi.Map {
	return pulumi.Map{
		"fullnameOverride": pulumi.String(releaseName),
		"falconSecret": pulumi.Map{
			enabledKey:   pulumi.Bool(true),
			"secretName": pulumi.String(secretName),
		},
		"falcon": falconValues(c),
		"node": pulumi.Map{
			enabledKey:    pulumi.Bool(true),
			"clusterName": pulumi.String(c.ClusterName),
			"image": pulumi.Map{
				"repository": pulumi.String(c.Image.Repository),
				"digest":     pulumi.String(c.Image.Digest),
				"pullPolicy": pulumi.String("IfNotPresent"),
			},
			"daemonset": pulumi.Map{
				"tolerations": pulumi.Array{
					pulumi.Map{"operator": pulumi.String("Exists")},
				},
			},
		},
		"container": pulumi.Map{enabledKey: pulumi.Bool(false)},
	}
}

// falconValues sets falcon.cloud, plus falcon.tags when the stack sets tags.
func falconValues(c *FalconConfig) pulumi.Map {
	return F.Pipe3(
		[]PR.Pair[string, string]{
			PR.MakePair(cloudKey, c.Cloud),
			PR.MakePair("tags", c.Tags),
		},
		A.Filter(F.Flow2(PR.Tail[string, string], S.IsNonEmpty)),
		A.Map(PR.MapTail[string](toPulumiString)),
		R.FromEntries[string, pulumi.Input],
	)
}

// SecretData is the Secret falcon-sensor-cid payload. The provisioning token
// key is present only when the stack sets provisioningToken.
func SecretData(c *FalconConfig) map[string]string {
	return F.Pipe2(
		[]PR.Pair[string, string]{
			PR.MakePair(cidKey, c.CID),
			PR.MakePair(tokenKey, c.ProvisioningToken),
		},
		A.Filter(F.Flow2(PR.Tail[string, string], S.IsNonEmpty)),
		R.FromEntries[string, string],
	)
}

const (
	enabledKey = "enabled"
	cloudKey   = "cloud"
)

func toPulumiString(v string) pulumi.Input {
	return pulumi.String(v)
}
