package main

import (
	"fmt"

	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type IngressConfig struct {
	TlsSecret       string
	Issuer       string
	Path         string
	BackendHosts []string
}

func (gs *GraphqlServer) CreateIngress(ctx *pulumi.Context, service pulumi.Resource) (*networkingv1.Ingress, error) {
	ingress, err := networkingv1.NewIngress(ctx, gs.Config.Name, &networkingv1.IngressArgs{
		Metadata: gs.IngressMetadata(),
		Spec:     gs.IngressSpec(),
	}, pulumi.DependsOn([]pulumi.Resource{service}))

	if err != nil {
		return nil, fmt.Errorf("failed to create graphql_server Ingress resource: %w", err)
	}

	return ingress, nil
}

// IngressMetadata returns the IngressMetadata for the Ingress resource.
func (gs *GraphqlServer) IngressMetadata() metav1.ObjectMetaPtrInput {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", gs.Config.Name)),
		Namespace: pulumi.String(gs.Config.Namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/issuer": pulumi.String(gs.Config.Ingress.Issuer),
		},
	}
}

// IngressSpec returns the IngressSpec for the Ingress resource.
func (gs *GraphqlServer) IngressSpec() networkingv1.IngressSpecPtrInput {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls:              gs.IngressTls(),
		Rules:            gs.IngressRules(),
	}
}

// IngressTls returns the TLS configuration for the Ingress resource.
func (gs *GraphqlServer) IngressTls() networkingv1.IngressTLSArrayInput {
	return networkingv1.IngressTLSArray{
		&networkingv1.IngressTLSArgs{
			SecretName: pulumi.String(gs.Config.Ingress.TlsSecret),
			Hosts:      pulumi.ToStringArray(gs.Config.Ingress.BackendHosts),
		},
	}
}

// IngressRules returns the IngressRules for the Ingress resource.
func (gs *GraphqlServer) IngressRules() networkingv1.IngressRuleArrayInput {
	var rules networkingv1.IngressRuleArray
	for _, h := range gs.Config.Ingress.BackendHosts {
		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(h),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: networkingv1.HTTPIngressPathArray{
					&networkingv1.HTTPIngressPathArgs{
						Path:     pulumi.String(gs.Config.Ingress.Path),
						PathType: pulumi.String("Prefix"),
						Backend: &networkingv1.IngressBackendArgs{
							Service: &networkingv1.IngressServiceBackendArgs{
								Name: pulumi.String(fmt.Sprintf("%s-api", gs.Config.Name)),
								Port: &networkingv1.ServiceBackendPortArgs{
									Number: pulumi.Int(gs.Config.Port),
								},
							},
						},
					},
				},
			},
		})
	}
	return rules
}
