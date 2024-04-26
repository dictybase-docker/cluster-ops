package k8s

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func Container(cfg *config.Config) pulumi.StringInput {
	return pulumi.String(
		fmt.Sprintf(
			"%s-container",
			cfg.Require("name"),
		),
	)
}

func Image(cfg *config.Config) pulumi.StringPtrInput {
	return pulumi.String(
		fmt.Sprintf(
			"%s:%s",
			cfg.Require("image"),
			cfg.Require("tag"),
		),
	)
}

func TemplateMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name: pulumi.String(fmt.Sprintf("%s-template", name)),
		Labels: pulumi.StringMap{
			"app": pulumi.String(
				fmt.Sprintf("%s-pulumi-template", name),
			),
		},
	}

}

func Metadata(cfg *config.Config, name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(cfg.Require("namespace")),
		Labels: pulumi.StringMap{
			"app": pulumi.String(fmt.Sprintf("%s-pulumi", name)),
		},
	}
}
