package main

import (
	"fmt"
	"strings"

	A "github.com/IBM/fp-go/v2/array"
	F "github.com/IBM/fp-go/v2/function"
	L "github.com/IBM/fp-go/v2/optics/lens"
	O "github.com/IBM/fp-go/v2/option"
	P "github.com/IBM/fp-go/v2/predicate"
	R "github.com/IBM/fp-go/v2/record"
	S "github.com/IBM/fp-go/v2/string"
	appsv1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/apps/v1"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v3/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type issueEnvSpec struct {
	envName    string
	secretName string
	key        string
	optional   bool
}

var (
	emnConfigLens = L.MakeLens(
		func(e *EventMessenger) *EventMessengerConfig { return e.Config },
		func(e *EventMessenger, v *EventMessengerConfig) *EventMessenger {
			e.Config = v
			return e
		},
	)

	cfgNamespaceLens = L.MakeLensRef(
		func(c *EventMessengerConfig) string { return c.Namespace },
		func(c *EventMessengerConfig, v string) *EventMessengerConfig {
			c.Namespace = v
			return c
		},
	)

	cfgLogLevelLens = L.MakeLensRef(
		func(c *EventMessengerConfig) string { return c.LogLevel },
		func(c *EventMessengerConfig, v string) *EventMessengerConfig {
			c.LogLevel = v
			return c
		},
	)

	cfgNatsLens = L.MakeLensRef(
		func(c *EventMessengerConfig) NatsProperties { return c.Nats },
		func(c *EventMessengerConfig, v NatsProperties) *EventMessengerConfig {
			c.Nats = v
			return c
		},
	)

	natsSubjectLens = L.MakeLens(
		func(n NatsProperties) string { return n.Subject },
		func(n NatsProperties, v string) NatsProperties {
			n.Subject = v
			return n
		},
	)

	cfgImageLens = L.MakeLensRef(
		func(c *EventMessengerConfig) ImageConfig { return c.Image },
		func(c *EventMessengerConfig, v ImageConfig) *EventMessengerConfig {
			c.Image = v
			return c
		},
	)

	imgNameLens = L.MakeLens(
		func(i ImageConfig) string { return i.Name },
		func(i ImageConfig, v string) ImageConfig {
			i.Name = v
			return i
		},
	)

	imgTagLens = L.MakeLens(
		func(i ImageConfig) string { return i.Tag },
		func(i ImageConfig, v string) ImageConfig {
			i.Tag = v
			return i
		},
	)

	imgPullPolicyLens = L.MakeLens(
		func(i ImageConfig) string { return i.PullPolicy },
		func(i ImageConfig, v string) ImageConfig {
			i.PullPolicy = v
			return i
		},
	)

	cfgIssueDepLens = L.MakeLensRef(
		func(c *EventMessengerConfig) IssueDeployment { return c.IssueDeployment },
		func(c *EventMessengerConfig, v IssueDeployment) *EventMessengerConfig {
			c.IssueDeployment = v
			return c
		},
	)

	issueDepNameLens = L.MakeLens(
		func(d IssueDeployment) string { return d.Name },
		func(d IssueDeployment, v string) IssueDeployment {
			d.Name = v
			return d
		},
	)

	issueDepSecretsLens = L.MakeLens(
		func(d IssueDeployment) IssueSecrets { return d.Secrets },
		func(d IssueDeployment, v IssueSecrets) IssueDeployment {
			d.Secrets = v
			return d
		},
	)

	issueSecretsNameLens = L.MakeLens(
		func(s IssueSecrets) string { return s.Name },
		func(s IssueSecrets, v string) IssueSecrets {
			s.Name = v
			return s
		},
	)

	issueSecretsKeysLens = L.MakeLens(
		func(s IssueSecrets) IssueSecretKeys { return s.Keys },
		func(s IssueSecrets, v IssueSecretKeys) IssueSecrets {
			s.Keys = v
			return s
		},
	)

	issueSecretsValuesLens = L.MakeLens(
		func(s IssueSecrets) IssueSecretValues { return s.Values },
		func(s IssueSecrets, v IssueSecretValues) IssueSecrets {
			s.Values = v
			return s
		},
	)

	issueKeysOwnerLens = L.MakeLens(
		func(k IssueSecretKeys) string { return k.Owner },
		func(k IssueSecretKeys, v string) IssueSecretKeys {
			k.Owner = v
			return k
		},
	)

	issueKeysRepoLens = L.MakeLens(
		func(k IssueSecretKeys) string { return k.Repository },
		func(k IssueSecretKeys, v string) IssueSecretKeys {
			k.Repository = v
			return k
		},
	)

	issueKeysTokenLens = L.MakeLens(
		func(k IssueSecretKeys) string { return k.Token },
		func(k IssueSecretKeys, v string) IssueSecretKeys {
			k.Token = v
			return k
		},
	)

	issueKeysOpenRouterLens = L.MakeLens(
		func(k IssueSecretKeys) string { return k.OpenRouterAPIKey },
		func(k IssueSecretKeys, v string) IssueSecretKeys {
			k.OpenRouterAPIKey = v
			return k
		},
	)

	issueValsOwnerLens = L.MakeLens(
		func(v IssueSecretValues) string { return v.Owner },
		func(v IssueSecretValues, x string) IssueSecretValues {
			v.Owner = x
			return v
		},
	)

	issueValsRepoLens = L.MakeLens(
		func(v IssueSecretValues) string { return v.Repository },
		func(v IssueSecretValues, x string) IssueSecretValues {
			v.Repository = x
			return v
		},
	)

	issueValsTokenLens = L.MakeLens(
		func(v IssueSecretValues) string { return v.Token },
		func(v IssueSecretValues, x string) IssueSecretValues {
			v.Token = x
			return v
		},
	)

	issueValsOpenRouterLens = L.MakeLens(
		func(v IssueSecretValues) string { return v.OpenRouterAPIKey },
		func(v IssueSecretValues, x string) IssueSecretValues {
			v.OpenRouterAPIKey = x
			return v
		},
	)

	envSpecNameLens = L.MakeLens(
		func(s issueEnvSpec) string { return s.envName },
		func(s issueEnvSpec, v string) issueEnvSpec {
			s.envName = v
			return s
		},
	)

	envSpecSecretLens = L.MakeLens(
		func(s issueEnvSpec) string { return s.secretName },
		func(s issueEnvSpec, v string) issueEnvSpec {
			s.secretName = v
			return s
		},
	)

	envSpecKeyLens = L.MakeLens(
		func(s issueEnvSpec) string { return s.key },
		func(s issueEnvSpec, v string) issueEnvSpec {
			s.key = v
			return s
		},
	)

	envSpecOptLens = L.MakeLens(
		func(s issueEnvSpec) bool { return s.optional },
		func(s issueEnvSpec, v bool) issueEnvSpec {
			s.optional = v
			return s
		},
	)

	objMetaNameLens = L.MakeLensRef(
		func(m *metav1.ObjectMetaArgs) pulumi.StringPtrInput { return m.Name },
		func(m *metav1.ObjectMetaArgs, v pulumi.StringPtrInput) *metav1.ObjectMetaArgs {
			m.Name = v
			return m
		},
	)

	objMetaNamespaceLens = L.MakeLensRef(
		func(m *metav1.ObjectMetaArgs) pulumi.StringPtrInput { return m.Namespace },
		func(m *metav1.ObjectMetaArgs, v pulumi.StringPtrInput) *metav1.ObjectMetaArgs {
			m.Namespace = v
			return m
		},
	)

	objMetaLabelsLens = L.MakeLensRef(
		func(m *metav1.ObjectMetaArgs) pulumi.StringMapInput { return m.Labels },
		func(m *metav1.ObjectMetaArgs, v pulumi.StringMapInput) *metav1.ObjectMetaArgs {
			m.Labels = v
			return m
		},
	)

	secretMetaLens = L.MakeLensRef(
		func(a *corev1.SecretArgs) metav1.ObjectMetaPtrInput { return a.Metadata },
		func(a *corev1.SecretArgs, v metav1.ObjectMetaPtrInput) *corev1.SecretArgs {
			a.Metadata = v
			return a
		},
	)

	secretStringDataLens = L.MakeLensRef(
		func(a *corev1.SecretArgs) pulumi.StringMapInput { return a.StringData },
		func(a *corev1.SecretArgs, v pulumi.StringMapInput) *corev1.SecretArgs {
			a.StringData = v
			return a
		},
	)

	keySelNameLens = L.MakeLensRef(
		func(s *corev1.SecretKeySelectorArgs) pulumi.StringPtrInput { return s.Name },
		func(
			s *corev1.SecretKeySelectorArgs,
			v pulumi.StringPtrInput,
		) *corev1.SecretKeySelectorArgs {
			s.Name = v
			return s
		},
	)

	keySelKeyLens = L.MakeLensRef(
		func(s *corev1.SecretKeySelectorArgs) pulumi.StringInput { return s.Key },
		func(
			s *corev1.SecretKeySelectorArgs,
			v pulumi.StringInput,
		) *corev1.SecretKeySelectorArgs {
			s.Key = v
			return s
		},
	)

	keySelOptionalLens = L.MakeLensRef(
		func(s *corev1.SecretKeySelectorArgs) pulumi.BoolPtrInput { return s.Optional },
		func(
			s *corev1.SecretKeySelectorArgs,
			v pulumi.BoolPtrInput,
		) *corev1.SecretKeySelectorArgs {
			s.Optional = v
			return s
		},
	)

	envSourceKeyRefLens = L.MakeLensRef(
		func(s *corev1.EnvVarSourceArgs) corev1.SecretKeySelectorPtrInput {
			return s.SecretKeyRef
		},
		func(
			s *corev1.EnvVarSourceArgs,
			v corev1.SecretKeySelectorPtrInput,
		) *corev1.EnvVarSourceArgs {
			s.SecretKeyRef = v
			return s
		},
	)

	envVarNameLens = L.MakeLensRef(
		func(e *corev1.EnvVarArgs) pulumi.StringInput { return e.Name },
		func(e *corev1.EnvVarArgs, v pulumi.StringInput) *corev1.EnvVarArgs {
			e.Name = v
			return e
		},
	)

	envVarValueFromLens = L.MakeLensRef(
		func(e *corev1.EnvVarArgs) corev1.EnvVarSourcePtrInput { return e.ValueFrom },
		func(
			e *corev1.EnvVarArgs,
			v corev1.EnvVarSourcePtrInput,
		) *corev1.EnvVarArgs {
			e.ValueFrom = v
			return e
		},
	)

	containerNameLens = L.MakeLensRef(
		func(c *corev1.ContainerArgs) pulumi.StringInput { return c.Name },
		func(c *corev1.ContainerArgs, v pulumi.StringInput) *corev1.ContainerArgs {
			c.Name = v
			return c
		},
	)

	containerImageLens = L.MakeLensRef(
		func(c *corev1.ContainerArgs) pulumi.StringPtrInput { return c.Image },
		func(
			c *corev1.ContainerArgs,
			v pulumi.StringPtrInput,
		) *corev1.ContainerArgs {
			c.Image = v
			return c
		},
	)

	containerPullPolicyLens = L.MakeLensRef(
		func(c *corev1.ContainerArgs) pulumi.StringPtrInput {
			return c.ImagePullPolicy
		},
		func(
			c *corev1.ContainerArgs,
			v pulumi.StringPtrInput,
		) *corev1.ContainerArgs {
			c.ImagePullPolicy = v
			return c
		},
	)

	containerArgsLens = L.MakeLensRef(
		func(c *corev1.ContainerArgs) pulumi.StringArrayInput { return c.Args },
		func(
			c *corev1.ContainerArgs,
			v pulumi.StringArrayInput,
		) *corev1.ContainerArgs {
			c.Args = v
			return c
		},
	)

	containerEnvLens = L.MakeLensRef(
		func(c *corev1.ContainerArgs) corev1.EnvVarArrayInput { return c.Env },
		func(
			c *corev1.ContainerArgs,
			v corev1.EnvVarArrayInput,
		) *corev1.ContainerArgs {
			c.Env = v
			return c
		},
	)

	podSpecContainersLens = L.MakeLensRef(
		func(p *corev1.PodSpecArgs) corev1.ContainerArrayInput { return p.Containers },
		func(
			p *corev1.PodSpecArgs,
			v corev1.ContainerArrayInput,
		) *corev1.PodSpecArgs {
			p.Containers = v
			return p
		},
	)

	podTmplMetaLens = L.MakeLensRef(
		func(t *corev1.PodTemplateSpecArgs) metav1.ObjectMetaPtrInput {
			return t.Metadata
		},
		func(
			t *corev1.PodTemplateSpecArgs,
			v metav1.ObjectMetaPtrInput,
		) *corev1.PodTemplateSpecArgs {
			t.Metadata = v
			return t
		},
	)

	podTmplSpecLens = L.MakeLensRef(
		func(t *corev1.PodTemplateSpecArgs) corev1.PodSpecPtrInput { return t.Spec },
		func(
			t *corev1.PodTemplateSpecArgs,
			v corev1.PodSpecPtrInput,
		) *corev1.PodTemplateSpecArgs {
			t.Spec = v
			return t
		},
	)

	labelSelMatchLens = L.MakeLensRef(
		func(l *metav1.LabelSelectorArgs) pulumi.StringMapInput {
			return l.MatchLabels
		},
		func(
			l *metav1.LabelSelectorArgs,
			v pulumi.StringMapInput,
		) *metav1.LabelSelectorArgs {
			l.MatchLabels = v
			return l
		},
	)

	depSpecSelectorLens = L.MakeLensRef(
		func(s *appsv1.DeploymentSpecArgs) metav1.LabelSelectorInput {
			return s.Selector
		},
		func(
			s *appsv1.DeploymentSpecArgs,
			v metav1.LabelSelectorInput,
		) *appsv1.DeploymentSpecArgs {
			s.Selector = v
			return s
		},
	)

	depSpecTmplLens = L.MakeLensRef(
		func(s *appsv1.DeploymentSpecArgs) corev1.PodTemplateSpecInput {
			return s.Template
		},
		func(
			s *appsv1.DeploymentSpecArgs,
			v corev1.PodTemplateSpecInput,
		) *appsv1.DeploymentSpecArgs {
			s.Template = v
			return s
		},
	)

	depArgsMetaLens = L.MakeLensRef(
		func(a *appsv1.DeploymentArgs) metav1.ObjectMetaPtrInput {
			return a.Metadata
		},
		func(
			a *appsv1.DeploymentArgs,
			v metav1.ObjectMetaPtrInput,
		) *appsv1.DeploymentArgs {
			a.Metadata = v
			return a
		},
	)

	depArgsSpecLens = L.MakeLensRef(
		func(a *appsv1.DeploymentArgs) appsv1.DeploymentSpecPtrInput {
			return a.Spec
		},
		func(
			a *appsv1.DeploymentArgs,
			v appsv1.DeploymentSpecPtrInput,
		) *appsv1.DeploymentArgs {
			a.Spec = v
			return a
		},
	)

	customTimeoutsCreateLens = L.MakeLensRef(
		func(t *pulumi.CustomTimeouts) string { return t.Create },
		func(t *pulumi.CustomTimeouts, v string) *pulumi.CustomTimeouts {
			t.Create = v
			return t
		},
	)

	emnIssueNameLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgIssueDepLens),
		L.Compose[*EventMessenger](issueDepNameLens),
	)

	emnLogLevelLens = F.Pipe1(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgLogLevelLens),
	)

	emnSubjectLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgNatsLens),
		L.Compose[*EventMessenger](natsSubjectLens),
	)

	emnImageNameLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgImageLens),
		L.Compose[*EventMessenger](imgNameLens),
	)

	emnImageTagLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgImageLens),
		L.Compose[*EventMessenger](imgTagLens),
	)

	emnImagePullPolicyLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgImageLens),
		L.Compose[*EventMessenger](imgPullPolicyLens),
	)

	emnNamespaceLens = F.Pipe1(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgNamespaceLens),
	)

	emnIssueSecretsLens = F.Pipe2(
		emnConfigLens,
		L.Compose[*EventMessenger](cfgIssueDepLens),
		L.Compose[*EventMessenger](issueDepSecretsLens),
	)

	emnIssueSecretNameLens = F.Pipe1(
		emnIssueSecretsLens,
		L.Compose[*EventMessenger](issueSecretsNameLens),
	)

	emnIssueOwnerKeyLens = F.Pipe2(
		emnIssueSecretsLens,
		L.Compose[*EventMessenger](issueSecretsKeysLens),
		L.Compose[*EventMessenger](issueKeysOwnerLens),
	)

	emnIssueRepoKeyLens = F.Pipe2(
		emnIssueSecretsLens,
		L.Compose[*EventMessenger](issueSecretsKeysLens),
		L.Compose[*EventMessenger](issueKeysRepoLens),
	)

	emnIssueTokenKeyLens = F.Pipe2(
		emnIssueSecretsLens,
		L.Compose[*EventMessenger](issueSecretsKeysLens),
		L.Compose[*EventMessenger](issueKeysTokenLens),
	)

	secretsOwnerKeyLens = F.Pipe1(
		issueSecretsKeysLens,
		L.Compose[IssueSecrets](issueKeysOwnerLens),
	)

	secretsRepoKeyLens = F.Pipe1(
		issueSecretsKeysLens,
		L.Compose[IssueSecrets](issueKeysRepoLens),
	)

	secretsTokenKeyLens = F.Pipe1(
		issueSecretsKeysLens,
		L.Compose[IssueSecrets](issueKeysTokenLens),
	)

	secretsOpenRouterKeyLens = F.Pipe1(
		issueSecretsKeysLens,
		L.Compose[IssueSecrets](issueKeysOpenRouterLens),
	)

	secretsOwnerValLens = F.Pipe1(
		issueSecretsValuesLens,
		L.Compose[IssueSecrets](issueValsOwnerLens),
	)

	secretsRepoValLens = F.Pipe1(
		issueSecretsValuesLens,
		L.Compose[IssueSecrets](issueValsRepoLens),
	)

	secretsTokenValLens = F.Pipe1(
		issueSecretsValuesLens,
		L.Compose[IssueSecrets](issueValsTokenLens),
	)

	secretsOpenRouterValLens = F.Pipe1(
		issueSecretsValuesLens,
		L.Compose[IssueSecrets](issueValsOpenRouterLens),
	)

	trimmedOpenRouterKey = F.Flow2(secretsOpenRouterKeyLens.Get, strings.TrimSpace)

	hasOpenRouterKey = F.Pipe1(
		S.IsNonEmpty,
		P.ContraMap(trimmedOpenRouterKey),
	)

	hasOpenRouterVal = F.Pipe1(
		S.IsNonEmpty,
		P.ContraMap(F.Flow2(secretsOpenRouterValLens.Get, strings.TrimSpace)),
	)

	hasOpenRouterSecretEntry = F.Pipe1(
		hasOpenRouterKey,
		P.And(hasOpenRouterVal),
	)

	toSecretStringInput = F.Flow7(
		toPulumiString,
		F.ToAny[pulumi.String],
		pulumi.ToSecret,
		F.ToAny[pulumi.Output],
		A.Of[any],
		F.Bind1st(F.Unvariadic1(pulumi.Sprintf), "%s"),
		toPulumiStringOutput,
	)
	specOptionalPtr = F.Flow2(
		O.FromPredicate(envSpecOptLens.Get),
		O.Fold(
			F.Constant[pulumi.BoolPtrInput](nil),
			F.Constant1[issueEnvSpec](pulumi.BoolPtr(true)),
		),
	)

	openRouterSecretEntry = F.Flow2(
		O.FromPredicate(hasOpenRouterSecretEntry),
		O.Fold(
			R.Empty[string, pulumi.StringInput],
			func(s IssueSecrets) map[string]pulumi.StringInput {
				return R.Singleton(
					trimmedOpenRouterKey(s),
					toSecretStringInput(secretsOpenRouterValLens.Get(s)),
				)
			},
		),
	)

	optionalOpenRouterSpecs = F.Flow2(
		O.FromPredicate(hasOpenRouterKey),
		O.Fold(
			A.Empty[issueEnvSpec],
			func(s IssueSecrets) []issueEnvSpec {
				return A.Of(
					newIssueEnvSpec(
						"OPENROUTER_API_KEY",
						issueSecretsNameLens.Get(s),
						trimmedOpenRouterKey(s),
						true,
					),
				)
			},
		),
	)
)

