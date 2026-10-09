package gcp

import (
	"errors"
	"net/http"

	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Action is what a converge did.
type Action string

const (
	ActionCreated   Action = "created"
	ActionUpdated   Action = "updated"
	ActionUnchanged Action = "unchanged"
	ActionDryRun    Action = "dry-run"
)

// MutationMode governs mutations: DryRun skips every mutation seam and
// reports intent; Confirmed gates destructive flows.
type MutationMode struct {
	DryRun    bool
	Confirmed bool
}

// Result reports one converge outcome.
type Result struct {
	Name   string // resource identifier: "gs://bucket", "projects/p/roles/x", "projects/p/serviceAccounts/e"
	Action Action
}

// IsNotFound reports a typed 404 / codes.NotFound (never message matching).
func IsNotFound(err error) bool {
	return hasGoogleCode(err, http.StatusNotFound) || hasGRPCCode(err, codes.NotFound)
}

// IsAlreadyExists reports a typed 409-create / codes.AlreadyExists.
func IsAlreadyExists(err error) bool {
	return hasGoogleCode(err, http.StatusConflict) || hasGRPCCode(err, codes.AlreadyExists)
}

// IsPermissionDenied reports a typed 403 / codes.PermissionDenied — never
// misread as missing or already-exists.
func IsPermissionDenied(err error) bool {
	return hasGoogleCode(err, http.StatusForbidden) || hasGRPCCode(err, codes.PermissionDenied)
}

// IsConflict reports an etag/optimistic-concurrency conflict: HTTP 409 or
// codes.Aborted.
func IsConflict(err error) bool {
	return hasGoogleCode(err, http.StatusConflict) || hasGRPCCode(err, codes.Aborted)
}

func hasGoogleCode(err error, code int) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == code
}

func hasGRPCCode(err error, code codes.Code) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == code
}
