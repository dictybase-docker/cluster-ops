package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// createIngress creates a new Ingress resource with the given configuration.
func (gs *GraphqlServer) CreateIngress(
	ctx *pulumi.Context,
) error {
	config := gs.Config
	_, err := networkingv1.NewIngress(
		ctx,
		fmt.Sprintf("%s-ingress", config.Name),
		&networkingv1.IngressArgs{
			Metadata: gs.CreateIngressMetadata(),
			Spec:     gs.CreateIngressSpec(),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to create %s ingress: %w", config.Name, err)
	}
	return nil
}

// CreateIngressMetadata creates the metadata for an Ingress resource.
func (gs *GraphqlServer) CreateIngressMetadata() *metav1.ObjectMetaArgs {
  config := gs.Config
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", config.Name)),
		Namespace: pulumi.String(config.Namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/cluster-issuer": pulumi.String(config.Ingress.Issuer),
		},
	}
}

// CreateIngressSpec creates the specification for an Ingress resource.
func (gs *GraphqlServer) CreateIngressSpec() *networkingv1.IngressSpecArgs {
  config := gs.Config
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(config.Ingress.Hosts),
				SecretName: pulumi.String(config.Ingress.TLSSecret),
			},
		},
		Rules: gs.CreateIngressRuleArray(),
	}
}

// CreateIngressRuleArray creates an array of IngressRule objects based on the provided IngressConfig.
func (gs *GraphqlServer) CreateIngressRuleArray(
) networkingv1.IngressRuleArray {
	var rules networkingv1.IngressRuleArray
  config := gs.Config

	for _, host := range config.Ingress.Hosts {
		var paths networkingv1.HTTPIngressPathArray
			paths = append(paths, &networkingv1.HTTPIngressPathArgs{
				Path:     pulumi.String(config.Ingress.Path),
				PathType: pulumi.String("Prefix"),
				Backend: &networkingv1.IngressBackendArgs{
					Service: &networkingv1.IngressServiceBackendArgs{
						Name: pulumi.String(fmt.Sprintf("%s-api", config.Name)),
						Port: &networkingv1.ServiceBackendPortArgs{
							Number: pulumi.Int(config.Port),
						},
					},
				},
			})

		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(host),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: paths,
			},
		})
	}

	return rules
}