func toPulumiStringOutput(o pulumi.StringOutput) pulumi.StringInput {
	return o
}

func toPulumiString(s string) pulumi.String {
	return pulumi.String(s)
}

func toPulumiStringMap(m map[string]pulumi.StringInput) pulumi.StringMap {
	return pulumi.StringMap(m)
}

func toEnvVarArray(items []corev1.EnvVarInput) corev1.EnvVarArray {
	return corev1.EnvVarArray(items)
}

func newIssueEnvSpec(
	envName string,
	secretName string,
	key string,
	optional bool,
) issueEnvSpec {
	return F.Pipe4(
		issueEnvSpec{},
		envSpecNameLens.Set(envName),
		envSpecSecretLens.Set(secretName),
		envSpecKeyLens.Set(key),
		envSpecOptLens.Set(optional),
	)
}

func toEnvVarArgs(spec issueEnvSpec) corev1.EnvVarInput {
	return F.Pipe5(
		&corev1.SecretKeySelectorArgs{},
		keySelNameLens.Set(toPulumiString(envSpecSecretLens.Get(spec))),
		keySelKeyLens.Set(toPulumiString(envSpecKeyLens.Get(spec))),
		keySelOptionalLens.Set(specOptionalPtr(spec)),
		func(sel *corev1.SecretKeySelectorArgs) *corev1.EnvVarSourceArgs {
			return F.Pipe1(&corev1.EnvVarSourceArgs{}, envSourceKeyRefLens.Set(sel))
		},
		func(src *corev1.EnvVarSourceArgs) *corev1.EnvVarArgs {
			return F.Pipe2(
				&corev1.EnvVarArgs{},
				envVarNameLens.Set(toPulumiString(envSpecNameLens.Get(spec))),
				envVarValueFromLens.Set(src),
			)
		},
	)
}

