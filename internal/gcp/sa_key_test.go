package gcp

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"testing"
	"time"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

type fakeSAKeyAPI struct {
	live    []string
	mints   int
	lastID  string
	listErr error
}

func (f *fakeSAKeyAPI) ListKeys(context.Context, string) ([]string, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.live, nil
}

func (f *fakeSAKeyAPI) CreateKey(context.Context, string) (string, []byte, error) {
	f.mints++
	f.lastID = fmt.Sprintf("k-new-%d", f.mints)
	return f.lastID, []byte(`{"private_key_id": "` + f.lastID + `"}`), nil
}

type fakeInfo string

func (f fakeInfo) Name() string       { return string(f) }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o600 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

type fakeFiles struct {
	keyFile   string // content of the key file, "" = missing
	wrote     []byte
	wroteMode os.FileMode
}

func (f *fakeFiles) Stat(string) (fs.FileInfo, error) {
	if f.keyFile == "" {
		return nil, fs.ErrNotExist
	}
	return fakeInfo("key.json"), nil
}

func (f *fakeFiles) ReadFile(string) ([]byte, error) {
	if f.keyFile == "" {
		return nil, fs.ErrNotExist
	}
	return []byte(f.keyFile), nil
}

func (f *fakeFiles) WriteFile(_ string, data []byte, perm os.FileMode) error {
	f.wrote = data
	f.wroteMode = perm
	return nil
}

const testSAEmail = "sa@proj-dcr.iam.gserviceaccount.com"

func TestSAKeyReusesLiveFile(t *testing.T) {
	api := &fakeSAKeyAPI{live: []string{"k1"}}
	files := &fakeFiles{keyFile: `{"private_key_id": "k1"}`}
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
	files := &fakeFiles{keyFile: `{"private_key_id": "k1"}`}
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
