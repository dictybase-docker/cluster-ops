package main

import (
	"fmt"
	"regexp"
	"strings"

	A "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	PR "github.com/IBM/fp-go/v2/pair"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	configNamespace     = "filestash"
	defaultAppName      = "filestash"
	defaultImageName    = "machines/filestash"
	defaultPort         = 8334
	defaultReplicas     = 1
	defaultStorageClass = "dictycr-balanced"
	defaultCacheLimit   = "2Gi"
	defaultSecretName   = "filestash"
	defaultS3Region     = "us-east-1"
	defaultS3Label      = "dictycr-minio"
	defaultIngressClass = "nginx"
	minStateSize        = "1Gi"
	defaultBackupCron   = "30 */4 * * *"
	defaultBackupKeep   = 7
	defaultSqliteImage  = "keinos/sqlite3:3.54.1@sha256:" +
		"b577e65892c6468d94b390097eda4488e05253470c161bdea68a05abb961fa17"
	defaultKubectlImage = "alpine/kubectl:1.35.4@sha256:" +
		"9ccd823647622b620dce092d0f1406b8b41d152ce08ac963e3d4683770b14dfd"
)

// FilestashConfig is the `filestash:properties` stack value.
type FilestashConfig struct {
	Name           string      `json:"name"`
	Namespace      string      `json:"namespace"`
	ApplicationURL string      `json:"applicationURL"`
	Port           int         `json:"port"`
	Replicas       int         `json:"replicas"`
	Image          ImageConfig `json:"image"`
	// PreviousImage is written by `just filestash upgrade` and read by
	// `just filestash rollback`. Pulumi does not use it.
	PreviousImage  ImageConfig   `json:"previousImage"`
	SecretName     string        `json:"secretName"`
	Storage        StorageConfig `json:"storage"`
	CacheSizeLimit string        `json:"cacheSizeLimit"`
	S3             S3Config      `json:"s3"`
	Ingress        IngressConfig `json:"ingress"`
	Plugins        PluginsConfig `json:"plugins"`
	Backup         BackupConfig  `json:"backup"`
}

// BackupConfig drives the SQLite-consistent state copy CronJob.
// MaxAgeHours is the recovery point target: `just filestash verify` fails
// when the newest copy or Velero backup is older. It has no default.
type BackupConfig struct {
	Schedule     string `json:"schedule"`
	Keep         int    `json:"keep"`
	MaxAgeHours  int    `json:"maxAgeHours"`
	SqliteImage  string `json:"sqliteImage"`
	KubectlImage string `json:"kubectlImage"`
}

// PluginsConfig points at the plugin bundle image built by
// `just filestash build-plugins`. An init container copies its ZIP files
// into the plugins folder at each start.
type PluginsConfig struct {
	Image ImageConfig `json:"image"`
}

