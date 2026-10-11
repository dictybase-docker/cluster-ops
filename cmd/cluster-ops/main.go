package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/dictybase-docker/cluster-ops/internal/custodian"
	"github.com/dictybase-docker/cluster-ops/internal/gcp"
	"github.com/dictybase-docker/cluster-ops/internal/kops"
	"github.com/dictybase-docker/cluster-ops/internal/util"
	"github.com/urfave/cli/v2"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	iamv1 "google.golang.org/api/iam/v1"
)

const (
	projectFlag        = "project"
	nameFlag           = "name"
	bucketFlag         = "bucket"
	credFileFlag       = "cred-file"
	dryRunFlag         = "dry-run"
	roleFlag           = "role"
	displayNameFlg     = "display-name"
	keyFileDefault     = "key.json"
	appName            = "cluster-ops"
	stackFlag          = "stack"
	folderFlag         = "folder"
	envPulumiStack     = "PULUMI_STACK"
	allowProtectedFlag = "allow-protected"
	arangoServiceFlag  = "arango-service"
	createCmd          = "create"
)

func main() {
	app := &cli.App{
		Name:  appName,
		Usage: "Unified cluster lifecycle management",
		Commands: []*cli.Command{
			kopsCommand(),
			validateCommand(),
			preflightCommand(),
			forwardCommand(),
			waitCommand(),
			bucketCommand(),
			stackCommand(),
			deployCommand(),
			iamCommand(),
			envCommand(),
			apiCommand(),
			saCommand(),
			kmsCommand(),
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
				Name:  "normalize",
				Usage: "Normalize cluster manifests for semantic comparison",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Required: true},
					&cli.StringFlag{Name: "output", Aliases: []string{"o"}, Required: true},
				},
				Action: normalizeManifestAction,
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

func normalizeManifestAction(cltx *cli.Context) error {
	return kops.NormalizeYAMLFile(cltx.String("file"), cltx.String("output"))
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
			EnvVars: []string{envProjectID},
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
						Name: flagKubeconfig, Aliases: []string{"k"},
						EnvVars: []string{envKubeconfig},
					},
				},
			},
		},
	}
}

func validateHAAction(cltx *cli.Context) error {
	checks, err := custodian.ValidateHA(cltx.String(flagKubeconfig))
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
				Name:   createCmd,
				Usage:  "Create and harden a GCS state bucket",
				Action: gcp.CreateKopsStateBucket,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: projectFlag, Aliases: []string{"p"},
						Required: true,
					},
					&cli.StringFlag{
						Name: bucketFlag, Aliases: []string{"b"},
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
			{
				Name:   "converge",
				Usage:  "Converge bucket versioning and lifecycle (idempotent)",
				Action: bucketConvergeAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: projectFlag, Aliases: []string{"p"}, Required: true},
					&cli.StringFlag{Name: bucketFlag, Aliases: []string{"b"}, Required: true},
					&cli.StringFlag{Name: "location", Value: "US"},
					&cli.BoolFlag{Name: "versioning", Value: true},
					&cli.StringFlag{Name: "lifecycle-file"},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
			{
				Name:   "delete",
				Usage:  "Purge objects/versions and delete a bucket (destructive)",
				Action: bucketDeleteAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: bucketFlag, Aliases: []string{"b"}, Required: true},
					&cli.StringFlag{Name: "confirm", Usage: "Pass exactly 'yes' to delete"},
					&cli.BoolFlag{Name: dryRunFlag},
					&cli.StringFlag{Name: credFileFlag},
				},
			},
		},
	}
}

// bucketConvergeAction converges bucket versioning/lifecycle. Dry-run prints
// intent and mutates nothing.
func bucketConvergeAction(cltx *cli.Context) error {
	lifecycle, err := loadLifecycleFile(cltx.String("lifecycle-file"))
	if err != nil {
		return err
	}
	cfg := gcp.BucketConfig{
		Name:       cltx.String(bucketFlag),
		ProjectID:  cltx.String(projectFlag),
		Location:   cltx.String("location"),
		Versioning: cltx.Bool("versioning"),
		Lifecycle:  lifecycle,
		CredFile:   cltx.String(credFileFlag),
	}
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	res, err := runBucketConverge(cfg, mode)
	if err != nil {
		return err
	}
	return reportConverge(res, mode)
}

// reportConverge prints the action line (dry-run: intent on stderr + JSON).
func reportConverge(res gcp.Result, mode gcp.MutationMode) error {
	if mode.DryRun {
		fmt.Fprintf(os.Stderr, "[DRY RUN] %s %s\n", res.Name, res.Action)
		return printJSONLine(res)
	}
	fmt.Printf("%s %s\n", res.Name, res.Action)
	return nil
}

