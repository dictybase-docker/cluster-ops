package k8s

import (
	"fmt"

	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func Container(name string) pulumi.StringInput {
	return pulumi.String(fmt.Sprintf("%s-container", name))
}

func Image(image, tag string) pulumi.StringPtrInput {
	return pulumi.String(fmt.Sprintf("%s:%s", image, tag))
}

func TemplateMetadata(name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name: pulumi.String(fmt.Sprintf("%s-template", name)),
		Labels: pulumi.StringMap{
			"app": pulumi.String(name),
		},
	}

}

func Metadata(namespace, name string) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(name),
		Namespace: pulumi.String(namespace),
		Labels: pulumi.StringMap{
			"app": pulumi.String(fmt.Sprintf("%s-pulumi", name)),
		},
	}
}
