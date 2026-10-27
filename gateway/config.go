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
	defaultGatewayName      = "dictycr-gateway"
	defaultGatewayClassName = "cilium"
	defaultIssuerName       = "letsencrypt-issuer"
	defaultIssuerServer     = "https://acme-v02.api.letsencrypt.org/directory"
	//nolint:gosec // G101: Kubernetes Secret name pointer, not a credential.
	defaultIssuerSecretName  = "letsencrypt-account-key"
	defaultRootPath          = "/"
	defaultFrontendPort      = 3000
	defaultGraphqlService    = "graphql-api"
	defaultGraphqlPort       = 8080
	defaultMinioService      = "minio"
	defaultMinioPort         = 9000
	defaultLogtoService      = "logto-api"
	defaultLogtoPort         = 3001
	gatewayNetworkingVersion = "gateway.networking.k8s.io/v1"
	routeKeyFrontend         = "frontend"
	routeKeyGraphql          = "graphql"
	routeKeyMinio            = "minio"
	routeKeyLogto            = "logto"
	fieldNamespace           = "namespace"
	fieldName                = "name"
	fieldPort                = "port"
)

type GatewayConfig struct {
	Name             string       `json:"name"`
	Namespace        string       `json:"namespace"`
	GatewayClassName string       `json:"gatewayClassName"`
	Issuer           IssuerConfig `json:"issuer"`
	Routes           RoutesConfig `json:"routes"`
}

type IssuerConfig struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	Server     string `json:"server"`
	SecretName string `json:"secretName"`
}

type RoutesConfig struct {
	Frontend FrontendRouteConfig `json:"frontend"`
	Graphql  ServiceRouteConfig  `json:"graphql"`
	Minio    ServiceRouteConfig  `json:"minio"`
	Logto    ServiceRouteConfig  `json:"logto"`
}

type FrontendRouteConfig struct {
	Enabled   bool                     `json:"enabled"`
	Hosts     []string                 `json:"hosts"`
	TLSSecret string                   `json:"tlsSecret"`
	Services  []FrontendServiceBackend `json:"services"`
}

type FrontendServiceBackend struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	Path string `json:"path"`
}

type ServiceRouteConfig struct {
	Enabled     bool     `json:"enabled"`
	Hosts       []string `json:"hosts"`
	TLSSecret   string   `json:"tlsSecret"`
	ServiceName string   `json:"serviceName"`
	ServicePort int      `json:"servicePort"`
	Path        string   `json:"path"`
}

