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
)

const (
	configNamespace = "falcon-sensor"
	// releaseName is the Helm release name and the chart fullnameOverride,
	// so the DaemonSet, ServiceAccount, and ConfigMap share it. The recipes
	// read it from the daemonSet stack output, never from a literal.
	releaseName = "falcon-sensor"
	chartName   = "falcon-sensor"
	imageName   = "falcon-sensor"
	// secretName holds FALCONCTL_OPT_CID and the optional
	// FALCONCTL_OPT_PROVISIONING_TOKEN. The chart reads it through
	// falconSecret.secretName.
	secretName = "falcon-sensor-cid"
	cidKey     = "FALCONCTL_OPT_CID"
	tokenKey   = "FALCONCTL_OPT_PROVISIONING_TOKEN" //nolint:gosec // env var name, not a credential
)

// FalconConfig is the `falcon-sensor:properties` stack value. cid and
// provisioningToken are KMS-encrypted stack secrets. Every other value is
// plain text. mirror-image writes image.repository, image.tag, and
// image.digest after it copies the sensor image into Artifact Registry.
type FalconConfig struct {
	Namespace   string         `json:"namespace"`
	ClusterName string         `json:"clusterName"`
	Cloud       string         `json:"cloud"`
	Chart       ChartConfig    `json:"chart"`
	Image       ImageConfig    `json:"image"`
	Registry    RegistryConfig `json:"registry"`
	// Tags is an optional comma-separated list of Falcon sensor grouping tags.
	Tags              string `json:"tags"`
	CID               string `json:"cid"`
	ProvisioningToken string `json:"provisioningToken"`
}

type ChartConfig struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Version    string `json:"version"`
}

// ImageConfig is the mirrored sensor image in Artifact Registry.
type ImageConfig struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Digest     string `json:"digest"`
}

// RegistryConfig names the Artifact Registry repository that mirror-image
// creates and copies into. The program uses it only to cross-check
// image.repository.
type RegistryConfig struct {
	Location   string `json:"location"`
	Repository string `json:"repository"`
}

// fieldCheck pairs a field name with "this check failed".
type fieldCheck = PR.Pair[string, bool]

// falconClouds are the region values the chart accepts for falcon.cloud.
var falconClouds = map[string]bool{
	"us-1": true, "us-2": true, "us-3": true,
	"eu-1": true, "us-gov-1": true, "us-gov-2": true,
}

func isUnknownCloud(cloud string) bool {
	return !falconClouds[cloud]
}

var (
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	cidPattern    = regexp.MustCompile(`^[0-9A-Fa-f]{32}-[0-9A-Fa-f]{2}$`)
	tagPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	isPlaceholder = P.Or(S.Includes("<"))(S.Includes(">"))
	isBadDigest   = P.Not(digestPattern.MatchString)
	isBadCID      = P.Not(cidPattern.MatchString)
	isBadTag      = P.Not(tagPattern.MatchString)
	isNotHTTPS    = P.Not(S.HasPrefix("https://"))
	matchString   = E.Eitherize2(regexp.MatchString)
	failedFields  = F.Flow2(
		A.Filter(PR.Tail[string, bool]),
		A.Map(PR.Head[string, bool]),
	)
)

// ReadConfig reads and validates `falcon-sensor:properties`.
func ReadConfig(ctx *pulumi.Context) (*FalconConfig, error) {
	cfg := &FalconConfig{}
	if err := config.New(ctx, configNamespace).TryObject("properties", cfg); err != nil {
		return nil, fmt.Errorf("failed to read falcon-sensor config: %w", err)
	}
	return cfg, cfg.Validate()
}

// Validate rejects `<...>` placeholders first, then invalid values. The
// error names fields only: it never prints the CID or the token.
func (c *FalconConfig) Validate() error {
	return F.Pipe3(
		E.Of[error](c),
		E.Chain(F.Bind12of3(rejectFields)("contains placeholder in", placeholderChecks)),
		E.Chain(F.Bind12of3(rejectFields)("is invalid in", invalidChecks)),
		E.ToError[*FalconConfig],
	)
}

// ImageRef is the digest-pinned image reference the DaemonSet pulls.
func (c *FalconConfig) ImageRef() string {
	return fmt.Sprintf("%s@%s", c.Image.Repository, c.Image.Digest)
}

func placeholderChecks(c *FalconConfig) []fieldCheck {
	return []fieldCheck{
		PR.MakePair("namespace", isPlaceholder(c.Namespace)),
		PR.MakePair("clusterName", isPlaceholder(c.ClusterName)),
		PR.MakePair("cloud", isPlaceholder(c.Cloud)),
		PR.MakePair("image.repository", isPlaceholder(c.Image.Repository)),
		PR.MakePair("image.tag", isPlaceholder(c.Image.Tag)),
		PR.MakePair("image.digest", isPlaceholder(c.Image.Digest)),
		PR.MakePair("cid", isPlaceholder(c.CID)),
		PR.MakePair("provisioningToken", isPlaceholder(c.ProvisioningToken)),
	}
}

func invalidChecks(c *FalconConfig) []fieldCheck {
	return []fieldCheck{
		PR.MakePair("namespace", S.IsEmpty(c.Namespace)),
		PR.MakePair("clusterName", S.IsEmpty(c.ClusterName)),
		PR.MakePair("cloud", isUnknownCloud(c.Cloud)),
		PR.MakePair("chart.name", c.Chart.Name != chartName),
		PR.MakePair("chart.repository", isNotHTTPS(c.Chart.Repository)),
		PR.MakePair("chart.version", S.IsEmpty(c.Chart.Version)),
		PR.MakePair("registry.location", S.IsEmpty(c.Registry.Location)),
		PR.MakePair("registry.repository", S.IsEmpty(c.Registry.Repository)),
		PR.MakePair("image.repository", isForeignRepository(c)),
		PR.MakePair("image.tag", isBadTag(c.Image.Tag)),
		PR.MakePair("image.digest", isBadDigest(c.Image.Digest)),
		PR.MakePair("cid", isBadCID(c.CID)),
	}
}

// isForeignRepository fails unless image.repository is the falcon-sensor
// image inside the registry.location / registry.repository Artifact
// Registry repository: <location>-docker.pkg.dev/<project>/<repo>/falcon-sensor.
func isForeignRepository(c *FalconConfig) bool {
	return F.Pipe2(
		repositoryPattern(c.Registry),
		F.Bind2nd(matchString, c.Image.Repository),
		E.Fold(F.Constant1[error](true), P.Not(F.Identity[bool])),
	)
}

// repositoryPattern anchors the expected image path. The GCP project segment
// follows the GCP project id rules.
func repositoryPattern(r RegistryConfig) string {
	return fmt.Sprintf(
		`^%s-docker\.pkg\.dev/[a-z][a-z0-9-]{4,28}[a-z0-9]/%s/%s$`,
		regexp.QuoteMeta(r.Location),
		regexp.QuoteMeta(r.Repository),
		imageName,
	)
}

// rejectFields fails with every field name whose check failed.
func rejectFields(
	reason string,
	checks func(*FalconConfig) []fieldCheck,
	c *FalconConfig,
) E.Either[error, *FalconConfig] {
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

func fieldError(reason, fields string) E.Either[error, *FalconConfig] {
	return E.Left[*FalconConfig](fmt.Errorf("falcon-sensor:properties %s %s", reason, fields))
}
