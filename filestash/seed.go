package main

import (
	"fmt"

	B "github.com/IBM/fp-go/v2/bytes"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	J "github.com/IBM/fp-go/v2/json"
	corev1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/core/v1"
	metav1 "github.com/pulumi/pulumi-kubernetes/sdk/v4/go/kubernetes/meta/v1"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	seedFile           = "config.json"
	searchTimeoutSecs  = 5
	seedCopyScriptTmpl = "mkdir -p %[1]s/config && " +
		"[ -e %[1]s/config/config.json ] || cp %[2]s/config.json %[1]s/config/config.json"
)

// seedDocument is the first-start config.json. It pins the one S3
// connection (type, label, endpoint, region) and has no credentials.
// Staff log in with their own MinIO keys. Admin console changes after the
// first start live on the state PVC, so the seed is copied only once.
func seedDocument(cfg *FilestashConfig) map[string]any {
	return map[string]any{
		"general": map[string]any{"name": "dictyBase files"},
		"features": map[string]any{
			"server": map[string]any{"console_enable": false},
			"search": map[string]any{"explore_timeout": searchTimeoutSecs},
			"share":  map[string]any{"enable": true},
		},
		"log": map[string]any{"telemetry": false},
		"connections": []map[string]any{{
			"type":     "s3",
			"label":    cfg.S3.Label,
			"endpoint": cfg.S3.Endpoint,
			"region":   cfg.S3.Region,
		}},
	}
}

var encodeSeed = F.Flow3(
	seedDocument,
	J.Marshal[map[string]any],
	E.Map[error](B.ToString),
)

// CreateSeedConfigMap creates the ConfigMap that holds the seed config.json.
func (f *Filestash) CreateSeedConfigMap(ctx *pulumi.Context) (*corev1.ConfigMap, error) {
	created := F.Pipe3(
		f.Config,
		encodeSeed,
		E.MapLeft[string](wrapSeedError),
		E.Chain(F.Bind1st(E.Eitherize2(f.newSeedConfigMap), ctx)),
	)
	return E.UnwrapError(created)
}

func wrapSeedError(err error) error {
	return fmt.Errorf("error encoding filestash seed config: %w", err)
}

func (f *Filestash) newSeedConfigMap(ctx *pulumi.Context, doc string) (*corev1.ConfigMap, error) {
	name := f.resourceName("seed")
	cm, err := corev1.NewConfigMap(ctx, name, &corev1.ConfigMapArgs{
		Metadata: &metav1.ObjectMetaArgs{
			Name:      pulumi.String(name),
			Namespace: pulumi.String(f.Config.Namespace),
			Labels:    f.labels(),
		},
		Data: pulumi.StringMap{seedFile: pulumi.String(doc)},
	})
	if err != nil {
		return nil, fmt.Errorf("error creating filestash seed configmap: %w", err)
	}
	return cm, nil
}

func seedCopyScript() string {
	return fmt.Sprintf(seedCopyScriptTmpl, statePath, seedPath)
}
