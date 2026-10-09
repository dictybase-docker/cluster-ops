package gcp

import (
	"context"
	"net/http"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	crm "google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/googleapi"
)

// fakeEnsureSA logs and reports an ensured service account.
type fakeEnsureSA struct {
	log     *[]string
	existed bool
	email   string
}

func (f *fakeEnsureSA) EnsureSA(context.Context, string, string, string) (string, Action, error) {
	if f.log != nil {
		*f.log = append(*f.log, callEnsureSA)
	}
	action := ActionCreated
	if f.existed {
		action = ActionUnchanged
	}
	return f.email, action, nil
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
	sa := &fakeEnsureSA{log: &log, email: testSAEmail}
	iam := &fakeProjectIAM{policy: &crm.Policy{Etag: testEtag}, log: &log}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}

	got, err := E.UnwrapError(SetupBackupSA(sa, iam, keys, files, twoRoleCfg(), MutationMode{})())
	require.NoError(t, err)
	require.Len(t, got, 4) // SA + 2 roles + key
	require.Equal(t,
		[]string{callEnsureSA, callGet, callSet, callGet, callSet, callListKeys, callCreateKey},
		log,
		"SA → roles → key, in order")
	require.Equal(t, ActionCreated, got[0].Action)
	require.Equal(t, ActionUpdated, got[1].Action)
	require.Equal(t, ActionUpdated, got[2].Action)
	require.Equal(t, ActionCreated, got[3].Action)
}

func TestSetupBackupSAIdempotent(t *testing.T) {
	log := []string{}
	sa := &fakeEnsureSA{log: &log, existed: true, email: testSAEmail}
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
	require.NotContains(t, log, callSet, "idempotent run never writes")
}

func TestSetupReaderGrantsBucket(t *testing.T) {
	log := []string{}
	sa := &fakeEnsureSA{log: &log, email: testSAEmail}
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
		hasUnconditionalBinding(bucketIAM.policy, "roles/storage.objectViewer", "serviceAccount:"+testSAEmail),
		"bucket binding grants objectViewer to the reader SA")
}

// A role bind failing with 403 must stop the flow BEFORE key minting.
func TestSetupStopsOnForbiddenRole(t *testing.T) {
	log := []string{}
	sa := &fakeEnsureSA{log: &log, email: testSAEmail}
	blocked := &blockedIAM{err: &googleapi.Error{Code: http.StatusForbidden}}
	keys := &fakeSAKeyAPI{log: &log}
	files := &fakeFiles{}

	_, err := E.UnwrapError(SetupBackupSA(sa, blocked, keys, files, twoRoleCfg(), MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, keys.mints, "no key minted after a failed binding")
}
