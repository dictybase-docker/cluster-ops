package gcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

type fakeSAKeyAPI struct {
	live      []string
	mints     int
	deletes   int
	lastID    string
	deletedID string
	listErr   error
	log       *[]string // optional shared sequence log
}

func (f *fakeSAKeyAPI) ListKeys(context.Context, string) ([]string, error) {
	if f.log != nil {
		*f.log = append(*f.log, callListKeys)
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.live, nil
}

func (f *fakeSAKeyAPI) CreateKey(context.Context, string) (string, []byte, error) {
	if f.log != nil {
		*f.log = append(*f.log, callCreateKey)
	}
	f.mints++
	f.lastID = fmt.Sprintf("k-new-%d", f.mints)
	return f.lastID, []byte(`{"private_key_id": "` + f.lastID + `"}`), nil
}

type fakeFiles struct {
	keyFile   string // content of the key file, "" = missing
	wrote     []byte
	wroteMode os.FileMode
}

func (f *fakeSAKeyAPI) DeleteKey(_ context.Context, _ string, keyID string) error {
	if f.log != nil {
		*f.log = append(*f.log, callDeleteKey)
	}
	f.deletes++
	f.deletedID = keyID
	return nil
}

func (f *fakeFiles) ReadFile(string) ([]byte, error) {
	if f.keyFile == "" {
		return nil, fs.ErrNotExist
	}
	return []byte(f.keyFile), nil
}

func (f *fakeFiles) MkdirAll(string, fs.FileMode) error { return nil }

func (f *fakeFiles) WriteFile(_ string, data []byte, perm os.FileMode) error {
	f.wrote = data
	f.wroteMode = perm
	return nil
}

const testSAEmail = "sa@proj-dcr.iam.gserviceaccount.com"

// liveKeyFile is the shared "key present with id k1" fixture.
const liveKeyFile = `{"private_key_id": "k1"}`

func TestSAKeyReusesLiveFile(t *testing.T) {
	api := &fakeSAKeyAPI{live: []string{"k1"}}
	files := &fakeFiles{keyFile: liveKeyFile}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUnchanged, got.Action)
	require.Zero(t, api.mints)
}

func TestSAKeyMintsWhenMissing(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := &fakeFiles{}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionCreated, got.Action)
	require.Equal(t, 1, api.mints)
	require.Equal(t, os.FileMode(0o600), files.wroteMode, "key material written 0600")
}

// Review Focus 3: dead upstream key → exactly one re-mint.
func TestSAKeyRemintsWhenKeyGone(t *testing.T) {
	api := &fakeSAKeyAPI{live: []string{"k2"}}
	files := &fakeFiles{keyFile: liveKeyFile}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, ActionUpdated, got.Action)
	require.Equal(t, 1, api.mints, "remints exactly once")
}

func TestSAKeyDryRunMintsNothing(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := &fakeFiles{}
	got, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "key.json", MutationMode{DryRun: true})())
	require.NoError(t, err)
	require.Equal(t, ActionDryRun, got.Action)
	require.Zero(t, api.mints)
}

func TestSAKeyListErrorNamed(t *testing.T) {
	api := &fakeSAKeyAPI{listErr: &googleapi.Error{Code: http.StatusForbidden}}
	files := &fakeFiles{}
	_, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "key.json", MutationMode{})())
	require.Error(t, err)
	require.True(t, IsPermissionDenied(err))
	require.Zero(t, api.mints, "a forbidden listing never mints")
}

func TestKeyIDFromPath(t *testing.T) {
	require.Equal(t, "abc123", keyIDFromPath("projects/p/serviceAccounts/e/keys/abc123"))
	require.Equal(t, "abc123", keyIDFromPath("abc123"))
}

// failingFiles fails the key write (simulating a full disk / bad path).
type failingFiles struct {
	fakeFiles
	mkdirs  int
	writeOK bool
}

func (f *failingFiles) MkdirAll(string, fs.FileMode) error {
	f.mkdirs++
	return nil
}

func (f *failingFiles) WriteFile(string, []byte, os.FileMode) error {
	if !f.writeOK {
		return errors.New("disk full")
	}
	return nil
}

// Review Focus 4 / C2: mint → write failure must NOT orphan a cloud key —
// the mint is compensated with a delete, and the parent dir is ready first.
func TestSAKeyWriteFailureCompensates(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := &failingFiles{}
	_, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "creds/proj/key.json", MutationMode{})())
	require.Error(t, err)
	require.Equal(t, 1, api.mints)
	require.Equal(t, 1, api.deletes, "orphaned mint is deleted again")
	require.Equal(t, "k-new-1", api.deletedID)
}

func TestSAKeyMintsAfterParentReady(t *testing.T) {
	api := &fakeSAKeyAPI{live: nil}
	files := &failingFiles{writeOK: true}
	_, err := E.UnwrapError(ConvergeSAKey(api, files, testSAEmail, "creds/proj/key.json", MutationMode{})())
	require.NoError(t, err)
	require.Equal(t, 1, files.mkdirs, "parent directory prepared before minting")
}

// The production file adapter really writes 0600 (tightened even when the
// file already existed loose).
func TestOSKeyFilesWrites0600(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/key.json"
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	require.NoError(t, os.Chmod(path, 0o644), "loosen the fixture before the converge")
	require.NoError(t, OSKeyFiles{}.WriteFile(path, []byte(`{"private_key_id":"k1"}`), keyFilePerm))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
