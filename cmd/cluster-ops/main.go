package main

import (
	"fmt"
	"os"

	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/dictybase-docker/cluster-ops/internal/gcp"
	"github.com/dictybase-docker/cluster-ops/internal/kops"
	"github.com/dictybase-docker/cluster-ops/internal/util"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "cluster-ops",
		Usage: "Unified cluster lifecycle management",
		Commands: []*cli.Command{
			kopsCommand(),
			validateCommand(),
			bucketCommand(),
			igCommand(),
			envCommand(),
			apiCommand(),
			saCommand(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

// ---------- kops ----------

func kopsCommand() *cli.Command {
	return &cli.Command{
		Name:  "kops",
		Usage: "Manage kops cluster lifecycle",
		Subcommands: []*cli.Command{
			{
				Name:   "create-config",
				Usage:  "Generate cluster manifest (no VMs provisioned)",
				Action: kops.CreateCluster,
				Flags:  kopsCLIFlags(),
			},
			{
				Name:   "update",
				Usage:  "Apply pending cluster changes (version-aware dispatch)",
				Action: kops.UpdateCluster,
			},
			{
				Name:  "delete",
				Usage: "Teardown a cluster (dry-run by default, --yes to execute)",
				Flags: kopsDeleteFlags(),
				Action: func(cltx *cli.Context) error {
					// Bridge: the unified CLI uses --cluster-name / --state / --project-id
					// but delete.go expects cluster-name / state / project-id / yes / non-interactive.
					return kops.DeleteCluster(cltx)
				},
			},
			{
				Name:   "recreate",
				Usage:  "Recreate cluster from saved manifest",
				Action: kops.RecreateCluster,
				Flags:  kopsRecreateFlags(),
			},
		},
	}
}

func kopsCLIFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "cluster-name", Aliases: []string{"c"},
			EnvVars: []string{"KOPS_CLUSTER_NAME"}, Required: true,
		},
		&cli.StringFlag{
			Name: "state", Aliases: []string{"s"},
			EnvVars: []string{"KOPS_STATE_STORE"}, Required: true,
		},
		&cli.StringFlag{
			Name: "project-id", Aliases: []string{"p"},
			Required: true,
		},
		&cli.StringFlag{
			Name: "zones", Aliases: []string{"z"},
			EnvVars: []string{"NODE_ZONES"}, Value: "us-central1-c",
		},
		&cli.StringFlag{
			Name: "node-size", Aliases: []string{"ns"},
			EnvVars: []string{"NODE_MACHINE"}, Value: "n1-custom-2-4096",
		},
		&cli.IntFlag{
			Name: "node-count", Aliases: []string{"nc"},
			EnvVars: []string{"TOTAL_NODES"}, Value: 4,
		},
		&cli.IntFlag{
			Name: "node-volume-size", Aliases: []string{"nvs"},
			EnvVars: []string{"NODE_DISK_SIZE"}, Value: 100,
		},
		&cli.StringFlag{
			Name: "control-plane-size", Aliases: []string{"cps"},
			EnvVars: []string{"MASTER_MACHINE"}, Value: "n1-custom-4-8192",
		},
		&cli.StringFlag{
			Name: "control-plane-count", Aliases: []string{"cpc"},
			EnvVars: []string{"TOTAL_MASTER"}, Value: "1",
		},
		&cli.IntFlag{
			Name: "control-plane-volume-size", Aliases: []string{"cpvs"},
			EnvVars: []string{"MASTER_DISK_SIZE"}, Value: 75,
		},
		&cli.StringFlag{
			Name: "control-plane-zones", Aliases: []string{"cpz"},
			EnvVars: []string{"CONTROL_PLANE_ZONES"},
		},
		&cli.StringFlag{
			Name: "topology", Aliases: []string{"t"},
			EnvVars: []string{"TOPOLOGY"}, Value: "public",
		},
		&cli.StringFlag{
			Name: "networking", Aliases: []string{"nw"},
			EnvVars: []string{"NETWORKING"}, Value: "cilium-etcd",
		},
		&cli.StringFlag{
			Name: "admin-access", Aliases: []string{"aa"},
			EnvVars: []string{"API_ACCESS_CIDR"},
		},
		&cli.StringFlag{
			Name: "kubernetes-version", Aliases: []string{"kv"},
			EnvVars: []string{"KUBERNETES_VERSION"},
		},
		&cli.StringFlag{
			Name: "ssh-key", Aliases: []string{"sk"},
			EnvVars: []string{"SSH_KEY"}, Value: "credentials/k8sVM.pub",
		},
		&cli.StringFlag{
			Name: "provider", Aliases: []string{"pr"},
			Value: "gce",
		},
		&cli.StringFlag{
			Name: "image", Aliases: []string{"im"},
			EnvVars: []string{"COMPUTE_IMAGE"}, Value: "ubuntu-os-cloud/ubuntu-2204-jammy-v20240829",
		},
		&cli.StringFlag{
			Name: "kubeconfig", Aliases: []string{"kc"},
			EnvVars: []string{"KUBECONFIG"},
		},
		&cli.StringFlag{
			Name: "credentials", Aliases: []string{"cred"},
			EnvVars: []string{"GOOGLE_APPLICATION_CREDENTIALS"},
		},
	}
}

func kopsDeleteFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "cluster-name", Aliases: []string{"c"},
			EnvVars: []string{"KOPS_CLUSTER_NAME"}, Required: true,
		},
		&cli.StringFlag{
			Name: "state", Aliases: []string{"s"},
			EnvVars: []string{"KOPS_STATE_STORE"}, Required: true,
		},
		&cli.StringFlag{
			Name: "project-id", Aliases: []string{"p"},
			EnvVars: []string{"PROJECT_ID"},
		},
		&cli.BoolFlag{
			Name: "yes",
			Usage: "Execute teardown (without this flag: dry-run only)",
		},
		&cli.BoolFlag{
			Name: "non-interactive",
			Usage: "Skip confirmation prompt (for CI)",
		},
	}
}

func kopsRecreateFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "cluster-name", Aliases: []string{"c"},
			EnvVars: []string{"KOPS_CLUSTER_NAME"}, Required: true,
		},
		&cli.StringFlag{
			Name: "state", Aliases: []string{"s"},
			EnvVars: []string{"KOPS_STATE_STORE"}, Required: true,
		},
		&cli.StringFlag{
			Name: "project-id", Aliases: []string{"p"},
			Required: true,
		},
		&cli.StringFlag{
			Name: "manifest", Aliases: []string{"m"},
			Usage: "Path to saved cluster manifest",
		},
		&cli.StringFlag{
			Name: "template-dir", Aliases: []string{"d"},
			Value: "config/kops/instancegroups",
			Usage: "Directory containing InstanceGroup templates",
		},
	}
}

// ---------- validate ----------

func validateCommand() *cli.Command {
	return &cli.Command{
		Name:  "validate",
		Usage: "Validate cluster health and topology",
		Subcommands: []*cli.Command{
			{
				Name:   "ha",
				Usage:  "Validate HA production topology (7 checks)",
				Action: validateHAAction,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "kubeconfig", Aliases: []string{"k"},
						EnvVars: []string{"KUBECONFIG"},
					},
				},
			},
		},
	}
}

func validateHAAction(cltx *cli.Context) error {
	checks, err := custodian.ValidateHA(cltx.String("kubeconfig"))
	if err != nil {
		return err
	}
	printHAChecks(checks)
	return nil
}

func printHAChecks(checks []custodian.HACheck) {
	pass, fail, warn := 0, 0, 0
	for _, c := range checks {
		fmt.Printf("%-30s %-6s %s\n", c.Name, c.Status, c.Message)
		switch c.Status {
		case "pass":
			pass++
		case "fail", "error":
			fail++
		case "warn":
			warn++
		}
	}
	fmt.Printf("\nPass: %d  Warn: %d  Fail: %d  Total: %d\n", pass, warn, fail, len(checks))
}

// ---------- bucket ----------