func ReadConfig(ctx *pulumi.Context) (*GatewayConfig, error) {
	conf := config.New(ctx, "")
	gwCfg := &GatewayConfig{}
	if err := conf.TryObject("properties", gwCfg); err != nil {
		return nil, fmt.Errorf("failed to read gateway config: %w", err)
	}
	gwCfg.ApplyDefaults()
	if err := gwCfg.Validate(); err != nil {
		return nil, err
	}
	return gwCfg, nil
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

func (c *GatewayConfig) ApplyDefaults() {
	c.Name = F.Pipe1(c.Name, defaultStr(defaultGatewayName))
	c.GatewayClassName = F.Pipe1(c.GatewayClassName, defaultStr(defaultGatewayClassName))
	c.Issuer.Name = F.Pipe1(c.Issuer.Name, defaultStr(defaultIssuerName))
	c.Issuer.Server = F.Pipe1(c.Issuer.Server, defaultStr(defaultIssuerServer))
	c.Issuer.SecretName = F.Pipe1(c.Issuer.SecretName, defaultStr(defaultIssuerSecretName))
	c.Routes.Graphql.applyServiceDefaults(defaultGraphqlService, defaultGraphqlPort, defaultRootPath)
	c.Routes.Minio.applyServiceDefaults(defaultMinioService, defaultMinioPort, defaultRootPath)
	c.Routes.Logto.applyServiceDefaults(defaultLogtoService, defaultLogtoPort, defaultRootPath)
	c.Routes.Frontend.Services = F.Pipe1(
		c.Routes.Frontend.Services,
		A.Map(applyFrontendBackendDefaults),
	)
}

func (r *ServiceRouteConfig) applyServiceDefaults(svc string, port int, path string) {
	r.ServiceName = F.Pipe1(r.ServiceName, defaultStr(svc))
	r.ServicePort = F.Pipe1(r.ServicePort, defaultInt(port))
	r.Path = F.Pipe1(r.Path, defaultStr(path))
}

func applyFrontendBackendDefaults(b FrontendServiceBackend) FrontendServiceBackend {
	b.Port = F.Pipe1(b.Port, defaultInt(defaultFrontendPort))
	b.Path = F.Pipe1(b.Path, defaultStr(defaultRootPath))
	return b
}

func (c *GatewayConfig) Validate() error {
	return F.Pipe1(
		validateGatewayConfig(c),
		E.ToError[*GatewayConfig],
	)
}

func validateGatewayConfig(c *GatewayConfig) E.Either[error, *GatewayConfig] {
	return F.Pipe5(
		E.Of[error](c),
		E.Chain(validateBaseFields),
		E.Chain(validateAtLeastOneRoute),
		E.Chain(validateFrontendRoute),
		E.Chain(validateNamedServiceRoute(routeKeyGraphql, func(cfg *GatewayConfig) ServiceRouteConfig {
			return cfg.Routes.Graphql
		})),
		E.Chain(validateRemainingServiceRoutes),
	)
}

func validateRemainingServiceRoutes(c *GatewayConfig) E.Either[error, *GatewayConfig] {
	return F.Pipe2(
		E.Of[error](c),
		E.Chain(validateNamedServiceRoute(routeKeyMinio, func(cfg *GatewayConfig) ServiceRouteConfig {
			return cfg.Routes.Minio
		})),
		E.Chain(validateNamedServiceRoute(routeKeyLogto, func(cfg *GatewayConfig) ServiceRouteConfig {
			return cfg.Routes.Logto
		})),
	)
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

func validateBaseFields(c *GatewayConfig) E.Either[error, *GatewayConfig] {
	fields := []namedField{
		{name: fieldNamespace, val: c.Namespace},
		{name: "issuer.email", val: c.Issuer.Email},
	}
	missing := missingFieldNames(fields)
	if len(missing) > 0 {
		return E.Left[*GatewayConfig](fmt.Errorf(
			"gateway:properties is missing %s",
			strings.Join(missing, ", "),
		))
	}
	placeholders := placeholderFieldNames([]namedField{
		{name: fieldNamespace, val: c.Namespace},
		{name: "issuer.email", val: c.Issuer.Email},
		{name: "issuer.secretName", val: c.Issuer.SecretName},
	})
	if len(placeholders) > 0 {
		return E.Left[*GatewayConfig](fmt.Errorf(
			"gateway:properties contains placeholder in %s",
			strings.Join(placeholders, ", "),
		))
	}
	return E.Right[error](c)
}

func anyRouteEnabled(c *GatewayConfig) bool {
	return c.Routes.Frontend.Enabled ||
		c.Routes.Graphql.Enabled ||
		c.Routes.Minio.Enabled ||
		c.Routes.Logto.Enabled
}

func validateAtLeastOneRoute(c *GatewayConfig) E.Either[error, *GatewayConfig] {
	return F.Pipe1(
		c,
		E.FromPredicate(
			anyRouteEnabled,
			func(*GatewayConfig) error {
				return fmt.Errorf("gateway:properties must enable at least one route")
			},
		),
	)
}

func validateRouteHostsAndSecret(
	routeKey string,
	hosts []string,
	tlsSecret string,
	extraMissing []string,
) error {
	missing := make([]string, 0, 3)
	if len(hosts) == 0 || F.Pipe1(hosts, A.Any(S.IsEmpty)) {
		missing = append(missing, "hosts")
	}
	if S.IsEmpty(tlsSecret) {
		missing = append(missing, "tlsSecret")
	}
	missing = append(missing, extraMissing...)
	if len(missing) > 0 {
		return fmt.Errorf(
			"routes.%s.enabled=true but routes.%s is missing %s",
			routeKey,
			routeKey,
			strings.Join(missing, ", "),
		)
	}
	placeholders := make([]string, 0, 2)
	if F.Pipe1(hosts, A.Any(hasAnglePlaceholder)) {
		placeholders = append(placeholders, "hosts")
	}
	if hasAnglePlaceholder(tlsSecret) {
		placeholders = append(placeholders, "tlsSecret")
	}
	if len(placeholders) > 0 {
		return fmt.Errorf(
			"routes.%s.enabled=true but routes.%s contains placeholder in %s",
			routeKey,
			routeKey,
			strings.Join(placeholders, ", "),
		)
	}
	return nil
}

func isInvalidFrontendBackend(b FrontendServiceBackend) bool {
	return S.IsEmpty(b.Name) || S.IsEmpty(b.Path) || b.Port <= 0
}

func validateFrontendRoute(c *GatewayConfig) E.Either[error, *GatewayConfig] {
	fe := c.Routes.Frontend
	if !fe.Enabled {
		return E.Right[error](c)
	}
	var extra []string
	if len(fe.Services) == 0 || F.Pipe1(fe.Services, A.Any(isInvalidFrontendBackend)) {
		extra = append(extra, "services")
	}
	if err := validateRouteHostsAndSecret(routeKeyFrontend, fe.Hosts, fe.TLSSecret, extra); err != nil {
		return E.Left[*GatewayConfig](err)
	}
	return E.Right[error](c)
}

func validateNamedServiceRoute(
	routeKey string,
	extract func(*GatewayConfig) ServiceRouteConfig,
) func(*GatewayConfig) E.Either[error, *GatewayConfig] {
	return func(c *GatewayConfig) E.Either[error, *GatewayConfig] {
		rt := extract(c)
		if !rt.Enabled {
			return E.Right[error](c)
		}
		var extra []string
		if S.IsEmpty(rt.ServiceName) {
			extra = append(extra, "serviceName")
		}
		if rt.ServicePort <= 0 {
			extra = append(extra, "servicePort")
		}
		if err := validateRouteHostsAndSecret(routeKey, rt.Hosts, rt.TLSSecret, extra); err != nil {
			return E.Left[*GatewayConfig](err)
		}
		return E.Right[error](c)
	}
}
