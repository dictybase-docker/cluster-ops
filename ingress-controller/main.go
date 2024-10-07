package main

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	nginxingress "github.com/pulumi/pulumi-kubernetes-ingress-nginx/sdk/go/kubernetes-ingress-nginx"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// Create an NGINX Ingress Controller
		_, err := nginxingress.NewIngressController(ctx, "nginx-ingress", &nginxingress.IngressControllerArgs{
			Controller: &nginxingress.ControllerArgs{
				PublishService: &nginxingress.ControllerPublishServiceArgs{
					Enabled: pulumi.Bool(true),
				},
			},
		})
		if err != nil {
			return err
		}

		return nil
	})
}
