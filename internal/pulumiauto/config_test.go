package pulumiauto

import (
	"context"
	"errors"
	"fmt"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/stretchr/testify/require"
)

// testSecret is the shared planted-secret fixture (never real).
const testSecret = "s3cr3t"

func TestSetConfigTargetsStack(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log}
	_, err := E.UnwrapError(SetConfig(context.Background(), st, "imageTag", "v1.2.3", false)())
	require.NoError(t, err)
	require.Equal(t, []string{"set-config:imageTag"}, log)
	require.Equal(t, auto.ConfigValue{Value: "v1.2.3"}, st.configs["imageTag"])
}

func TestSetSecretMarksSecret(t *testing.T) {
	log := []string{}
	st := &fakeStack{log: &log}
	_, err := E.UnwrapError(SetConfig(context.Background(), st, "dbPassword", testSecret, true)())
	require.NoError(t, err)
	require.Equal(t, auto.ConfigValue{Value: testSecret, Secret: true}, st.configs["dbPassword"])
	for _, line := range log {
		require.NotContains(t, line, testSecret, "secret values never reach logs")
	}
}

func TestSecretValuesCollectsOnlySecrets(t *testing.T) {
	st := &fakeStack{configs: auto.ConfigMap{
		"imageTag":   {Value: "v1"},
		"dbPassword": {Value: testSecret, Secret: true},
	}}
	got, err := E.UnwrapError(SecretValues(context.Background(), st)())
	require.NoError(t, err)
	require.Equal(t, []string{testSecret}, got)
}

func TestStackOutputReadsKey(t *testing.T) {
	st := &fakeStack{outputs: auto.OutputMap{"appNamespace": {Value: "ns1"}}}
	got, err := E.UnwrapError(StackOutput(context.Background(), st, "appNamespace")())
	require.NoError(t, err)
	require.Equal(t, "ns1", fmt.Sprint(got))

	_, err = E.UnwrapError(StackOutput(context.Background(), st, "missing")())
	require.ErrorIs(t, err, errOutputNotFound)
}

func TestErrOutputNotFoundIsSentinel(t *testing.T) {
	require.True(t, errors.Is(errOutputNotFound, errOutputNotFound))
}