// loadLifecycleFile reads gcloud's --lifecycle-file YAML. It returns nil when
// the flag is absent: existing bucket rules are then left untouched.
func loadLifecycleFile(path string) (*storage.Lifecycle, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	parsed, err := gcp.ParseGcloudLifecycle(f)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func printJSONLine(v any) error {
	return json.NewEncoder(os.Stdout).Encode(v)
}

func runBucketConverge(cfg gcp.BucketConfig, mode gcp.MutationMode) (gcp.Result, error) {
	forced := F.Pipe1(
		convergeWithClient(cfg, mode),
		IOE.WithResource[gcp.Result](gcp.NewStorageClientFrom(cfg.CredFile), closeStorageClient),
	)()
	return E.UnwrapError(forced)
}

func convergeWithClient(
	cfg gcp.BucketConfig, mode gcp.MutationMode,
) func(*storage.Client) IOE.IOEither[error, gcp.Result] {
	return func(c *storage.Client) IOE.IOEither[error, gcp.Result] {
		return gcp.ConvergeBucket(gcp.StorageBucketAPI{Client: c}, cfg, mode)
	}
}

func closeStorageClient(c *storage.Client) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, c.Close()
	})
}

// bucketDeleteAction purges and deletes a state bucket. DryRun reports only;
// deletion requires --confirm yes (the wrapper's prompt converts to it).
func bucketDeleteAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{
		DryRun:    cltx.Bool(dryRunFlag),
		Confirmed: cltx.String("confirm") == "yes",
	}
	res, err := runBucketDelete(cltx.String(bucketFlag), cltx.String(credFileFlag), mode)
	if err != nil {
		return err
	}
	return reportConverge(res, mode)
}

func runBucketDelete(name, credFile string, mode gcp.MutationMode) (gcp.Result, error) {
	forced := F.Pipe1(
		deleteWithClient(name, mode),
		IOE.WithResource[gcp.Result](gcp.NewStorageClientFrom(credFile), closeStorageClient),
	)()
	return E.UnwrapError(forced)
}

func deleteWithClient(
	name string, mode gcp.MutationMode,
) func(*storage.Client) IOE.IOEither[error, gcp.Result] {
	return func(c *storage.Client) IOE.IOEither[error, gcp.Result] {
		return gcp.DeleteBucketState(gcp.StorageDeleteAPI{Client: c}, name, mode)
	}
}

// iamCommand manages GCP IAM bindings.
func iamCommand() *cli.Command {
	return &cli.Command{
		Name:  "iam",
		Usage: "Manage GCP IAM bindings",
		Subcommands: []*cli.Command{
			{
				Name:      "add-binding",
				Usage:     "Converge a project IAM binding (idempotent)",
				Action:    iamAddBindingAction,
				ArgsUsage: " ",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: projectFlag, Aliases: []string{"p"}, Required: true},
					&cli.StringFlag{Name: "member", Required: true},
					&cli.StringFlag{Name: roleFlag, Required: true},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
			{
				Name:   "add-bucket-binding",
				Usage:  "Converge a bucket IAM binding (idempotent)",
				Action: iamAddBucketBindingAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: bucketFlag, Aliases: []string{"b"}, Required: true},
					&cli.StringFlag{Name: "member", Required: true},
					&cli.StringFlag{Name: roleFlag, Required: true},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
		},
	}
}

func iamAddBindingAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	res, err := runIAMBinding(
		cltx.String(projectFlag), cltx.String("member"), cltx.String(roleFlag),
		cltx.String(credFileFlag), mode)
	if err != nil {
		return err
	}
	return reportConverge(res, mode)
}

func runIAMBinding(project, member, role, credFile string, mode gcp.MutationMode) (gcp.Result, error) {
	forced := F.Pipe1(
		iamBindingWithClient(project, member, role, mode),
		IOE.WithResource[gcp.Result](gcp.NewCRMService(credFile), closeCRMService),
	)()
	return E.UnwrapError(forced)
}

func iamBindingWithClient(
	project, member, role string, mode gcp.MutationMode,
) func(*crm.Service) IOE.IOEither[error, gcp.Result] {
	return func(s *crm.Service) IOE.IOEither[error, gcp.Result] {
		return gcp.ConvergeProjectIAMBinding(gcp.CRMProjectIAM{Service: s}, project, member, role, mode)
	}
}

func closeCRMService(*crm.Service) IOE.IOEither[error, struct{}] {
	return IOE.Of[error](struct{}{})
}

func iamAddBucketBindingAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	res, err := runBucketIAMBinding(
		cltx.String(bucketFlag), cltx.String("member"), cltx.String(roleFlag),
		cltx.String(credFileFlag), mode)
	if err != nil {
		return err
	}
	return reportConverge(res, mode)
}

func runBucketIAMBinding(bucket, member, role, credFile string, mode gcp.MutationMode) (gcp.Result, error) {
	forced := F.Pipe1(
		bucketIAMWithClient(bucket, member, role, mode),
		IOE.WithResource[gcp.Result](gcp.NewStorageClientFrom(credFile), closeStorageClient),
	)()
	return E.UnwrapError(forced)
}

func bucketIAMWithClient(
	bucket, member, role string, mode gcp.MutationMode,
) func(*storage.Client) IOE.IOEither[error, gcp.Result] {
	return func(c *storage.Client) IOE.IOEither[error, gcp.Result] {
		return gcp.ConvergeBucketIAMBinding(gcp.StorageBucketIAM{Client: c}, bucket, member, role, mode)
	}
}

// saCreateKeyAction converges an SA key: a live file matching an upstream key
// is reused; otherwise exactly one key is minted and written 0600.
func saCreateKeyAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	saEmail := cltx.String(nameFlag) + "@" + cltx.String(projectFlag) + ".iam.gserviceaccount.com"
	res, err := runSAKeyConverge(saEmail, cltx.String("output-file"), cltx.String(credFileFlag), mode)
	if err != nil {
		return err
	}
	return reportConverge(res, mode)
}

func runSAKeyConverge(saEmail, keyFile, credFile string, mode gcp.MutationMode) (gcp.Result, error) {
	forced := F.Pipe1(
		saKeyWithClient(saEmail, keyFile, mode),
		IOE.WithResource[gcp.Result](gcp.NewIAMService(credFile), closeIAMService),
	)()
	return E.UnwrapError(forced)
}

func saKeyWithClient(
	saEmail, keyFile string, mode gcp.MutationMode,
) func(*iamv1.Service) IOE.IOEither[error, gcp.Result] {
	return func(s *iamv1.Service) IOE.IOEither[error, gcp.Result] {
		return gcp.ConvergeSAKey(gcp.IAMSAKeyAPI{Service: s}, gcp.OSKeyFiles{}, saEmail, keyFile, mode)
	}
}

func closeIAMService(*iamv1.Service) IOE.IOEither[error, struct{}] {
	return IOE.Of[error](struct{}{})
}

func saSetupBackupAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	cfg := gcp.BackupSAConfig{
		Project:     cltx.String(projectFlag),
		SAName:      cltx.String(nameFlag),
		DisplayName: cltx.String(displayNameFlg),
		Roles:       splitCSV(cltx.String("roles")),
		KeyFilePath: cltx.String("key-file"),
		CredFile:    cltx.String(credFileFlag),
	}
	res, err := runSetupBackup(cfg, mode)
	if err != nil {
		return err
	}
	return reportConvergeAll(res, mode)
}

func saSetupReaderAction(cltx *cli.Context) error {
	mode := gcp.MutationMode{DryRun: cltx.Bool(dryRunFlag)}
	cfg := gcp.CrossProjectReaderConfig{
		SourceProject: cltx.String("source-project"),
		SAName:        cltx.String(nameFlag),
		DisplayName:   cltx.String(displayNameFlg),
		Bucket:        cltx.String(bucketFlag),
		Role:          cltx.String(roleFlag),
		KeyFilePath:   cltx.String("key-file"),
		CredFile:      cltx.String(credFileFlag),
	}
	res, err := runSetupReader(cfg, mode)
	if err != nil {
		return err
	}
	return reportConvergeAll(res, mode)
}

// splitCSV splits a comma-separated list, skipping empties.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := []string{}
	for v := range strings.SplitSeq(s, ",") {
		if t := strings.TrimSpace(v); t != "" {
			parts = append(parts, t)
		}
	}
	return parts
}

// reportConvergeAll prints one action line per composed step.
func reportConvergeAll(res []gcp.Result, mode gcp.MutationMode) error {
	for _, r := range res {
		if err := reportConverge(r, mode); err != nil {
			return err
		}
	}
	return nil
}