func buildIssueSecretStringData(secrets IssueSecrets) pulumi.StringMap {
	return F.Pipe5(
		R.Empty[string, pulumi.StringInput](),
		R.UpsertAt(
			secretsOwnerKeyLens.Get(secrets),
			toSecretStringInput(secretsOwnerValLens.Get(secrets)),
		),
		R.UpsertAt(
			secretsRepoKeyLens.Get(secrets),
			toSecretStringInput(secretsRepoValLens.Get(secrets)),
		),
		R.UpsertAt(
			secretsTokenKeyLens.Get(secrets),
			toSecretStringInput(secretsTokenValLens.Get(secrets)),
		),
		F.Bind2nd(
			R.UnionLastMonoid[string, pulumi.StringInput]().Concat,
			openRouterSecretEntry(secrets),
		),
		toPulumiStringMap,
	)
}

func (emn *EventMessenger) CreateIssueSecret(
	ctx *pulumi.Context,
) (*corev1.Secret, error) {
	secret, err := corev1.NewSecret(
		ctx,
		emnIssueSecretNameLens.Get(emn),
		F.Pipe2(
			&corev1.SecretArgs{},
			secretMetaLens.Set(
				F.Pipe2(
					&metav1.ObjectMetaArgs{},
					objMetaNameLens.Set(toPulumiString(emnIssueSecretNameLens.Get(emn))),
					objMetaNamespaceLens.Set(toPulumiString(emnNamespaceLens.Get(emn))),
				),
			),
			secretStringDataLens.Set(
				buildIssueSecretStringData(emnIssueSecretsLens.Get(emn)),
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("error creating issue secret: %w", err)
	}

	return secret, nil
}

func (emn *EventMessenger) IssueContainerEnvArgsArray() corev1.EnvVarArray {
	return F.Pipe2(
		A.ArrayConcatAll(
			[]issueEnvSpec{
				newIssueEnvSpec(
					"GITHUB_OWNER",
					emnIssueSecretNameLens.Get(emn),
					emnIssueOwnerKeyLens.Get(emn),
					false,
				),
				newIssueEnvSpec(
					"GITHUB_REPOSITORY",
					emnIssueSecretNameLens.Get(emn),
					emnIssueRepoKeyLens.Get(emn),
					false,
				),
				newIssueEnvSpec(
					"GITHUB_TOKEN",
					emnIssueSecretNameLens.Get(emn),
					emnIssueTokenKeyLens.Get(emn),
					false,
				),
			},
			optionalOpenRouterSpecs(emnIssueSecretsLens.Get(emn)),
		),
		A.Map(toEnvVarArgs),
		toEnvVarArray,
	)
}

func (emn *EventMessenger) IssueContainerArgs() pulumi.StringArray {
	return pulumi.ToStringArray([]string{
		flagLogLevel,
		emnLogLevelLens.Get(emn),
		"gh-issue",
		flagSubject,
		emnSubjectLens.Get(emn),
		"--token",
		"$(GITHUB_TOKEN)",
		"--repository",
		"$(GITHUB_REPOSITORY)",
		"--owner",
		"$(GITHUB_OWNER)",
	})
}

func (emn *EventMessenger) IssueContainerArray() corev1.ContainerArray {
	return corev1.ContainerArray{
		F.Pipe5(
			&corev1.ContainerArgs{},
			containerNameLens.Set(toPulumiString(emnIssueNameLens.Get(emn))),
			containerImageLens.Set(
				toPulumiString(
					fmt.Sprintf(
						"%s:%s",
						emnImageNameLens.Get(emn),
						emnImageTagLens.Get(emn),
					),
				),
			),
			containerPullPolicyLens.Set(
				toPulumiString(emnImagePullPolicyLens.Get(emn)),
			),
			containerArgsLens.Set(emn.IssueContainerArgs()),
			containerEnvLens.Set(emn.IssueContainerEnvArgsArray()),
		),
	}
}

func (emn *EventMessenger) CreateIssueDeployment(
	ctx *pulumi.Context,
	issueSecret *corev1.Secret,
) (*appsv1.Deployment, error) {
	deployment, err := appsv1.NewDeployment(
		ctx,
		emnIssueNameLens.Get(emn),
		F.Pipe2(
			&appsv1.DeploymentArgs{},
			depArgsMetaLens.Set(emn.CreateIssueDeploymentMetadata()),
			depArgsSpecLens.Set(emn.CreateIssueDeploymentSpec()),
		),
		pulumi.DependsOn([]pulumi.Resource{issueSecret}),
		pulumi.Timeouts(
			F.Pipe1(&pulumi.CustomTimeouts{}, customTimeoutsCreateLens.Set("3m")),
		),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"error creating %s deployment: %w",
			emnIssueNameLens.Get(emn),
			err,
		)
	}
	return deployment, nil
}

func (emn *EventMessenger) issueAppLabels() pulumi.StringMap {
	return F.Pipe1(
		R.Singleton(
			appLabel,
			pulumi.StringInput(toPulumiString(emnIssueNameLens.Get(emn))),
		),
		toPulumiStringMap,
	)
}

func (emn *EventMessenger) CreateIssueDeploymentMetadata() *metav1.ObjectMetaArgs {
	return F.Pipe3(
		&metav1.ObjectMetaArgs{},
		objMetaNamespaceLens.Set(toPulumiString(emnNamespaceLens.Get(emn))),
		objMetaNameLens.Set(toPulumiString(emnIssueNameLens.Get(emn))),
		objMetaLabelsLens.Set(emn.issueAppLabels()),
	)
}

func (emn *EventMessenger) CreateIssueDeploymentSpec() *appsv1.DeploymentSpecArgs {
	return F.Pipe2(
		&appsv1.DeploymentSpecArgs{},
		depSpecSelectorLens.Set(
			F.Pipe1(
				&metav1.LabelSelectorArgs{},
				labelSelMatchLens.Set(emn.issueAppLabels()),
			),
		),
		depSpecTmplLens.Set(
			F.Pipe2(
				&corev1.PodTemplateSpecArgs{},
				podTmplMetaLens.Set(
					F.Pipe1(
						&metav1.ObjectMetaArgs{},
						objMetaLabelsLens.Set(emn.issueAppLabels()),
					),
				),
				podTmplSpecLens.Set(
					F.Pipe1(
						&corev1.PodSpecArgs{},
						podSpecContainersLens.Set(emn.IssueContainerArray()),
					),
				),
			),
		),
	)
}
