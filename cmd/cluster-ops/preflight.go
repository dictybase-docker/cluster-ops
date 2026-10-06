package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

const deployerRolesFile = "gcs-files/roles-permissions/deployer-roles.txt"

var (
	errNoCluster = errors.New("no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)")
	errNoStack   = errors.New("no stack name — pass --stack or set PULUMI_STACK (via cluster env)")
)

func registryDirFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "registry-dir",
		Value: "config/clusters",
		Usage: "Cluster registry directory",
	}
}

func clusterFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "cluster", Aliases: []string{"c"},
			EnvVars: []string{"CLUSTER_NAME"},
			Usage:   "Cluster name (defaults to CLUSTER_NAME)",
		},
		&cli.StringFlag{
			Name: "sa-key", Aliases: []string{"k"},
			Required: true,
			Usage:    "Deployer service account key JSON path",
		},
		registryDirFlag(),
	}
}

func preflightCommand() *cli.Command {
	return &cli.Command{
		Name:  "preflight",
		Usage: "Read-only deploy gates; exit 1 lists every failed check",
		Subcommands: []*cli.Command{
			{
				Name:   "backend-prereqs",
				Usage:  "Namespace, ArangoDB Secret, database via the app's Service, port",
				Action: backendPrereqsAction,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "folder", Aliases: []string{"f"},
						Required: true,
						Usage:    "Service project folder (stack config source)",
					},
					&cli.StringFlag{
						Name: "stack", Aliases: []string{"s"},
						EnvVars: []string{"PULUMI_STACK"},
						Usage:   "Pulumi stack name (defaults to PULUMI_STACK)",
					},
					&cli.StringFlag{
						Name: "arango-service", Aliases: []string{"a"},
						Value: "arangodb",
						Usage: "ArangoDB Service name the app connects to",
					},
					&cli.StringFlag{
						Name:    "kubeconfig",
						EnvVars: []string{"KUBECONFIG"},
						Usage:   "Kubeconfig path (defaults to KUBECONFIG)",
					},
					registryDirFlag(),
				},
			},
			{
				Name:   "deployer-access",
				Usage:  "Deployer key project, cluster access via kops state, KMS round-trip",
				Action: deployerAccessAction,
				Flags:  clusterFlags(),
			},
			{
				Name:   "deploy-credentials",
				Usage:  "deployer-access plus every IAM permission of deployer-roles.txt",
				Action: deployCredentialsAction,
				Flags:  clusterFlags(),
			},
			{
				Name:   "pulumi-backend",
				Usage:  "PULUMI_* wiring: env, manager key, state bucket, KMS key, active login",
				Action: pulumiBackendAction,
			},
		},
	}
}

// reportGate prints every result; a failed gate exits 1 after one summary
// line, a green gate prints okLines (the last is the Next: recipe hint).
func reportGate(results []preflight.Result, failMsg string, okLines ...string) error {
	preflight.Print(os.Stdout, results)
	if preflight.Failed(results) {
		return cli.Exit("\nERROR: "+failMsg+" — fix the FAIL lines above, then re-run.", 1)
	}
	for _, line := range okLines {
		fmt.Println(line)
	}
	return nil
}

func runGate(checks []IO.IO[preflight.Result]) []preflight.Result {
	return preflight.Run(checks)
}

// ---------- deployer-access / deploy-credentials ----------

func loadDeployerConfig(cltx *cli.Context) E.Either[error, backend.DeployerConfig] {
	return F.Pipe3(
		cltx.String("cluster"),
		E.FromPredicate(S.IsNonEmpty, func(string) error { return errNoCluster }),
		E.Chain(func(name string) E.Either[error, registry.Entry] {
			return registry.LoadByName(cltx.String("registry-dir"), name)
		}),
		E.Map[error](func(entry registry.Entry) backend.DeployerConfig {
			return backend.DeployerConfig{Entry: entry, SAKeyPath: cltx.String("sa-key")}
		}),
	)
}

func deployerAccessAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadDeployerConfig(cltx),
		E.Fold(F.Identity[error], func(cfg backend.DeployerConfig) error {
			return reportGate(
				runGate(backend.DeployerChecks(cfg)),
				"deployer access verification failed",
				fmt.Sprintf("\nDeployer access verified for %s.", cfg.Entry.Cluster),
				fmt.Sprintf("Next: just ci check-deploy-credentials --cluster %s --sa-key %s", cfg.Entry.Cluster, cfg.SAKeyPath),
			)
		}),
	)
}

func deployCredentialsAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadDeployerConfig(cltx),
		E.Fold(F.Identity[error], func(cfg backend.DeployerConfig) error {
			return reportGate(
				runGate(append(
					backend.DeployerChecks(cfg),
					backend.CheckIAMPermissions(backend.CredentialsConfig{
						Entry:         cfg.Entry,
						SAKeyPath:     cfg.SAKeyPath,
						RolesFilePath: deployerRolesFile,
					}),
				)),
				"deploy credentials verification failed",
				fmt.Sprintf("\nDeploy credentials verified for %s.", cfg.Entry.Cluster),
				fmt.Sprintf(
					"Next: just ci set-deploy-secret --cluster %s --sa-key %s --repos <owner/name>",
					cfg.Entry.Cluster, cfg.SAKeyPath,
				),
			)
		}),
	)
}

// ---------- backend-prereqs ----------

// stackProperties is the slice of <project>:properties the gate needs.
type stackProperties struct {
	AppName        string `yaml:"appName"`
	Port           int    `yaml:"port"`
	ArangodbSecret struct {
		Name    string `yaml:"name"`
		Userkey string `yaml:"userkey"`
		Passkey string `yaml:"passkey"`
	} `yaml:"arangodbSecret"`
}

// prereqsInput threads the resolved inputs through the loading pipeline.
type prereqsInput struct {
	folder, stack, project string
	props                  stackProperties
	entry                  registry.Entry
	clients                kube.Clients
}

func readStackProperties(in prereqsInput) E.Either[error, prereqsInput] {
	path := filepath.Join(in.folder, "Pulumi."+in.stack+".yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return E.Left[prereqsInput](fmt.Errorf("MISSING: %s — scaffold it first (scaffold-backend-stack)", path))
	}
	var doc struct {
		Config map[string]stackProperties `yaml:"config"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return E.Left[prereqsInput](fmt.Errorf("parse %s: %w", path, err))
	}
	props, ok := doc.Config[in.project+":properties"]
	if !ok {
		return E.Left[prereqsInput](fmt.Errorf("%s has no %s:properties block", path, in.project))
	}
	in.props = props
	return E.Right[error](in)
}

func loadPrereqsInput(cltx *cli.Context) E.Either[error, prereqsInput] {
	folder := filepath.Clean(cltx.String("folder"))
	return F.Pipe4(
		prereqsInput{folder: folder, stack: cltx.String("stack"), project: filepath.Base(folder)},
		E.FromPredicate(func(in prereqsInput) bool { return in.stack != "" }, func(prereqsInput) error { return errNoStack }),
		E.Chain(readStackProperties),
		E.Chain(func(in prereqsInput) E.Either[error, prereqsInput] {
			return F.Pipe1(
				registry.LoadByStack(cltx.String("registry-dir"), in.stack),
				E.Map[error](func(entry registry.Entry) prereqsInput { in.entry = entry; return in }),
			)
		}),
		E.Chain(func(in prereqsInput) E.Either[error, prereqsInput] {
			return F.Pipe1(
				kube.NewClients(cltx.String("kubeconfig"), ""),
				E.Map[error](func(c kube.Clients) prereqsInput { in.clients = c; return in }),
			)
		}),
	)
}

func backendPrereqsAction(cltx *cli.Context) error {
	return F.Pipe1(
		loadPrereqsInput(cltx),
		E.Fold(F.Identity[error], func(in prereqsInput) error {
			return reportGate(
				runGate(backend.PrereqsChecks(backend.PrereqsConfig{
					Clients:       in.clients,
					Namespace:     in.entry.Namespace,
					SecretName:    in.props.ArangodbSecret.Name,
					UserKey:       in.props.ArangodbSecret.Userkey,
					PassKey:       in.props.ArangodbSecret.Passkey,
					ArangoService: cltx.String("arango-service"),
					AppName:       in.props.AppName,
					Port:          in.props.Port,
				})),
				"prerequisite gate failed",
				fmt.Sprintf("\nAll prerequisites green for %s/%s.", in.project, in.stack),
				fmt.Sprintf(
					"Next: just gcp-pulumi bootstrap-service --stack %s --folder %s --image-tag <published-tag>",
					in.stack, in.folder,
				),
			)
		}),
	)
}

// ---------- pulumi-backend ----------

func pulumiBackendAction(_ *cli.Context) error {
	return reportGate(
		runGate(backend.PulumiChecks(backend.PulumiBackendConfig{
			CredsPath:       os.Getenv("PULUMI_GCP_CREDENTIALS"),
			SecretsProvider: os.Getenv("PULUMI_SECRET_PROVIDER"),
			BackendURL:      os.Getenv("PULUMI_BACKEND_URL"),
			Stack:           os.Getenv("PULUMI_STACK"),
		})),
		"Pulumi backend check failed (docs/reference/pulumi/backend-bootstrap.md)",
		"\nBackend wiring looks correct.",
	)
}
