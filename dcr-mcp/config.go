package main

import (
	"fmt"
	"strings"

	A "github.com/IBM/fp-go/v2/array"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	S "github.com/IBM/fp-go/v2/string"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	defaultAppName        = "dcr-mcp"
	defaultImageName      = "dictybase/dcr-mcp"
	defaultCommand        = "start-server"
	defaultHost           = "0.0.0.0"
	defaultPort           = 8080
	defaultRateLimit      = 120
	defaultWildcardOrigin = "*"
)

type DcrMcpConfig struct {
	Name            string        `json:"name"`
	Namespace       string        `json:"namespace"`
	Command         string        `json:"command"`
	Host            string        `json:"host"`
	Port            int           `json:"port"`
	Stateless       *bool         `json:"stateless"`
	RateLimit       int           `json:"rateLimit"`
	GraphQLEndpoint string        `json:"graphqlEndpoint"`
	AllowedOrigins  []string      `json:"allowedOrigins"`
	Image           ImageConfig   `json:"image"`
	Ingress         IngressConfig `json:"ingress"`
}

type ImageConfig struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

type IngressConfig struct {
	Enabled   bool        `json:"enabled"`
	Hosts     []string    `json:"hosts"`
	Path      string      `json:"path"`
	Label     LabelConfig `json:"label"`
	TLSSecret string      `json:"tlsSecret"`
}

type LabelConfig struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type namedField struct {
	name string
	val  string
}

func isMissingField(f namedField) bool {
	return S.IsEmpty(f.val)
}

func hasAnglePlaceholder(s string) bool {
	return strings.Contains(s, "<") || strings.Contains(s, ">")
}

func fieldHasPlaceholder(f namedField) bool {
	return hasAnglePlaceholder(f.val)
}

func extractFieldName(f namedField) string {
	return f.name
}

func missingFieldNames(fields []namedField) []string {
	return F.Pipe2(
		fields,
		A.Filter(isMissingField),
		A.Map(extractFieldName),
	)
}

func placeholderFieldNames(fields []namedField) []string {
	return F.Pipe2(
		fields,
		A.Filter(fieldHasPlaceholder),
		A.Map(extractFieldName),
	)
}

func defaultStr(fallback string) func(string) string {
	return F.Flow2(
		O.FromPredicate(S.IsNonEmpty),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func defaultInt(fallback int) func(int) int {
	return F.Flow2(
		O.FromPredicate(P.IsNonZero[int]()),
		O.GetOrElse(F.Constant(fallback)),
	)
}

func defaultBoolPtr(fallback bool) func(*bool) *bool {
	return F.Flow2(
		O.FromNillable2[bool],
		O.Fold(
			func() *bool { return new(fallback) },
			func(v bool) *bool { return new(v) },
		),
	)
}

func defaultOrigins(origins []string) []string {
	return F.Pipe2(
		origins,
		O.FromPredicate(A.IsNonEmpty[string]),
		O.GetOrElse(F.Constant([]string{defaultWildcardOrigin})),
	)
}

func ReadConfig(ctx *pulumi.Context) (*DcrMcpConfig, error) {
	conf := config.New(ctx, "dcr-mcp")
	mcpCfg := &DcrMcpConfig{}
	if err := conf.TryObject("properties", mcpCfg); err != nil {
		return nil, fmt.Errorf("failed to read dcr-mcp config: %w", err)
	}
	mcpCfg.ApplyDefaults()
	if err := mcpCfg.Validate(); err != nil {
		return nil, err
	}
	return mcpCfg, nil
}

func (c *DcrMcpConfig) ApplyDefaults() {
	c.Name = F.Pipe1(c.Name, defaultStr(defaultAppName))
	c.Command = F.Pipe1(c.Command, defaultStr(defaultCommand))
	c.Host = F.Pipe1(c.Host, defaultStr(defaultHost))
	c.Port = F.Pipe1(c.Port, defaultInt(defaultPort))
	c.RateLimit = F.Pipe1(c.RateLimit, defaultInt(defaultRateLimit))
	c.Stateless = F.Pipe1(c.Stateless, defaultBoolPtr(true))
	c.Image.Name = F.Pipe1(c.Image.Name, defaultStr(defaultImageName))
	c.AllowedOrigins = F.Pipe1(c.AllowedOrigins, defaultOrigins)
}

func (c *DcrMcpConfig) StatelessValue() bool {
	return F.Pipe2(
		c.Stateless,
		O.FromNillable2[bool],
		O.GetOrElse(F.Constant(true)),
	)
}

func (c *DcrMcpConfig) Validate() error {
	return F.Pipe1(
		validateDcrMcpConfig(c),
		E.ToError[*DcrMcpConfig],
	)
}

func validateDcrMcpConfig(c *DcrMcpConfig) E.Either[error, *DcrMcpConfig] {
	return F.Pipe2(
		E.Of[error](c),
		E.Chain(validateBase),
		E.Chain(validateIngress),
	)
}

func validateBase(c *DcrMcpConfig) E.Either[error, *DcrMcpConfig] {
	fields := []namedField{
		{name: "namespace", val: c.Namespace},
		{name: "image.tag", val: c.Image.Tag},
		{name: "graphqlEndpoint", val: c.GraphQLEndpoint},
	}
	missing := missingFieldNames(fields)
	if len(missing) > 0 {
		return E.Left[*DcrMcpConfig](fmt.Errorf(
			"dcr-mcp:properties is missing %s",
			strings.Join(missing, ", "),
		))
	}
	placeholders := placeholderFieldNames(fields)
	if len(placeholders) > 0 {
		return E.Left[*DcrMcpConfig](fmt.Errorf(
			"dcr-mcp:properties contains placeholder in %s",
			strings.Join(placeholders, ", "),
		))
	}
	return E.Right[error](c)
}

func validateIngress(c *DcrMcpConfig) E.Either[error, *DcrMcpConfig] {
	if !c.Ingress.Enabled {
		return E.Right[error](c)
	}
	var missing []string
	if len(c.Ingress.Hosts) == 0 || F.Pipe1(c.Ingress.Hosts, A.Any(S.IsEmpty)) {
		missing = append(missing, "hosts")
	}
	if S.IsEmpty(c.Ingress.Path) {
		missing = append(missing, "path")
	}
	if S.IsEmpty(c.Ingress.TLSSecret) {
		missing = append(missing, "tlsSecret")
	}
	if len(missing) > 0 {
		return E.Left[*DcrMcpConfig](fmt.Errorf(
			"ingress.enabled=true but ingress is missing %s",
			strings.Join(missing, ", "),
		))
	}
	var placeholders []string
	if F.Pipe1(c.Ingress.Hosts, A.Any(hasAnglePlaceholder)) {
		placeholders = append(placeholders, "hosts")
	}
	if hasAnglePlaceholder(c.Ingress.TLSSecret) {
		placeholders = append(placeholders, "tlsSecret")
	}
	if len(placeholders) > 0 {
		return E.Left[*DcrMcpConfig](fmt.Errorf(
			"ingress.enabled=true but ingress contains placeholder in %s",
			strings.Join(placeholders, ", "),
		))
	}
	return E.Right[error](c)
}
