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

const (
	projectFlag = "project"
	nameFlag    = "name"
)

func main() {
	app := &cli.App{
		Name:  "cluster-ops",
		Usage: "Unified cluster lifecycle management",
		Commands: []*cli.Command{
			kopsCommand(),
			validateCommand(),
			bucketCommand(),
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
				Name:   "update",
				Usage:  "Apply pending cluster changes (version-aware dispatch)",
				Action: kops.UpdateCluster,
			},
			{
				Name:   "plan",
				Usage:  "Preview pending cluster changes without applying them (version-aware dispatch)",
				Action: kops.PlanCluster,
			},
			{
				Name:  "delete",
				Usage: "Teardown a cluster (dry-run by default, --yes to execute)",
				Flags: kopsDeleteFlags(),
				// Bridge: the unified CLI uses --cluster-name / --state / --project-id
				// but delete.go expects cluster-name / state / project-id / yes / non-interactive.
				Action: kops.DeleteCluster,
			},
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
			Name:  "yes",
			Usage: "Execute teardown (without this flag: dry-run only)",
		},
		&cli.BoolFlag{
			Name:  "non-interactive",
			Usage: "Skip confirmation prompt (for CI)",
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
						Name: projectFlag, Aliases: []string{"p"},
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
					&cli.StringFlag{Name: nameFlag, Aliases: []string{"n"}, Required: true},
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
					&cli.StringFlag{Name: projectFlag, Aliases: []string{"p"}, Required: true},
					&cli.StringFlag{Name: "api-file-path", Aliases: []string{"f"}, Required: true},
				},
			},
			{
				Name:   "disable",
				Usage:  "Disable APIs from a file",
				Action: gcp.DisableAPIs,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: projectFlag, Aliases: []string{"p"}, Required: true},
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
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: projectFlag, Required: true},
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
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: projectFlag, Required: true},
					&cli.StringFlag{Name: "output-file", Aliases: []string{"o"}, Value: "key.json"},
				},
			},
		},
	}
}
