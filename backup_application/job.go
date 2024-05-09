package main

import (
	"github.com/dictybase-docker/cluster-ops/k8s"
	batchv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/batch/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func jobPodTemplate(args *specProperties) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: k8s.TemplateMetadata(args.app.jobName),
		Spec: &corev1.PodSpecArgs{
			RestartPolicy: pulumi.String("Never"),
			Containers:    createRepoContainerSpec(args),
			Volumes: k8s.CreateVolumeSpec(
				args.secretName,
				args.app.volumeName,
				[]*k8s.VolumeItemsProperties{
					{Key: "gcsbucket.credentials", Value: "credentials.json"},
				},
			),
		},
	}
}

func jobSpecArgs(args *specProperties) *batchv1.JobSpecArgs {
	return &batchv1.JobSpecArgs{
		Template: jobPodTemplate(args),
		Selector: k8s.SpecLabelSelector(args.app.jobName),
	}
}

func createJobSpec(args *specProperties) *batchv1.JobArgs {
	return &batchv1.JobArgs{
		Metadata: k8s.Metadata(args.namespace, args.app.jobName),
		Spec:     jobSpecArgs(args),
	}
}