func runSetupBackup(cfg gcp.BackupSAConfig, mode gcp.MutationMode) ([]gcp.Result, error) {
	iamSvc, err := E.UnwrapError(gcp.NewCRMService(cfg.CredFile)())
	if err != nil {
		return nil, err
	}
	keySvc, err := E.UnwrapError(gcp.NewIAMService(cfg.CredFile)())
	if err != nil {
		return nil, err
	}
	forced := gcp.SetupBackupSA(
		gcp.IAMServiceAccountAPI{Service: keySvc},
		gcp.CRMProjectIAM{Service: iamSvc},
		gcp.IAMSAKeyAPI{Service: keySvc},
		gcp.OSKeyFiles{},
		cfg, mode,
	)()
	return E.UnwrapError(forced)
}

func runSetupReader(cfg gcp.CrossProjectReaderConfig, mode gcp.MutationMode) ([]gcp.Result, error) {
	keySvc, err := E.UnwrapError(gcp.NewIAMService(cfg.CredFile)())
	if err != nil {
		return nil, err
	}
	client, err := E.UnwrapError(gcp.NewStorageClientFrom(cfg.CredFile)())
	if err != nil {
		return nil, err
	}
	defer client.Close()
	forced := gcp.SetupCrossProjectReader(
		gcp.IAMServiceAccountAPI{Service: keySvc},
		gcp.StorageBucketIAM{Client: client},
		gcp.IAMSAKeyAPI{Service: keySvc},
		gcp.OSKeyFiles{},
		cfg, mode,
	)()
	return E.UnwrapError(forced)
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
					&cli.BoolFlag{
						Name: "disable-dependent-services",
						Usage: "Also disable services that depend on the listed APIs. " +
							"For bigquery.googleapis.com this can include container.googleapis.com (GKE).",
					},
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
				Name:   createCmd,
				Usage:  "Create a service account with roles from a file",
				Action: gcp.CreateServiceAccount,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: projectFlag, Required: true},
					&cli.StringFlag{Name: "roles-file", Aliases: []string{"r"}, Required: true},
					&cli.StringFlag{Name: "description"},
					&cli.StringFlag{Name: displayNameFlg},
					&cli.StringFlag{Name: "output-file", Aliases: []string{"o"}, Value: keyFileDefault},
				},
			},
			{
				Name:   "setup-backup",
				Usage:  "Converge backup SA + role bindings + key (idempotent)",
				Action: saSetupBackupAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: projectFlag, Required: true},
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: displayNameFlg},
					&cli.StringFlag{Name: "roles", Usage: "Comma-separated roles (may be empty)"},
					&cli.StringFlag{Name: "key-file", Aliases: []string{"o"}, Value: keyFileDefault},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
			{
				Name:   "setup-reader",
				Usage:  "Converge cross-project reader SA + bucket binding + key (idempotent)",
				Action: saSetupReaderAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "source-project", Required: true},
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: displayNameFlg},
					&cli.StringFlag{Name: bucketFlag, Required: true},
					&cli.StringFlag{Name: roleFlag, Value: "roles/storage.objectViewer"},
					&cli.StringFlag{Name: "key-file", Aliases: []string{"o"}, Value: keyFileDefault},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
			{
				Name:   "create-key",
				Usage:  "Converge a key for an existing service account (idempotent)",
				Action: saCreateKeyAction,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: nameFlag, Required: true},
					&cli.StringFlag{Name: projectFlag, Required: true},
					&cli.StringFlag{Name: "output-file", Aliases: []string{"o"}, Value: keyFileDefault},
					&cli.StringFlag{Name: credFileFlag},
					&cli.BoolFlag{Name: dryRunFlag},
				},
			},
		},
	}
}

// ---------- kms ----------

func kmsCommand() *cli.Command {
	return &cli.Command{
		Name:  "kms",
		Usage: "Manage Cloud KMS keyrings and keys",
		Subcommands: []*cli.Command{
			{
				Name:   createCmd,
				Usage:  "Create KMS keyring and key",
				Action: gcp.CreateKeyringAndKey,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "project-id",
						Aliases:  []string{"p"},
						EnvVars:  []string{"PROJECT_ID"},
						Required: true,
					},
					&cli.StringFlag{
						Name:     "keyring-name",
						Aliases:  []string{"r"},
						Required: true,
					},
					&cli.StringFlag{
						Name:     "key-name",
						Aliases:  []string{"k"},
						Required: true,
					},
					&cli.StringFlag{
						Name:    "location",
						Aliases: []string{"l"},
						Value:   "us-central1",
					},
					&cli.StringFlag{
						Name:    "credentials",
						Aliases: []string{"c"},
						EnvVars: []string{
							"PULUMI_GCP_CREDENTIALS",
							"GOOGLE_APPLICATION_CREDENTIALS",
						},
						Required: true,
					},
				},
			},
		},
	}
}