func bucketCommand() *cli.Command {
	return &cli.Command{
		Name:  "bucket",
		Usage: "Manage GCS state bucket",
		Subcommands: []*cli.Command{
			{
				Name:   "create",
				Usage:  "Create and harden a GCS state bucket",
				Action: gcp.CreateKopsStateBucket,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "project", Aliases: []string{"p"},
						Required: true,
					},
					&cli.StringFlag{
						Name: "bucket", Aliases: []string{"b"},
						Required: true,
					},
					&cli.IntFlag{
						Name: "max-versions", Aliases: []string{"m"},
						Value: 6,
					},
					&cli.StringFlag{
						Name: "region", Value: "US",
					},
					&cli.BoolFlag{
						Name: "harden", Value: true,
						Usage: "Enable UBLA, PAP, and versioning",
					},
				},
			},
		},
	}
}

// ---------- ig (InstanceGroups) ----------

func igCommand() *cli.Command {
	return &cli.Command{
		Name:  "ig",
		Usage: "Manage kops InstanceGroups",
		Subcommands: []*cli.Command{
			{
				Name:   "apply",
				Usage:  "Apply checked-in InstanceGroup templates",
				Action: kops.ApplyInstanceGroups,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "template-dir", Aliases: []string{"d"},
						Value: "config/kops/instancegroups",
					},
					&cli.BoolFlag{
						Name: "dry-run",
						Usage: "Render and validate without applying",
					},
				},
			},
			{
				Name:   "list",
				Usage:  "List all InstanceGroups",
				Action: kops.ListInstanceGroups,
			},
		},
	}
}

// ---------- env ----------

func envCommand() *cli.Command {
	return &cli.Command{
		Name:  "env",
		Usage: "Manage per-cluster environment",
		Subcommands: []*cli.Command{
			{
				Name:      "set-cred",
				Usage:     "Set GOOGLE_APPLICATION_CREDENTIALS in a per-cluster env file",
				ArgsUsage: "<env> <cluster> <key-path>",
				Action: func(cltx *cli.Context) error {
					if cltx.NArg() != 3 {
						return fmt.Errorf("usage: cluster-ops env set-cred <env> <cluster> <key-path>")
					}
					envFile := fmt.Sprintf(".env.%s.%s", cltx.Args().Get(0), cltx.Args().Get(1))
					return util.SetCredential(envFile, cltx.Args().Get(2))
				},
			},
			{
				Name:  "set-var",
				Usage: "Set an environment variable in .envrc and reload",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Aliases: []string{"n"}, Required: true},
					&cli.StringFlag{Name: "value", Aliases: []string{"v"}, Required: true},
				},
				Action: util.SetEnvironmentalVariable,
			},
		},
	}
}

// ---------- api ----------

func apiCommand() *cli.Command {
	return &cli.Command{
		Name:  "api",
		Usage: "Enable or disable GCP APIs",
		Subcommands: []*cli.Command{
			{
				Name:   "enable",
				Usage:  "Enable APIs from a file",
				Action: gcp.EnableAPIs,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "project", Aliases: []string{"p"}, Required: true},
					&cli.StringFlag{Name: "api-file-path", Aliases: []string{"f"}, Required: true},
				},
			},
			{
				Name:   "disable",
				Usage:  "Disable APIs from a file",
				Action: gcp.DisableAPIs,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "project", Aliases: []string{"p"}, Required: true},
					&cli.StringFlag{Name: "api-file-path", Aliases: []string{"f"}, Required: true},
				},
			},
		},
	}
}

// ---------- sa ----------

func saCommand() *cli.Command {
	return &cli.Command{
		Name:  "sa",
		Usage: "Create service accounts and keys",
		Subcommands: []*cli.Command{
			{
				Name:   "create",
				Usage:  "Create a service account with roles from a file",
				Action: gcp.CreateServiceAccount,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Required: true},
					&cli.StringFlag{Name: "project", Required: true},
					&cli.StringFlag{Name: "roles-file", Aliases: []string{"r"}, Required: true},
					&cli.StringFlag{Name: "description"},
					&cli.StringFlag{Name: "display-name"},
					&cli.StringFlag{Name: "output-file", Aliases: []string{"o"}, Value: "key.json"},
				},
			},
			{
				Name:   "create-key",
				Usage:  "Create a key for an existing service account",
				Action: gcp.CreateServiceAccountKey,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "name", Required: true},
					&cli.StringFlag{Name: "project", Required: true},
					&cli.StringFlag{Name: "output-file", Aliases: []string{"o"}, Value: "key.json"},
				},
			},
		},
	}
}
