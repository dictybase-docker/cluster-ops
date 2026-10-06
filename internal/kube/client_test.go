package kube_test

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/dictybase-docker/cluster-ops/internal/kube"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected kube.Reason
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: kube.ReasonOther,
		},
		{
			name:     "NotFound",
			err:      apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "namespaces"}, "test"),
			expected: kube.ReasonNotFound,
		},
		{
			name: "Forbidden",
			err: apierrors.NewForbidden(
				schema.GroupResource{Resource: "deployments"}, "test", errors.New("denied"),
			),
			expected: kube.ReasonForbidden,
		},
		{
			name:     "Unauthorized",
			err:      apierrors.NewUnauthorized("no token"),
			expected: kube.ReasonUnauthorized,
		},
		{
			name:     "connection refused",
			err:      &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")},
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "connection refused (substring)",
			err:      fmt.Errorf("dial tcp: connection refused"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "no such host",
			err:      fmt.Errorf("lookup failed: no such host"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "i/o timeout",
			err:      fmt.Errorf("context deadline exceeded: i/o timeout"),
			expected: kube.ReasonUnreachable,
		},
		{
			name:     "other error",
			err:      fmt.Errorf("some other error"),
			expected: kube.ReasonOther,
		},
		{
			name:     "wrapped NotFound",
			err:      fmt.Errorf("outer: %w", apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "foo")),
			expected: kube.ReasonNotFound,
		},
		{
			name:     "wrapped Forbidden",
			err:      fmt.Errorf("outer: %w", apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "bar", nil)),
			expected: kube.ReasonForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := kube.Classify(tt.err)
			require.Equal(t, tt.expected, reason, "Classify(%v)", tt.err)
		})
	}
}
