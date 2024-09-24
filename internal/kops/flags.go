package kops

import "github.com/urfave/cli/v2"

func DefineOtherFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "log-level",
			Aliases: []string{"l"},
			Usage:   "logging level, should be one of debug,info,warn,error",
			Value:   "error",
		},
		&cli.StringFlag{
			Name:    "provider",
			Aliases: []string{"cp"},
			Usage:   "cloud provider where the cluster will be hosted",
			Value:   "gce",
		},
		&cli.StringFlag{
			Name:    "image",
			Aliases: []string{"im"},
			Usage:   "compute image to be used for the vm",
			EnvVars: []string{"COMPUTE_IMAGE"},
			Value:   "ubuntu-os-cloud/ubuntu-2204-jammy-v20240829",
		},
	}
}

func DefineNodeFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "node-size",
			Aliases: []string{"ns"},
			Usage:   "The machine type for kubernetes node",
			Value:   "n1-custom-2-4096",
			EnvVars: []string{"NODE_MACHINE"},
		},
		&cli.IntFlag{
			Name:    "node-volume-size",
			Aliases: []string{"nvs"},
			Usage:   "size of the boot disk of kubernetes nodes in GB",
			Value:   100,
			EnvVars: []string{"NODE_DISK_SIZE"},
		},
		&cli.IntFlag{
			Name:    "node-count",
			Aliases: []string{"nc"},
			Usage:   "no of kubernetes worker node to create",
			Value:   4,
			EnvVars: []string{"TOTAL_NODES"},
		},
	}
}

func DefineMasterFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "master-size",
			Aliases: []string{"ms"},
			Usage:   "The machine type for kubernetes master",
			Value:   "n1-custom-4-8192",
			EnvVars: []string{"MASTER_MACHINE"},
		},
		&cli.StringFlag{
			Name:    "master-count",
			Aliases: []string{"mc"},
			Usage:   "no of kubernetes master to create",
			Value:   "1",
			EnvVars: []string{"TOTAL_MASTER"},
		},
		&cli.Int64Flag{
			Name:    "master-volume-size",
			Aliases: []string{"mvs"},
			Usage:   "size of the boot disk of kubernetes master in GB",
			Value:   75,
			EnvVars: []string{"MASTER_DISK_SIZE"},
		},
	}
}

func DefineKubernetesFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "kubernetes-version",
			Aliases: []string{"kv"},
			Usage:   "version of kubernetes cluster",
			EnvVars: []string{"KUBERNETES_VERSION"},
		},
		&cli.StringFlag{
			Name:    "kubeconfig",
			Aliases: []string{"kc"},
			Usage:   "kubernetes config file of k8s cluster",
			EnvVars: []string{"KUBECONFIG"},
		},
	}
}

func DefineCredentialsFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "credentials",
			Aliases: []string{"cred"},
			Usage:   "google cloud service account file in JSON format",
			EnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
		},
		&cli.StringFlag{
			Name:    "ssh-key",
			Aliases: []string{"sk"},
			Usage:   "public ssh key file that will be injected in the vm for login",
			EnvVars: []string{"SSH_KEY"},
			Value:   "credentials/k8sVM.pub",
		},
	}
}

func DefineClusterFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "cluster-name",
			Aliases:  []string{"cn"},
			Usage:    "name of the cluster",
			EnvVars:  []string{"KOPS_CLUSTER_NAME"},
			Required: true,
		},
		&cli.StringFlag{
			Name:     "state",
			Aliases:  []string{"st"},
			Usage:    "remote location of kops state",
			EnvVars:  []string{"KOPS_STATE_STORE"},
			Required: true,
		},
		&cli.StringFlag{
			Name:     "project-id",
			Aliases:  []string{"pi"},
			Usage:    "the google cloud project id",
			Required: true,
		},
		&cli.StringFlag{
			Name:    "zone",
			Aliases: []string{"z"},
			Usage:   "the google cloud zone",
			Value:   "us-central1-c",
		},
	}
}
