package pulumiauto

import (
	"context"
	"errors"
	"fmt"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
)

// errOutputNotFound names a missing stack output (sentinel; errors.Is, never
// message matching).
var errOutputNotFound = errors.New("stack output not set")

// SetConfig writes one key; secret marks it for the secrets provider. The
// value never reaches logs.
func SetConfig(ctx context.Context, s StackAPI, key, value string, secret bool) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, s.SetConfig(ctx, key, auto.ConfigValue{Value: value, Secret: secret})
	})
}

// SecretValues returns every secret config VALUE of the stack. The redactor
// (Task 5) masks them in logs; never log the result.
func SecretValues(ctx context.Context, s StackAPI) IOE.IOEither[error, []string] {
	return F.Pipe1(
		IOE.TryCatchError(func() (auto.ConfigMap, error) { return s.GetAllConfig(ctx) }),
		IOE.Map[error](secretValues),
	)
}

func secretValues(cfg auto.ConfigMap) []string {
	values := []string{}
	for _, v := range cfg {
		if v.Secret && v.Value != "" {
			values = append(values, v.Value)
		}
	}
	return values
}

// StackOutput reads one output value; a missing key is errOutputNotFound.
func StackOutput(ctx context.Context, s StackAPI, key string) IOE.IOEither[error, string] {
	return F.Pipe1(
		IOE.TryCatchError(func() (auto.OutputMap, error) { return s.Outputs(ctx) }),
		IOE.ChainEitherK(outputValue(key)),
	)
}

func outputValue(key string) func(auto.OutputMap) E.Either[error, string] {
	return func(outs auto.OutputMap) E.Either[error, string] {
		v, ok := outs[key]
		if !ok {
			return E.Left[string](fmt.Errorf("%q: %w", key, errOutputNotFound))
		}
		return E.Of[error](fmt.Sprint(v.Value))
	}
}
