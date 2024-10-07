package main

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (lt *Logto) CreateIngress(
	ctx *pulumi.Context,
	service pulumi.Resource,
) (*networkingv1.Ingress, error) {
	ingress, err := networkingv1.NewIngress(
		ctx,
		fmt.Sprintf("%s-ingress", lt.Config.Name),
		&networkingv1.IngressArgs{
			Metadata: lt.IngressMetadata(),
			Spec:     lt.IngressSpec(),
		},
		pulumi.DependsOn([]pulumi.Resource{service}),
	)

	if err != nil {
		return nil, fmt.Errorf(
			"failed to create Logto Ingress resource: %w",
			err,
		)
	}

	return ingress, nil
}

func (lt *Logto) IngressMetadata() metav1.ObjectMetaPtrInput {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(fmt.Sprintf("%s-ingress", lt.Config.Name)),
		Namespace: pulumi.String(lt.Config.Namespace),
		Annotations: pulumi.StringMap{
			"cert-manager.io/issuer": pulumi.String(lt.Config.Ingress.Issuer),
		},
	}
}

func (lt *Logto) IngressSpec() networkingv1.IngressSpecPtrInput {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls:              lt.IngressTLS(),
		Rules:            lt.IngressRules(),
	}
}

func (lt *Logto) IngressTLS() networkingv1.IngressTLSArrayInput {
	return networkingv1.IngressTLSArray{
		&networkingv1.IngressTLSArgs{
			SecretName: pulumi.String(lt.Config.Ingress.TLSSecret),
			Hosts:      pulumi.ToStringArray(lt.Config.Ingress.BackendHosts),
		},
	}
}

func (lt *Logto) IngressRules() networkingv1.IngressRuleArrayInput {
	var rules networkingv1.IngressRuleArray
	for _, h := range lt.Config.Ingress.BackendHosts {
		rules = append(rules, &networkingv1.IngressRuleArgs{
			Host: pulumi.String(h),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: networkingv1.HTTPIngressPathArray{
					&networkingv1.HTTPIngressPathArgs{
						Path:     pulumi.String("/"),
						PathType: pulumi.String("Prefix"),
						Backend: &networkingv1.IngressBackendArgs{
							Service: &networkingv1.IngressServiceBackendArgs{
								Name: pulumi.String(
									fmt.Sprintf("%s-api", lt.Config.Name),
								),
								Port: &networkingv1.ServiceBackendPortArgs{
									Number: pulumi.Int(lt.Config.APIPort),
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
