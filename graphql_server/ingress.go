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
			Metadata: createIngressMetadata(config.Name, config.Namespace, config.Ingress.Issuer),
			Spec:     createIngressSpec(config.Ingress),
		},
	)

	if err != nil {
		return fmt.Errorf("failed to create %s ingress: %w", config.Name, err)
	}
	return nil
}

// createIngressMetadata creates the metadata for an Ingress resource.
func createIngressMetadata(
	name string,
	namespace string,
	issuer string,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", name)),
		Namespace: pulumi.String(namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/cluster-issuer": pulumi.String(issuer),
		},
	}
}

// createIngressSpec creates the specification for an Ingress resource.
func createIngressSpec(config IngressConfig) *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(config.Hosts),
				SecretName: pulumi.String(config.TLSSecret),
			},
		},
		Rules: createIngressRuleArray(config),
	}
}

// createIngressRuleArray creates an array of IngressRule objects based on the provided IngressConfig.
func createIngressRuleArray(
	config IngressConfig,
) networkingv1.IngressRuleArray {
	var rules networkingv1.IngressRuleArray

	for _, host := range config.Hosts {
		var paths networkingv1.HTTPIngressPathArray
		for _, service := range config.Services {
			paths = append(paths, &networkingv1.HTTPIngressPathArgs{
				Path:     pulumi.String(service.Path),
				PathType: pulumi.String("Prefix"),
				Backend: &networkingv1.IngressBackendArgs{
					Service: &networkingv1.IngressServiceBackendArgs{
						Name: pulumi.String(service.Name),
						Port: &networkingv1.ServiceBackendPortArgs{
							Number: pulumi.Int(service.Port),
						},
					},
				},
			})
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
