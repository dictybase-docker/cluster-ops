package main

import (
	"fmt"
	"strings"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	PR "github.com/IBM/fp-go/v2/pair"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	defaultSchedule   = "30 */4 * * *"
	defaultKeepLast   = 42
	defaultKeepDaily  = 30
	defaultSecretName = "filestash-backup-auth"
	bucketPrefix      = "restic-filestash-backup-"
	defaultResticImg  = "restic/restic:0.17.0@sha256:" +
		"da2bd7f56bc940bd14166d4802370d49b2232ee1d28898a3295fbf3d73606d80"
	defaultSqliteImg = "keinos/sqlite3:3.54.1@sha256:" +
		"b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17"
)

// BackupConfig is the `filestash-backup:properties` stack value.
type BackupConfig struct {
	Namespace   string       `json:"namespace"`
	Bucket      string       `json:"bucket"`
	Schedule    string       `json:"schedule"`
	KeepLast    int          `json:"keepLast"`
	KeepDaily   int          `json:"keepDaily"`
	ResticImage string       `json:"resticImage"`
	SqliteImage string       `json:"sqliteImage"`
	Secret      SecretConfig `json:"secret"`
}

// SecretConfig holds the KMS-encrypted values of Secret filestash-backup-auth.
// ServiceAccountFile is the path of the backup service account JSON key; the
// program reads the file at deploy time.
type SecretConfig struct {
	Name               string `json:"name"`
	ResticPass         string `json:"resticPass"`
	GcsProject         string `json:"gcsProject"`
	ServiceAccountFile string `json:"serviceAccountFile"`
}

type fieldCheck = PR.Pair[string, bool]

var (
	isNotBackupBucket = P.Not(S.HasPrefix(bucketPrefix))
	failedFields      = F.Flow2(
		A.Filter(PR.Tail[string, bool]),
		A.Map(PR.Head[string, bool]),
	)
)

func strOr(fallback, v string) string {
	return F.Pipe2(v, O.FromPredicate(S.IsNonEmpty), O.GetOrElse(F.Constant(fallback)))
}

func intOr(fallback, v int) int {
	return F.Pipe2(v, O.FromPredicate(P.IsNonZero[int]()), O.GetOrElse(F.Constant(fallback)))
}

// ReadConfig reads, defaults, and validates `filestash-backup:properties`.
func ReadConfig(ctx *pulumi.Context) (*BackupConfig, error) {
	cfg := &BackupConfig{}
	if err := config.New(ctx, "filestash-backup").TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read filestash-backup config: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

// ApplyDefaults fills every cluster-independent default.
func (c *BackupConfig) ApplyDefaults() {
	c.Schedule = strOr(defaultSchedule, c.Schedule)
	c.KeepLast = intOr(defaultKeepLast, c.KeepLast)
	c.KeepDaily = intOr(defaultKeepDaily, c.KeepDaily)
	c.ResticImage = strOr(defaultResticImg, c.ResticImage)
	c.SqliteImage = strOr(defaultSqliteImg, c.SqliteImage)
	c.Secret.Name = strOr(defaultSecretName, c.Secret.Name)
}

// Validate names every missing or invalid field.
func (c *BackupConfig) Validate() error {
	return F.Pipe4(
		checks(c),
		failedFields,
		O.FromPredicate(A.IsNonEmpty[string]),
		O.Map(F.Bind2nd(strings.Join, ", ")),
		O.Fold(F.Constant[error](nil), invalidFields),
	)
}

func invalidFields(fields string) error {
	return fmt.Errorf("filestash-backup:properties is invalid in %s", fields)
}

func checks(c *BackupConfig) []fieldCheck {
	return []fieldCheck{
		PR.MakePair("namespace", S.IsEmpty(c.Namespace)),
		PR.MakePair("bucket", isNotBackupBucket(c.Bucket)),
		PR.MakePair("keepLast", c.KeepLast < 1),
		PR.MakePair("keepDaily", c.KeepDaily < 1),
		PR.MakePair("secret.resticPass", S.IsEmpty(c.Secret.ResticPass)),
		PR.MakePair("secret.gcsProject", S.IsEmpty(c.Secret.GcsProject)),
		PR.MakePair("secret.serviceAccountFile", S.IsEmpty(c.Secret.ServiceAccountFile)),
	}
}
