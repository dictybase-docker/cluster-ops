package gcp

import (
	"errors"
	"fmt"
	"testing"
	"time"

	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"

	E "github.com/IBM/fp-go/v2/either"
	O "github.com/IBM/fp-go/v2/option"
	R "github.com/IBM/fp-go/v2/retry"
	"github.com/stretchr/testify/assert"
)

func TestIsSAMissing(t *testing.T) {
	wrapped := fmt.Errorf(
		"resourceManagerService.Projects.SetIamPolicy: %w",
		&googleapi.Error{
			Code:    400,
			Message: "Service account pulumi-manager@p.iam.gserviceaccount.com does not exist., badRequest",
		},
	)
	assert.True(t, isSAMissing(wrapped))

	// A plain error merely containing "does not exist" must not qualify.
	assert.False(t, isSAMissing(errors.New(
		"some bucket does not exist")))
	// A non-400 API error with a missing-resource message must not qualify.
	assert.False(t, isSAMissing(&googleapi.Error{
		Code:    403,
		Message: "Service account sa@p.iam.gserviceaccount.com does not exist.",
	}))
	assert.False(t, isSAMissing(nil))
}

func TestRetryOnSAMissing(t *testing.T) {
	missing := fmt.Errorf(
		"resourceManagerService.Projects.SetIamPolicy: %w",
		&googleapi.Error{
			Code:    400,
			Message: "Service account sa@p.iam.gserviceaccount.com does not exist., badRequest",
		},
	)

	assert.True(t, retryOnSAMissing(E.Left[*cloudresourcemanager.Policy](missing)))
	assert.False(t, retryOnSAMissing(E.Left[*cloudresourcemanager.Policy](
		errors.New("googleapi: Error 403: permission denied"))))
	assert.False(t, retryOnSAMissing(E.Right[error](&cloudresourcemanager.Policy{})))
}

func TestIamPropagationPolicy(t *testing.T) {
	// Every iteration below the retry limit must report a retry delay
	// (LimitRetries caps the attempt count), then the combined policy stops.
	for iter := range int(iamPropagationRetries) {
		delay, ok := O.Unwrap(iamPropagationPolicy(R.RetryStatus{IterNumber: uint(iter)}))
		assert.True(t, ok, "policy should retry at iter %d", iter)
		assert.Greater(t, delay, time.Duration(0))
	}
	_, ok := O.Unwrap(iamPropagationPolicy(R.RetryStatus{IterNumber: iamPropagationRetries}))
	assert.False(t, ok, "policy should stop at iter %d", iamPropagationRetries)
}
