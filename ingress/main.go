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
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	networkingv1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/networking/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	ingressKeyFrontend = "frontend"
	ingressKeyAPI      = "api"
	defaultStockSvc    = "stock-api"
	defaultStockPort   = 9345
	defaultStockPath   = "/v1/stocks"
)

// Ingresses holds the configuration for the cluster Ingress resources.
type Ingresses struct {
	Config *Config
}

// Config represents the overall configuration for the Ingress resources.
type Config struct {
	Namespace string        `json:"namespace"`
	Frontend  IngressConfig `json:"frontend"`
	API       IngressConfig `json:"api"`
}

// ServiceBackend describes one path-prefix backend Service rule.
type ServiceBackend struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	Path string `json:"path"`
}

// LabelConfig holds a single metadata label key-value pair.
type LabelConfig struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// IngressConfig holds the configuration for a single Ingress resource.
type IngressConfig struct {
	Enabled  *bool            `json:"enabled"`
	Hosts    []string         `json:"hosts"`
	Label    LabelConfig      `json:"label"`
	Secret   string           `json:"secret"`
	Services []ServiceBackend `json:"services"`
}

func main() {
	pulumi.Run(Run)
}

// Run reads the stack configuration and creates all enabled Ingress resources.
func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return NewIngresses(cfg).Install(ctx)
}

// NewIngresses constructs an Ingresses installer from a validated Config.
func NewIngresses(cfg *Config) *Ingresses {
	return &Ingresses{Config: cfg}
}

// Install creates each enabled Ingress resource in the configured namespace.
func (ing *Ingresses) Install(ctx *pulumi.Context) error {
	for _, named := range []struct {
		key string
		cfg IngressConfig
	}{
		{key: ingressKeyFrontend, cfg: ing.Config.Frontend},
		{key: ingressKeyAPI, cfg: ing.Config.API},
	} {
		if !named.cfg.IsEnabled() {
			continue
		}
		if err := createIngress(ctx, named.key, ing.Config.Namespace, named.cfg); err != nil {
			return err
		}
	}
	return nil
}