type ImageConfig struct {
	Name   string `json:"name"`
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

// Ref is the pinned image reference. The digest wins over the tag.
func (i ImageConfig) Ref() string {
	return fmt.Sprintf("%s:%s@%s", i.Name, i.Tag, i.Digest)
}

type StorageConfig struct {
	Size         string `json:"size"`
	StorageClass string `json:"storageClass"`
}

type S3Config struct {
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Label    string `json:"label"`
}

type IngressConfig struct {
	Enabled   bool        `json:"enabled"`
	ClassName string      `json:"className"`
	Hosts     []string    `json:"hosts"`
	TLSSecret string      `json:"tlsSecret"`
	Label     LabelConfig `json:"label"`
}

type LabelConfig struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// fieldCheck pairs a field name with "this check failed".
type fieldCheck = PR.Pair[string, bool]

var (
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	minStateQty     = resource.MustParse(minStateSize)
	parseQuantity   = E.Eitherize1(resource.ParseQuantity)
	isPlaceholder   = P.Or(S.Includes("<"))(S.Includes(">"))
	isInvalidDigest = P.Not(digestPattern.MatchString)
	isNotHTTPS      = P.Not(S.HasPrefix("https://"))
	failedFields    = F.Flow2(
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

func isBadQuantity(v string) bool {
	return F.Pipe2(v, parseQuantity, E.Fold(F.Constant1[error](true), F.Constant1[resource.Quantity](false)))
}

func isBelowMinState(q resource.Quantity) bool {
	return q.Cmp(minStateQty) < 0
}

func isBadStateSize(v string) bool {
	return F.Pipe3(
		v,
		parseQuantity,
		E.Map[error](isBelowMinState),
		E.GetOrElse(F.Constant1[error](true)),
	)
}

func anyPlaceholder(vals []string) bool {
	return F.Pipe1(vals, A.Any(isPlaceholder))
}

func anyEmpty(vals []string) bool {
	return A.IsEmpty(vals) || F.Pipe1(vals, A.Any(S.IsEmpty))
}

// ReadConfig reads, defaults, and validates `filestash:properties`.
func ReadConfig(ctx *pulumi.Context) (*FilestashConfig, error) {
	cfg := &FilestashConfig{}
	if err := config.New(ctx, configNamespace).TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read filestash config: %w", err)
	}
	cfg.ApplyDefaults()
	return cfg, cfg.Validate()
}

// ApplyDefaults fills every field that has a cluster-independent default.
// storage.size has no default: each stack file sets it.
func (c *FilestashConfig) ApplyDefaults() {
	c.Name = strOr(defaultAppName, c.Name)
	c.Port = intOr(defaultPort, c.Port)
	c.Replicas = intOr(defaultReplicas, c.Replicas)
	c.Image.Name = strOr(defaultImageName, c.Image.Name)
	c.SecretName = strOr(defaultSecretName, c.SecretName)
	c.Storage.StorageClass = strOr(defaultStorageClass, c.Storage.StorageClass)
	c.CacheSizeLimit = strOr(defaultCacheLimit, c.CacheSizeLimit)
	c.S3.Region = strOr(defaultS3Region, c.S3.Region)
	c.S3.Label = strOr(defaultS3Label, c.S3.Label)
	c.Ingress.ClassName = strOr(defaultIngressClass, c.Ingress.ClassName)
	c.Backup.Schedule = strOr(defaultBackupCron, c.Backup.Schedule)
	c.Backup.Keep = intOr(defaultBackupKeep, c.Backup.Keep)
	c.Backup.SqliteImage = strOr(defaultSqliteImage, c.Backup.SqliteImage)
	c.Backup.KubectlImage = strOr(defaultKubectlImage, c.Backup.KubectlImage)
}

// Validate rejects `<...>` placeholders first, then invalid values.
func (c *FilestashConfig) Validate() error {
	return F.Pipe3(
		E.Of[error](c),
		E.Chain(F.Bind12of3(rejectFields)("contains placeholder in", placeholderChecks)),
		E.Chain(F.Bind12of3(rejectFields)("is invalid in", invalidChecks)),
		E.ToError[*FilestashConfig],
	)
}

func invalidChecks(c *FilestashConfig) []fieldCheck {
	return A.ArrayConcatAll(
		[]fieldCheck{
			PR.MakePair("namespace", S.IsEmpty(c.Namespace)),
			PR.MakePair("image.tag", S.IsEmpty(c.Image.Tag)),
			PR.MakePair("image.digest", isInvalidDigest(c.Image.Digest)),
			PR.MakePair("replicas", c.Replicas != defaultReplicas),
			PR.MakePair("applicationURL", isNotHTTPS(c.ApplicationURL)),
			PR.MakePair("storage.size", isBadStateSize(c.Storage.Size)),
			PR.MakePair("cacheSizeLimit", isBadQuantity(c.CacheSizeLimit)),
			PR.MakePair("s3.endpoint", S.IsEmpty(c.S3.Endpoint)),
			PR.MakePair("backup.maxAgeHours", c.Backup.MaxAgeHours < 1),
			PR.MakePair("backup.keep", c.Backup.Keep < 1),
			PR.MakePair("plugins.image.name", S.IsEmpty(c.Plugins.Image.Name)),
			PR.MakePair("plugins.image.tag", S.IsEmpty(c.Plugins.Image.Tag)),
			PR.MakePair("plugins.image.digest", isInvalidDigest(c.Plugins.Image.Digest)),
		},
		ingressInvalidChecks(c.Ingress),
	)
}

func ingressInvalidChecks(ing IngressConfig) []fieldCheck {
	return []fieldCheck{
		PR.MakePair("ingress.hosts", ing.Enabled && anyEmpty(ing.Hosts)),
		PR.MakePair("ingress.tlsSecret", ing.Enabled && S.IsEmpty(ing.TLSSecret)),
	}
}

func placeholderChecks(c *FilestashConfig) []fieldCheck {
	return []fieldCheck{
		PR.MakePair("namespace", isPlaceholder(c.Namespace)),
		PR.MakePair("applicationURL", isPlaceholder(c.ApplicationURL)),
		PR.MakePair("s3.endpoint", isPlaceholder(c.S3.Endpoint)),
		PR.MakePair("plugins.image.tag", isPlaceholder(c.Plugins.Image.Tag)),
		PR.MakePair("plugins.image.digest", isPlaceholder(c.Plugins.Image.Digest)),
		PR.MakePair("ingress.hosts", c.Ingress.Enabled && anyPlaceholder(c.Ingress.Hosts)),
		PR.MakePair("ingress.tlsSecret", c.Ingress.Enabled && isPlaceholder(c.Ingress.TLSSecret)),
	}
}

// rejectFields fails with every field name whose check failed.
func rejectFields(
	reason string,
	checks func(*FilestashConfig) []fieldCheck,
	c *FilestashConfig,
) E.Either[error, *FilestashConfig] {
	return F.Pipe3(
		checks(c),
		failedFields,
		O.FromPredicate(A.IsNonEmpty[string]),
		O.Fold(
			F.Constant(E.Right[error](c)),
			F.Flow2(F.Bind2nd(strings.Join, ", "), F.Bind1st(fieldError, reason)),
		),
	)
}

func fieldError(reason, fields string) E.Either[error, *FilestashConfig] {
	return E.Left[*FilestashConfig](fmt.Errorf("filestash:properties %s %s", reason, fields))
}
