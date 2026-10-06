package kube

import (
	"errors"
	"testing"

	ttesting "github.com/gruntwork-io/terratest/modules/testing"
	"github.com/stretchr/testify/require"
)

// terratest's Tunnel.ForwardPortE calls the non-E GetService internally, which
// reports failure through t.Fatal. These tests pin the adapter contract that
// turns those calls into ordinary errors instead of crashing the CLI.

const adapterName = "tunnel"

var errTest = errors.New("forward failed")

func TestRunTFatalBecomesError(t *testing.T) {
	got, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (*int, error) {
			tt.Fatalf("service %s not found", "arangodb")
			return new(int), nil
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "service arangodb not found")
	require.Nil(t, got)
}

func TestRunTFailNowBecomesError(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (int, error) {
			tt.FailNow()
			return 1, nil
		},
	)
	require.Error(t, err)
}

func TestRunTRecordedErrorFailsTheCall(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(tt ttesting.TestingT) (int, error) {
			tt.Errorf("pod %s not ready", "arangodb-0")
			return 1, nil
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pod arangodb-0 not ready")
}

func TestRunTReturnsFnError(t *testing.T) {
	_, err := runT(
		&testingAdapter{name: adapterName},
		func(_ ttesting.TestingT) (int, error) {
			return 0, errTest
		},
	)
	require.ErrorIs(t, err, errTest)
}

func TestRunTSuccess(t *testing.T) {
	got, err := runT(
		&testingAdapter{name: adapterName},
		func(_ ttesting.TestingT) (int, error) {
			return 42, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, 42, got)
}

func TestRunTRepanicsNonSentinel(t *testing.T) {
	require.PanicsWithValue(t, "real bug", func() {
		_, _ = runT(
			&testingAdapter{name: adapterName},
			func(_ ttesting.TestingT) (int, error) {
				panic("real bug")
			},
		)
	})
}

func TestParsePort(t *testing.T) {
	port, err := parsePort("localhost:43121")
	require.NoError(t, err)
	require.Equal(t, 43121, port)

	_, err = parsePort("localhost")
	require.Error(t, err)

	_, err = parsePort("localhost:http")
	require.Error(t, err)
}
