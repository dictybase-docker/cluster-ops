package gcp

import (
	"context"
	"net/http"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	R "github.com/IBM/fp-go/v2/retry"
	"github.com/stretchr/testify/require"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

// fakeSAAPI logs and reports a probed/created service account.
type fakeSAAPI struct {
	log     *[]string
	existed bool
	email   string
	creates int
}

func (f *fakeSAAPI) GetSA(context.Context, string, string) (string, bool, error) {
	if f.log != nil {
		*f.log = append(*f.log, callGetSA)
	}
	return f.email, f.existed, nil
}

func (f *fakeSAAPI) CreateSA(context.Context, string, string, string) (string, bool, error) {
	if f.log != nil {
		*f.log = append(*f.log, callCreateSA)
	}
	f.creates++
	return f.email, true, nil
}

func twoRoleCfg() BackupSAConfig {
	return BackupSAConfig{
		Project: testProject, SAName: "backup-gcs-sa", DisplayName: "backup",
		Roles:       []string{"roles/storage.admin", "roles/storage.objectViewer"},
		KeyFilePath: "credentials/proj-dcr/backup-gcs-sa.json",
	}
}

func TestSetupBackupSAHappy(t *testing.T) {
	log := []string{}
	sa := &fakeSAAPI{log: &log, email: testSAEmail}
	iam := &fakeProjectIAM{policy: &crm.Policy{Etag: testEtag}, log: &log}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}

	got, err := E.UnwrapError(SetupBackupSA(sa, iam, keys, files, twoRoleCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Len(t, got, 4) // SA + 2 roles + key
	require.Equal(t,
		[]string{callGetSA, callCreateSA, callGet, callSet, callGet, callSet, callListKeys, callCreateKey},
		log,
		"SA probe → create → roles → key, in order")
	require.Equal(t, ActionCreated, got[0].Action)
	require.Equal(t, ActionUpdated, got[1].Action)
	require.Equal(t, ActionUpdated, got[2].Action)
	require.Equal(t, ActionCreated, got[3].Action)
	require.Equal(t, 1, sa.creates)
}

func TestSetupBackupSAIdempotent(t *testing.T) {
	log := []string{}
	sa := &fakeSAAPI{log: &log, existed: true, email: testSAEmail}
	iam := &fakeProjectIAM{
		policy: testBindingPolicy("serviceAccount:" + testSAEmail),
		log:    &log,
	}
	cfg := twoRoleCfg()
	cfg.Roles = []string{testRole} // already bound
	keys := &fakeSAKeyAPI{log: &log, live: []string{"k1"}}
	files := &fakeFiles{keyFile: liveKeyFile}

	got, err := E.UnwrapError(SetupBackupSA(sa, iam, keys, files, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Len(t, got, 3) // SA + 1 role + key
	for _, r := range got {
		require.Equal(t, ActionUnchanged, r.Action, r.Name)
	}
	require.Zero(t, keys.mints, "idempotent run never mints")
	require.Zero(t, sa.creates, "idempotent run never creates")
	require.NotContains(t, log, callSet, "idempotent run never writes")
}

// Review Focus 4 / Global Constraint: dry-run touches NO mutation seam —
// including EnsureSA's create path (the first draft created real SAs).
func TestSetupBackupSADryRunMutatesNothing(t *testing.T) {
	log := []string{}
	sa := &fakeSAAPI{log: &log, email: testSAEmail}
	iam := &fakeProjectIAM{policy: &crm.Policy{Etag: testEtag}, log: &log}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}
	mode := MutationMode{DryRun: true}

	got, err := E.UnwrapError(SetupBackupSA(sa, iam, keys, files, twoRoleCfg(), mode)())
	require.NoError(t, err)
	require.Len(t, got, 4)
	for _, r := range got {
		require.Equal(t, ActionDryRun, r.Action, r.Name)
	}
	require.Zero(t, sa.creates, "dry-run never creates a service account")
	require.Zero(t, keys.mints, "dry-run never mints")
	require.NotContains(t, log, callSet, "dry-run never writes policy")
}

func TestSetupReaderGrantsBucket(t *testing.T) {
	log := []string{}
	sa := &fakeSAAPI{log: &log, email: testSAEmail}
	bucketIAM := &fakeBucketIAM{policy: bucketPolicy()}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}
	cfg := CrossProjectReaderConfig{
		SourceProject: testProject, SAName: "arangodb-restic-reader",
		Bucket: testBucketName, Role: "roles/storage.objectViewer",
		KeyFilePath: "credentials/proj-dcr/reader.json",
	}

	got, err := E.UnwrapError(SetupCrossProjectReader(sa, bucketIAM, keys, files, cfg, MutationMode{})())
	require.NoError(t, err)
	require.Len(t, got, 3) // SA + bucket bind + key
	require.True(t,
		hasUnconditionalBinding(bucketIAM.policy, "roles/storage.objectViewer",
			"serviceAccount:arangodb-restic-reader@"+testProject+".iam.gserviceaccount.com"),
		"bucket binding grants objectViewer to the reader SA")
}

// A role bind failing with 403 must stop the flow BEFORE key minting.
func TestSetupStopsOnForbiddenRole(t *testing.T) {
	log := []string{}
	sa := &fakeSAAPI{log: &log, email: testSAEmail}
	blocked := &blockedIAM{err: &googleapi.Error{Code: http.StatusForbidden}}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}

	_, err := E.UnwrapError(SetupBackupSA(sa, blocked, keys, files, twoRoleCfg(), MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, keys.mints, "no key minted after a failed binding")
}

// A freshly created SA propagates asynchronously: transient binding failures
// (400-class propagation, not forbidden/not-found) retry with backoff.
func TestSetupRetriesAfterSAPropagation(t *testing.T) {
	restore := saPropagationPolicy
	saPropagationPolicy = R.Monoid.Concat(
		R.LimitRetries(3),
		R.ExponentialBackoff(time.Millisecond),
	)
	defer func() { saPropagationPolicy = restore }()

	log := []string{}
	sa := &fakeSAAPI{log: &log, email: testSAEmail} // fresh (creates)
	iam := &fakeProjectIAM{
		policy:  &crm.Policy{Etag: testEtag},
		log:     &log,
		getErrs: []error{&googleapi.Error{Code: http.StatusBadRequest}},
	}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}
	cfg := twoRoleCfg()
	cfg.Roles = []string{testRole}

	got, err := E.UnwrapError(SetupBackupSA(sa, iam, keys, files, cfg, MutationMode{})())
	require.NoError(t, err, "transient propagation failure must retry")
	require.Equal(t, ActionUpdated, got[1].Action)
	require.Equal(t, 1, keys.mints)
}
