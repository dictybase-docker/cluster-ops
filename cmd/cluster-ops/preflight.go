package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IO "github.com/IBM/fp-go/v2/io"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/dictybase-docker/cluster-ops/internal/preflight"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/backend"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/datastore"
	"github.com/dictybase-docker/cluster-ops/internal/preflight/toolchain"
	"github.com/dictybase-docker/cluster-ops/internal/registry"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v3"
)

const deployerRolesFile = "gcs-files/roles-permissions/deployer-roles.txt"

const (
	defPoolValue     = "database"
	defClasses       = "dictycr-balanced"
	defProvisioner   = "pd.csi.storage.gke.io"
	defArangoService = "arangodb"
	defRedisName     = "redis"
	flagPool         = "pool"
	flagCount        = "count"
	flagNodeCount    = "node-count"
	flagNamespace    = "namespace"
	flagOperatorNS   = "operator-namespace"
	flagMembers      = "members"
	flagInstances    = "instances"
	flagRequireAmd64 = "require-amd64"
	usageNodePool    = "Node label pool=<pool>"
	usageNodeCount   = "Expected pool node count"
)

var (
	errNoCluster = errors.New("no cluster name — pass --cluster or set CLUSTER_NAME (via cluster env)")
	errNoStack   = errors.New("no stack name — pass --stack or set PULUMI_STACK (via cluster env)")
)

const (
	flagKubeconfig = "kubeconfig"
	envKubeconfig  = "KUBECONFIG"
	cmdForward     = "forward"
	cmdForwardRun  = "run"
	cmdForwardHold = "hold"
	flagNS         = "ns"
	flagSVC        = "svc"
	flagRemotePort = "remote-port"
	flagLocalPort  = "local-port"
)

// kubeconfigFlag is the shared kubeconfig flag (defaults to KUBECONFIG env).
func kubeconfigFlag() cli.Flag {
	return &cli.StringFlag{
		Name:    flagKubeconfig,
		EnvVars: []string{envKubeconfig},
		Usage:   "Kubeconfig path (defaults to KUBECONFIG)",
	}
}

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

