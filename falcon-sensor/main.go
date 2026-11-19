package main

import (
	"fmt"

	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	"github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/helm/v3"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Falcon installs the CrowdStrike Falcon node sensor DaemonSet: the
// namespace with the privileged Pod Security level, Secret
// falcon-sensor-cid, and the falcon-sensor Helm release.
type Falcon struct {
	Config *FalconConfig
}

func main() {
	pulumi.Run(Run)
}

func Run(ctx *pulumi.Context) error {
	cfg, err := ReadConfig(ctx)
	if err != nil {
		return err
	}
	return (&Falcon{Config: cfg}).Install(ctx)
}

func (f *Falcon) Install(ctx *pulumi.Context) error {
	ns, err := f.CreateNamespace(ctx)
	if err != nil {
		return err
	}
	sec, err := f.CreateSecret(ctx, ns)
	if err != nil {
		return err
	}
	if err := f.CreateRelease(ctx, sec); err != nil {
		return err
	}
	ctx.Export("namespace", pulumi.String(f.Config.Namespace))
	ctx.Export("daemonSet", pulumi.String(releaseName))
	ctx.Export("image", pulumi.String(f.Config.ImageRef()))
	return nil
}

// CreateNamespace makes the sensor namespace. The sensor pods are
// privileged, so all three Pod Security labels are "privileged".
func (f *Falcon) CreateNamespace(ctx *pulumi.Context) (*corev1.Namespace, error) {
	ns, err := corev1.NewNamespace(ctx, "falcon-namespace", &corev1.NamespaceArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:   pulumi.String(f.Config.Namespace),
			Labels: PodSecurityLabels(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating namespace %s: %w", f.Config.Namespace, err)
	}
	return ns, nil
}

// PodSecurityLabels are the namespace labels for the privileged level.
func PodSecurityLabels() pulumi.StringMap {
	return pulumi.StringMap{
		"pod-security.kubernetes.io/enforce": pulumi.String("privileged"),
		"pod-security.kubernetes.io/audit":   pulumi.String("privileged"),
		"pod-security.kubernetes.io/warn":    pulumi.String("privileged"),
	}
}

// CreateSecret makes Secret falcon-sensor-cid from the encrypted stack
// config. The values stay Pulumi secrets in the state.
func (f *Falcon) CreateSecret(ctx *pulumi.Context, ns *corev1.Namespace) (*corev1.Secret, error) {
	sec, err := corev1.NewSecret(ctx, "falcon-cid", &corev1.SecretArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(secretName),
			Namespace: ns.Metadata.Name(),
		},
		Type: pulumi.String("Opaque"),
		// ToSecret keeps the StringMap type: the assertion cannot fail.
		StringData: pulumi.ToSecret( //nolint:errcheck,forcetypeassert // see above
			pulumi.ToStringMap(SecretData(f.Config)),
		).(pulumi.StringMapOutput),
	})
	if err != nil {
		return nil, fmt.Errorf("error creating secret %s: %w", secretName, err)
	}
	return sec, nil
}

// CreateRelease installs the pinned falcon-sensor chart after the Secret
// exists, so the first sensor pod finds its CID.
func (f *Falcon) CreateRelease(ctx *pulumi.Context, sec *corev1.Secret) error {
	cfg := f.Config
	_, err := helm.NewRelease(ctx, "falcon-sensor", &helm.ReleaseArgs{
		Name:      pulumi.String(releaseName),
		Chart:     pulumi.String(cfg.Chart.Name),
		Version:   pulumi.String(cfg.Chart.Version),
		Namespace: sec.Metadata.Namespace().Elem(),
		RepositoryOpts: helm.RepositoryOptsArgs{
			Repo: pulumi.String(cfg.Chart.Repository),
		},
		Values: HelmValues(cfg),
	}, pulumi.DependsOn([]pulumi.Resource{sec}))
	if err != nil {
		return fmt.Errorf("error installing Helm chart %s: %w", cfg.Chart.Name, err)
	}
	return nil
}
