package main

import (
	"fmt"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/apiextensions"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func (g *GatewayStack) createHTTPRoutes(
	ctx *pulumi.Context,
	gw pulumi.Resource,
) error {
	if err := g.createFrontendHTTPRoute(ctx, gw); err != nil {
		return err
	}
	for _, named := range []struct {
		key string
		rt  ServiceRouteConfig
	}{
		{key: routeKeyGraphql, rt: g.Config.Routes.Graphql},
		{key: routeKeyMinio, rt: g.Config.Routes.Minio},
		{key: routeKeyLogto, rt: g.Config.Routes.Logto},
		{key: routeKeyMcp, rt: g.Config.Routes.Mcp},
	} {
		if err := g.createServiceHTTPRoute(ctx, gw, named.key, named.rt); err != nil {
			return err
		}
	}
	return nil
}

func (g *GatewayStack) createFrontendHTTPRoute(
	ctx *pulumi.Context,
	gw pulumi.Resource,
) error {
	fe := g.Config.Routes.Frontend
	if !fe.Enabled {
		return nil
	}
	rules := F.Pipe1(
		fe.Services,
		A.Map(buildFrontendRule),
	)
	return g.registerHTTPRoute(ctx, gw, "frontend-route", fe.Hosts, toPulumiArray(rules))
}

func (g *GatewayStack) createServiceHTTPRoute(
	ctx *pulumi.Context,
	gw pulumi.Resource,
	routeKey string,
	rt ServiceRouteConfig,
) error {
	if !rt.Enabled {
		return nil
	}
	routeName := fmt.Sprintf("%s-route", routeKey)
	rules := pulumi.Array{
		buildPathPrefixRule(rt.Path, rt.ServiceName, rt.ServicePort),
	}
	return g.registerHTTPRoute(ctx, gw, routeName, rt.Hosts, rules)
}

func (g *GatewayStack) registerHTTPRoute(
	ctx *pulumi.Context,
	gw pulumi.Resource,
	routeName string,
	hosts []string,
	rules pulumi.Array,
) error {
	cfg := g.Config
	_, err := apiextensions.NewCustomResource(
		ctx,
		routeName,
		&apiextensions.CustomResourceArgs{
			ApiVersion: pulumi.String(gatewayNetworkingVersion),
			Kind:       pulumi.String("HTTPRoute"),
			Metadata: &metav1.ObjectMetaArgs{
				Name:      pulumi.String(routeName),
				Namespace: pulumi.String(cfg.Namespace),
			},
			OtherFields: kubernetes.UntypedArgs{
				"spec": pulumi.Map{
					"parentRefs": pulumi.Array{
						pulumi.Map{
							fieldName:      pulumi.String(cfg.Name),
							fieldNamespace: pulumi.String(cfg.Namespace),
						},
					},
					"hostnames": pulumi.ToStringArray(hosts),
					"rules":     rules,
				},
			},
		},
		pulumi.DependsOn([]pulumi.Resource{gw}),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTPRoute %s: %w", routeName, err)
	}
	return nil
}

func buildFrontendRule(svc FrontendServiceBackend) pulumi.Input {
	return buildPathPrefixRule(svc.Path, svc.Name, svc.Port)
}

func buildPathPrefixRule(path, serviceName string, port int) pulumi.Map {
	return pulumi.Map{
		"matches": pulumi.Array{
			pulumi.Map{
				"path": pulumi.Map{
					"type":  pulumi.String("PathPrefix"),
					"value": pulumi.String(path),
				},
			},
		},
		"backendRefs": pulumi.Array{
			pulumi.Map{
				fieldName: pulumi.String(serviceName),
				fieldPort: pulumi.Int(port),
			},
		},
	}
}

func toPulumiArray(inputs []pulumi.Input) pulumi.Array {
	arr := make(pulumi.Array, len(inputs))
	copy(arr, inputs)
	return arr
}
