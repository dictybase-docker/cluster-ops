package main

// type containerSpecFn func(*specProperties) corev1.ContainerArray

/* func createPodTemplateSpec(
	args *specProperties,
	fn containerSpecFn,
) *corev1.PodTemplateSpecArgs {
	return &corev1.PodTemplateSpecArgs{
		Metadata: k8s.TemplateMetadata(args.app.jobName),
		Spec: &corev1.PodSpecArgs{
			RestartPolicy: pulumi.String("Never"),
			Containers:    fn(args),
			Volumes: k8s.CreateVolumeSpec(
				args.secretName,
				args.app.volumeName,
				[]*k8s.VolumeItemsProperties{
					{Key: "gcsbucket.credentials", Value: "credentials.json"},
				},
			),
		},
	}
} */

/* func createRepoJobSpec(args *specProperties) *batchv1.JobArgs {
	return &batchv1.JobArgs{
		Metadata: k8s.Metadata(args.namespace, args.app.jobName),
		Spec: batchv1.JobSpecArgs{
			Template: createPodTemplateSpec(args, createRepoContainerSpec),
			Selector: k8s.SpecLabelSelector(a
rgs.app.jobName),
		},
	}
} */

/* func createPostgresJobSpec(args *specProperties) *batchv1.CronJobArgs {
	pgProps := args.Postgresql
	return &batchv1.CronJobArgs{
		Metadata: k8s.Metadata(args.Namespace, pgProps.JobName),
		Spec: batchv1.CronJobSpecArgs{
			Schedule: pulumi.String(pgProps.Schedule),
			JobTemplate: &batchv1.JobTemplateSpecArgs{
				Spec: &batchv1.JobSpecArgs{
					Template: createPodTemplateSpec(
						args,
						postgresBackupContainerSpec,
					),
				},
			},
		},
	}
} */
