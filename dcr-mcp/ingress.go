package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// CreateIngress creates a new Ingress resource when ingress.enabled is true.
// Disabled by default so production clusters manage routing via the central Gateway.
func (m *DcrMcp) CreateIngress(
	ctx *pulumi.Context,
) error {
	cfg := m.Config
	if !cfg.Ingress.Enabled {
		return nil
	}
	_, err := networkingv1.NewIngress(
		ctx,
		fmt.Sprintf("%s-ingress", cfg.Name),
		&networkingv1.IngressArgs{
			Metadata: m.CreateIngressMetadata(),
			Spec:     m.CreateIngressSpec(),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create %s ingress: %w", cfg.Name, err)
	}
	return nil
}

// CreateIngressMetadata creates the metadata for an Ingress resource.
func (m *DcrMcp) CreateIngressMetadata() *metav1.ObjectMetaArgs {
	cfg := m.Config
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", cfg.Name)),
		Namespace: pulumi.String(cfg.Namespace),
		Labels: pulumi.StringMap{
			cfg.Ingress.Label.Name: pulumi.String(cfg.Ingress.Label.Value),
		},
	}
}

// CreateIngressSpec creates the specification for an Ingress resource.
func (m *DcrMcp) CreateIngressSpec() *networkingv1.IngressSpecArgs {
	cfg := m.Config
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(cfg.Ingress.Hosts),
				SecretName: pulumi.String(cfg.Ingress.TLSSecret),
			},
		},
		Rules: m.CreateIngressRuleArray(),
	}
}

// CreateIngressRuleArray creates an array of IngressRule objects based on the provided IngressConfig.
func (m *DcrMcp) CreateIngressRuleArray() networkingv1.IngressRuleArray {
	var rules networkingv1.IngressRuleArray
	cfg := m.Config

	for _, host := range cfg.Ingress.Hosts {
		paths := networkingv1.HTTPIngressPathArray{
			&networkingv1.HTTPIngressPathArgs{
				Path:     pulumi.String(cfg.Ingress.Path),
				PathType: pulumi.String("Prefix"),
				Backend: &networkingv1.IngressBackendArgs{
					Service: &networkingv1.IngressServiceBackendArgs{
						Name: pulumi.String(fmt.Sprintf("%s-api", cfg.Name)),
						Port: &networkingv1.ServiceBackendPortArgs{
							Number: pulumi.Int(cfg.Port),
						},
					},
				},
			},
		}

		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(host),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: paths,
			},
		})
	}

	return rules
}