// ReadConfig reads, defaults, and validates the Pulumi stack configuration.
func ReadConfig(ctx *pulumi.Context) (*Config, error) {
	conf := config.New(ctx, "")
	ingressConfig := &Config{}
	if err := conf.TryObject("properties", ingressConfig); err != nil {
		return nil, fmt.Errorf("failed to read ingress config: %w", err)
	}
	ingressConfig.ApplyDefaults()
	if err := ingressConfig.Validate(); err != nil {
		return nil, err
	}
	return ingressConfig, nil
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

func defaultAPIServices(services []ServiceBackend) []ServiceBackend {
	return F.Pipe2(
		services,
		O.FromPredicate(A.IsNonEmpty[ServiceBackend]),
		O.GetOrElse(F.Constant([]ServiceBackend{
			{
				Name: defaultStockSvc,
				Port: defaultStockPort,
				Path: defaultStockPath,
			},
		})),
	)
}

// ApplyDefaults populates default toggles and default API service backends.
func (c *Config) ApplyDefaults() {
	c.Frontend.Enabled = F.Pipe1(c.Frontend.Enabled, defaultBoolPtr(true))
	c.API.Enabled = F.Pipe1(c.API.Enabled, defaultBoolPtr(false))
	c.API.Services = F.Pipe1(c.API.Services, defaultAPIServices)
}

// IsEnabled reports whether this IngressConfig is enabled.
func (ic IngressConfig) IsEnabled() bool {
	return F.Pipe2(
		ic.Enabled,
		O.FromNillable2[bool],
		O.GetOrElse(F.Constant(false)),
	)
}

// Validate checks that the configuration has a valid namespace and valid enabled Ingress blocks.
func (c *Config) Validate() error {
	return F.Pipe5(
		E.Of[error](c),
		E.Chain(validateNamespace),
		E.Chain(validateAtLeastOneIngress),
		E.Chain(validateNamedIngress(ingressKeyFrontend, extractFrontendIngress)),
		E.Chain(validateNamedIngress(ingressKeyAPI, extractAPIIngress)),
		E.ToError[*Config],
	)
}

func extractFrontendIngress(c *Config) IngressConfig {
	return c.Frontend
}

func extractAPIIngress(c *Config) IngressConfig {
	return c.API
}

func hasAnglePlaceholder(s string) bool {
	return strings.Contains(s, "<") || strings.Contains(s, ">")
}

func validateNamespace(c *Config) E.Either[error, *Config] {
	if S.IsEmpty(c.Namespace) {
		return E.Left[*Config](fmt.Errorf("ingress:properties is missing namespace"))
	}
	if hasAnglePlaceholder(c.Namespace) {
		return E.Left[*Config](fmt.Errorf("ingress:properties contains placeholder in namespace"))
	}
	return E.Right[error](c)
}

func anyIngressEnabled(c *Config) bool {
	return c.Frontend.IsEnabled() || c.API.IsEnabled()
}

func validateAtLeastOneIngress(c *Config) E.Either[error, *Config] {
	return F.Pipe1(
		c,
		E.FromPredicate(
			anyIngressEnabled,
			func(*Config) error {
				return fmt.Errorf("ingress:properties must enable at least one ingress")
			},
		),
	)
}

func isInvalidServiceBackend(b ServiceBackend) bool {
	return S.IsEmpty(b.Name) || S.IsEmpty(b.Path) || b.Port <= 0
}

func validateIngressFields(key string, ic IngressConfig) error {
	missing := missingIngressFields(ic)
	if len(missing) > 0 {
		return fmt.Errorf(
			"%s.enabled=true but %s is missing %s",
			key,
			key,
			strings.Join(missing, ", "),
		)
	}
	placeholders := placeholderIngressFields(ic)
	if len(placeholders) > 0 {
		return fmt.Errorf(
			"%s.enabled=true but %s contains placeholder in %s",
			key,
			key,
			strings.Join(placeholders, ", "),
		)
	}
	return nil
}

func missingIngressFields(ic IngressConfig) []string {
	var missing []string
	if len(ic.Hosts) == 0 || F.Pipe1(ic.Hosts, A.Any(S.IsEmpty)) {
		missing = append(missing, "hosts")
	}
	if S.IsEmpty(ic.Secret) {
		missing = append(missing, "secret")
	}
	if len(ic.Services) == 0 || F.Pipe1(ic.Services, A.Any(isInvalidServiceBackend)) {
		missing = append(missing, "services")
	}
	return missing
}

func placeholderIngressFields(ic IngressConfig) []string {
	var placeholders []string
	if F.Pipe1(ic.Hosts, A.Any(hasAnglePlaceholder)) {
		placeholders = append(placeholders, "hosts")
	}
	if hasAnglePlaceholder(ic.Secret) {
		placeholders = append(placeholders, "secret")
	}
	return placeholders
}

func validateNamedIngress(
	key string,
	extract func(*Config) IngressConfig,
) func(*Config) E.Either[error, *Config] {
	return func(c *Config) E.Either[error, *Config] {
		ic := extract(c)
		if !ic.IsEnabled() {
			return E.Right[error](c)
		}
		if err := validateIngressFields(key, ic); err != nil {
			return E.Left[*Config](err)
		}
		return E.Right[error](c)
	}
}

func createIngress(
	ctx *pulumi.Context,
	name string,
	namespace string,
	cfg IngressConfig,
) error {
	ingressName := fmt.Sprintf("%s-ingress", name)
	_, err := networkingv1.NewIngress(
		ctx,
		ingressName,
		&networkingv1.IngressArgs{
			Metadata: createIngressMetadata(ingressName, namespace, cfg),
			Spec:     createIngressSpec(cfg),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create %s ingress: %w", name, err)
	}
	return nil
}

func hasLabelName(l LabelConfig) bool {
	return S.IsNonEmpty(l.Name)
}

func toLabelMap(l LabelConfig) pulumi.StringMap {
	return pulumi.StringMap{
		l.Name: pulumi.String(l.Value),
	}
}

var buildIngressLabels = F.Pipe1(
	hasLabelName,
	P.Fold(F.Constant1[LabelConfig, pulumi.StringMap](nil), toLabelMap),
)

func createIngressMetadata(
	ingressName string,
	namespace string,
	cfg IngressConfig,
) *metav1.ObjectMetaArgs {
	return &metav1.ObjectMetaArgs{
		Name:      pulumi.String(ingressName),
		Namespace: pulumi.String(namespace),
		Labels:    buildIngressLabels(cfg.Label),
	}
}

func createIngressSpec(cfg IngressConfig) *networkingv1.IngressSpecArgs {
	return &networkingv1.IngressSpecArgs{
		IngressClassName: pulumi.String("nginx"),
		Tls: networkingv1.IngressTLSArray{
			&networkingv1.IngressTLSArgs{
				Hosts:      pulumi.ToStringArray(cfg.Hosts),
				SecretName: pulumi.String(cfg.Secret),
			},
		},
		Rules: createIngressRuleArray(cfg),
	}
}

func toHTTPIngressPath(service ServiceBackend) networkingv1.HTTPIngressPathInput {
	return &networkingv1.HTTPIngressPathArgs{
		Path:     pulumi.String(service.Path),
		PathType: pulumi.String("Prefix"),
		Backend: &networkingv1.IngressBackendArgs{
			Service: &networkingv1.IngressServiceBackendArgs{
				Name: pulumi.String(service.Name),
				Port: &networkingv1.ServiceBackendPortArgs{
					Number: pulumi.Int(service.Port),
				},
			},
		},
	}
}

func buildHostIngressRule(
	paths networkingv1.HTTPIngressPathArray,
) func(string) networkingv1.IngressRuleInput {
	return func(host string) networkingv1.IngressRuleInput {
		return &networkingv1.IngressRuleArgs{
			Host: pulumi.String(host),
			Http: &networkingv1.HTTPIngressRuleValueArgs{
				Paths: paths,
			},
		}
	}
}

func createIngressRuleArray(cfg IngressConfig) networkingv1.IngressRuleArray {
	paths := networkingv1.HTTPIngressPathArray(
		F.Pipe1(cfg.Services, A.Map(toHTTPIngressPath)),
	)
	toRule := buildHostIngressRule(paths)
	return networkingv1.IngressRuleArray(
		F.Pipe1(cfg.Hosts, A.Map(toRule)),
	)
}