//nolint:funlen,cyclop // subcommand table; splitting hurts review
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
						Value: defArangoService,
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
				Name:   "storageclass",
				Usage:  "StorageClasses this repo's databases depend on (exists + provisioner + default report)",
				Action: storageclassAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "classes", Value: defClasses, Usage: "Comma-separated StorageClass names"},
					&cli.StringFlag{Name: "provisioner", Value: defProvisioner, Usage: "Expected provisioner"},
					kubeconfigFlag(),
				},
			},
			{
				Name:   "tools",
				Usage:  "Local toolchain binaries and versions",
				Action: toolsAction,
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
			{
				Name:   "pool",
				Usage:  "stateful-db pool prerequisites: node count, arch, taint, zones, Ready",
				Action: poolAction,
				Flags:  poolFlags(),
			},
			{
				Name:   "arangodb",
				Usage:  "Full ArangoDB post-install check: pool, operator, CR state, members, PVCs, Service, Job",
				Flags:  arangodbFlags(),
				Action: arangodbVerifyAction,
			},
			{
				Name:   "arango-app-credentials",
				Usage:  "Run an ephemeral arangosh probe per database with the app Secret (env-only credentials)",
				Flags:  arangoAppCredentialsFlags(),
				Action: arangoAppCredentialsAction,
			},
			{
				Name:   "postgres",
				Usage:  "Full CNPG post-install check: pool, operator, CR phase, instances, PVCs, Services, backup",
				Flags:  postgresFlags(),
				Action: postgresVerifyAction,
			},
			{
				Name:   "redis",
				Usage:  "Redis post-install check: pool, pod, data PVC, Service, PING handshake",
				Flags:  redisPreflightFlags(),
				Action: redisVerifyAction,
			},
			{
				Name:   "minio",
				Usage:  "MinIO post-install check: pool, pod, PVCs, Service, root Secret",
				Flags:  minioPreflightFlags(),
				Action: minioVerifyAction,
			},
			{
				Name:   "nats",
				Usage:  "NATS post-install check: StatefulSet readiness, Service port, nats-box rtt",
				Flags:  natsPreflightFlags(),
				Action: natsVerifyAction,
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

func toolsAction(_ *cli.Context) error {
	return reportGate(
		runGate(toolchain.CheckTools(toolchain.DefaultToolsConfig())),
		"toolchain check failed",
		"\nAll required tools present.",
	)
}

func storageclassAction(cltx *cli.Context) error {
	clients, err := newClientsValue(cltx)
	if err != nil {
		return err
	}
	classes := []string{}
	for raw := range strings.SplitSeq(cltx.String("classes"), ",") {
		if class := strings.TrimSpace(raw); class != "" {
			classes = append(classes, class)
		}
	}
	return reportGate(
		runGate(backend.CheckStorageClass(backend.StorageClassConfig{
			Clients: clients, Classes: classes, Provisioner: cltx.String("provisioner"),
		})),
		"storageclass check failed",
		"\nAll StorageClass checks passed.",
	)
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

// ---------- datastore verify subcommands ----------

func kubeconfigOnlyFlags() []cli.Flag {
	return []cli.Flag{
		kubeconfigFlag(),
		&cli.StringFlag{Name: "context", Usage: "Kubeconfig context (defaults to current-context)"},
	}
}

func poolFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{Name: flagPool, Value: defPoolValue, Usage: usageNodePool},
		&cli.IntFlag{Name: flagCount, Value: 3, Usage: "Expected node count"},
		&cli.BoolFlag{Name: flagRequireAmd64, Usage: "Fail when a pool node is not amd64 (ArangoDB)"},
	)
}

func poolAction(cltx *cli.Context) error {
	clients, err := newClientsValue(cltx)
	if err != nil {
		return err
	}
	return reportGate(
		runGate(datastore.CheckPool(datastore.PoolConfig{
			Clients: clients, Pool: cltx.String("pool"), WantCount: cltx.Int("count"),
			RequireAmd64: cltx.Bool("require-amd64"),
		})),
		"pool prerequisite check failed",
	)
}

func newClientsValue(cltx *cli.Context) (kube.Clients, error) {
	return E.UnwrapError(newClientsIOE(cltx)())
}

func datastoreAction(
	build func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result],
	failMsg string,
) cli.ActionFunc {
	return func(cltx *cli.Context) error {
		clients, err := newClientsValue(cltx)
		if err != nil {
			return err
		}
		return reportGate(runGate(build(cltx, clients)), failMsg)
	}
}

func arangodbVerifyAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyArangoDB(datastore.ArangoConfig{
			Clients:           clients,
			Namespace:         cltx.String("namespace"),
			OperatorNamespace: cltx.String("operator-namespace"),
			Members:           cltx.Int("members"),
			Pool:              cltx.String("pool"),
			WantNodes:         cltx.Int("node-count"),
		})
	}, "ArangoDB verification failed")(cltx)
}

func arangoAppCredentialsAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyArangoAppCredentials(datastore.ArangoAppCredentialsConfig{
			Clients:    clients,
			Namespace:  cltx.String("namespace"),
			SecretName: cltx.String("secret-name"),
			UserKey:    cltx.String("user-key"),
			PassKey:    cltx.String("pass-key"),
			Image:      cltx.String("image"),
			Databases:  cltx.StringSlice("database"),
		})
	}, "application credential verification failed")(cltx)
}

func postgresVerifyAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyPostgres(datastore.PostgresConfig{
			Clients:           clients,
			Namespace:         cltx.String("namespace"),
			OperatorNamespace: cltx.String("operator-namespace"),
			Cluster:           cltx.String("cluster"),
			Instances:         cltx.Int("instances"),
			Pool:              cltx.String("pool"),
			WantNodes:         cltx.Int("node-count"),
		})
	}, "postgres verification failed")(cltx)
}

func redisVerifyAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyRedis(datastore.RedisConfig{
			Clients:   clients,
			Namespace: cltx.String("namespace"),
			Name:      cltx.String("name"),
			Pool:      cltx.String("pool"),
			WantNodes: cltx.Int("node-count"),
		})
	}, "redis verification failed")(cltx)
}

func minioVerifyAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyMinio(datastore.MinioConfig{
			Clients:   clients,
			Namespace: cltx.String("namespace"),
			Secret:    cltx.String("secret"),
			Pool:      cltx.String("pool"),
			WantNodes: cltx.Int("node-count"),
		})
	}, "minio verification failed")(cltx)
}

func natsVerifyAction(cltx *cli.Context) error {
	return datastoreAction(func(cltx *cli.Context, clients kube.Clients) []IO.IO[preflight.Result] {
		return datastore.VerifyNats(datastore.NatsConfig{
			Clients:   clients,
			Namespace: cltx.String("namespace"),
		})
	}, "nats verification failed")(cltx)
}

func arangodbFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{
			Name: flagNamespace, Aliases: []string{"n"}, Required: true,
			Usage: "Namespace holding the ArangoDeployment",
		},
		&cli.StringFlag{Name: flagOperatorNS, Value: "prod", Usage: "Namespace holding the kube-arangodb operator"},
		&cli.IntFlag{Name: flagMembers, Value: 9, Usage: "Expected member pod count"},
		&cli.StringFlag{Name: flagPool, Value: defPoolValue, Usage: usageNodePool},
		&cli.IntFlag{Name: flagNodeCount, Value: 3, Usage: usageNodeCount},
	)
}

func postgresFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{Name: flagNamespace, Aliases: []string{"n"}, Required: true, Usage: "Namespace holding the Cluster"},
		&cli.StringFlag{Name: flagOperatorNS, Value: "operators", Usage: "Namespace holding the CNPG operator"},
		&cli.StringFlag{Name: "cluster", Value: "logto", Usage: "Cluster name"},
		&cli.IntFlag{Name: flagInstances, Value: 1, Usage: "Expected instance pod count"},
		&cli.StringFlag{Name: flagPool, Value: defPoolValue, Usage: usageNodePool},
		&cli.IntFlag{Name: flagNodeCount, Value: 3, Usage: usageNodeCount},
	)
}

func arangoAppCredentialsFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{
			Name: flagNamespace, Aliases: []string{"n"}, Required: true,
			Usage: "Namespace holding the Secret and ArangoDB",
		},
		&cli.StringFlag{Name: "secret-name", Required: true, Usage: "Application credentials Secret"},
		&cli.StringFlag{Name: "user-key", Required: true, Usage: "Secret key for the username"},
		&cli.StringFlag{Name: "pass-key", Required: true, Usage: "Secret key for the password"},
		&cli.StringFlag{Name: "image", Required: true, Usage: "arangodb image tag (from the cluster stack config)"},
		&cli.StringSliceFlag{Name: "database", Required: true, Usage: "Database to verify (repeatable)"},
	)
}

func redisPreflightFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{Name: flagNamespace, Aliases: []string{"n"}, Required: true, Usage: "Namespace Redis runs in"},
		&cli.StringFlag{Name: "name", Value: defRedisName, Usage: "Deployment/Service name"},
		&cli.StringFlag{Name: flagPool, Value: defPoolValue, Usage: usageNodePool},
		&cli.IntFlag{Name: flagNodeCount, Value: 3, Usage: usageNodeCount},
	)
}

func minioPreflightFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{Name: flagNamespace, Aliases: []string{"n"}, Required: true, Usage: "Namespace MinIO runs in"},
		&cli.StringFlag{Name: "secret", Value: "minio-root", Usage: "Root credentials Secret"},
		&cli.StringFlag{Name: flagPool, Value: defPoolValue, Usage: usageNodePool},
		&cli.IntFlag{Name: flagNodeCount, Value: 3, Usage: usageNodeCount},
	)
}

func natsPreflightFlags() []cli.Flag {
	return append(kubeconfigOnlyFlags(),
		&cli.StringFlag{Name: flagNamespace, Aliases: []string{"n"}, Required: true, Usage: "Namespace NATS runs in"},
	)
}
