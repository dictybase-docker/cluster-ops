package kube

import (
	"fmt"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Clients holds Kubernetes clients and configuration.
type Clients struct {
	Kube kubernetes.Interface
	Rest *rest.Config
}

// Reason represents classified Kubernetes error reasons.
type Reason int

const (
	ReasonNotFound Reason = iota
	ReasonForbidden
	ReasonUnauthorized
	ReasonUnreachable
	ReasonOther
)

// NewClients creates Kubernetes clients from kubeconfig.
// Empty kubeconfig uses default loading rules.
// Empty context uses current-context from kubeconfig.
func NewClients(kubeconfig, context string) E.Either[error, Clients] {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		configOverrides.CurrentContext = context
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		configOverrides,
	)

	config, err := clientConfig.ClientConfig()
	if err != nil {
		return E.Left[Clients](fmt.Errorf("failed to load kubeconfig: %w", err))
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return E.Left[Clients](fmt.Errorf("failed to create kubernetes client: %w", err))
	}

	return E.Right[error](Clients{
		Kube: clientset,
		Rest: config,
	})
}

// Classify maps a Kubernetes error to a Reason for distinct Fix text.
func Classify(err error) Reason {
	if err == nil {
		return ReasonOther
	}
	if errors.IsNotFound(err) {
		return ReasonNotFound
	}
	if errors.IsForbidden(err) {
		return ReasonForbidden
	}
	if errors.IsUnauthorized(err) {
		return ReasonUnauthorized
	}
	// Unreachable: connection refused, timeout, DNS failure
	// client-go doesn't expose a specific check, so inspect message
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") {
		return ReasonUnreachable
	}
	return ReasonOther
}
