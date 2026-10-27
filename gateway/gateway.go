package main

import (
	"fmt"

	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type routeListenerSpec struct {
	routeKey  string
	hosts     []string
	tlsSecret string
}

func (g *GatewayStack) createGateway(
	ctx *pulumi.Context,
	issuer pulumi.Resource,
) (*apiextensions.CustomResource, error) {
	cfg := g.Config
	gw, err := apiextensions.NewCustomResource(
		ctx,
		cfg.Name,
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String(gatewayNetworkingVersion),
			Kind:       pulumi.String("Gateway"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(cfg.Name),
				Namespace: pulumi.String(cfg.Namespace),
				Annotations: pulumi.StringMap{
					"cert-manager.io/issuer": pulumi.String(cfg.Issuer.Name),
				},
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": g.buildGatewaySpec(),
			},
		},
		pulumi.DependsOn([]pulumi.Resource{issuer}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gateway %s: %w", cfg.Name, err)
	}
	return gw, nil
}

func (g *GatewayStack) buildGatewaySpec() pulumi.Map {
	return pulumi.Map{
		"gatewayClassName": pulumi.String(g.Config.GatewayClassName),
		"listeners":        g.buildGatewayListeners(),
	}
}

func (g *GatewayStack) buildGatewayListeners() pulumi.Array {
	var listeners pulumi.Array
	seenHosts := make(map[string]bool)
	for _, rl := range g.enabledRouteListeners() {
		for idx, host := range rl.hosts {
			if seenHosts[host] {
				continue
			}
			seenHosts[host] = true
			listeners = append(
				listeners,
				buildHTTPListener(rl.routeKey, idx, host),
				buildHTTPSListener(rl.routeKey, idx, host, rl.tlsSecret),
			)
		}
	}
	return listeners
}

func buildHTTPListener(routeKey string, idx int, host string) pulumi.Map {
	return pulumi.Map{
		fieldName:  pulumi.String(fmt.Sprintf("http-%s-%d", routeKey, idx)),
		"hostname": pulumi.String(host),
		fieldPort:  pulumi.Int(80),
		"protocol": pulumi.String("HTTP"),
		"allowedRoutes": pulumi.Map{
			"namespaces": pulumi.Map{
				"from": pulumi.String("Same"),
			},
		},
	}
}

func buildHTTPSListener(routeKey string, idx int, host, tlsSecret string) pulumi.Map {
	return pulumi.Map{
		fieldName:  pulumi.String(fmt.Sprintf("https-%s-%d", routeKey, idx)),
		"hostname": pulumi.String(host),
		fieldPort:  pulumi.Int(443),
		"protocol": pulumi.String("HTTPS"),
		"allowedRoutes": pulumi.Map{
			"namespaces": pulumi.Map{
				"from": pulumi.String("Same"),
			},
		},
		"tls": pulumi.Map{
			"mode": pulumi.String("Terminate"),
			"certificateRefs": pulumi.Array{
				pulumi.Map{
					"kind":    pulumi.String("Secret"),
					fieldName: pulumi.String(tlsSecret),
				},
			},
		},
	}
}

func (g *GatewayStack) enabledRouteListeners() []routeListenerSpec {
	cfg := g.Config
	var specs []routeListenerSpec
	if cfg.Routes.Frontend.Enabled {
		specs = append(specs, routeListenerSpec{
			routeKey:  routeKeyFrontend,
			hosts:     cfg.Routes.Frontend.Hosts,
			tlsSecret: cfg.Routes.Frontend.TLSSecret,
		})
	}
	for _, named := range []struct {
		key string
		rt  ServiceRouteConfig
	}{
		{key: routeKeyGraphql, rt: cfg.Routes.Graphql},
		{key: routeKeyMinio, rt: cfg.Routes.Minio},
		{key: routeKeyLogto, rt: cfg.Routes.Logto},
	} {
		if named.rt.Enabled {
			specs = append(specs, routeListenerSpec{
				routeKey:  named.key,
				hosts:     named.rt.Hosts,
				tlsSecret: named.rt.TLSSecret,
			})
		}
	}
	return specs
}
