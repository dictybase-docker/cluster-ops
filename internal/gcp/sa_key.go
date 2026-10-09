package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	iamv1 "google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

// SAKeyAPI is the injected CLOUD key surface (no file IO inside).
// ListKeys returns normalized key IDs (last path segment of
// projects/p/serviceAccounts/e/keys/<id>).
type SAKeyAPI interface {
	ListKeys(ctx context.Context, saEmail string) ([]string, error)
	CreateKey(ctx context.Context, saEmail string) (keyID string, keyJSON []byte, err error)
	// DeleteKey compensates an orphaned mint (write-after-mint failure).
	DeleteKey(ctx context.Context, saEmail, keyID string) error
}

// KeyFiles is the injected key-file IO (shared with the SA setup flows).
type KeyFiles interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
}

// keyFilePerm is the only mode key material may be written with.
const keyFilePerm os.FileMode = 0o600

// keyDirPerm is the only mode key directories may be created with.
const keyDirPerm os.FileMode = 0o700

type saKeyWork struct {
	api     SAKeyAPI
	files   KeyFiles
	saEmail string
	keyFile string
	mode    MutationMode
}

// keyState is the probe result threaded through the pipe (never mutated on
// the work struct — value receivers cannot carry state across steps).
type keyState struct {
	fileID   string   // private_key_id of the existing file ("" = missing)
	live     []string // normalized live key IDs
	mintKind Action   // ActionCreated | ActionUpdated
}

// keyMaterial is one minted key: its ID plus the raw credential JSON.
type keyMaterial struct {
	id   string
	json []byte
}

// ConvergeSAKey: file present AND its private_key_id in ListKeys →
// unchanged; file present but key gone → re-mint; file missing → mint.
// Exactly one mint in every path. DryRun reports the intent without minting.
// Key material is written by writeKeyFile OUTSIDE the cloud seam (0600).
func ConvergeSAKey(
	api SAKeyAPI, files KeyFiles, saEmail, keyFilePath string, mode MutationMode,
) IOE.IOEither[error, Result] {
	w := saKeyWork{api: api, files: files, saEmail: saEmail, keyFile: keyFilePath, mode: mode}
	return F.Pipe1(w.probeKeyState(), IOE.Chain(w.convergeFromProbe))
}

func (w saKeyWork) name() string {
	return "serviceAccounts/" + w.saEmail
}

// probeKeyState reads the local key file and the live key list. A missing
// file is a probe outcome (fileID ""), not an error.
func (w saKeyWork) probeKeyState() IOE.IOEither[error, keyState] {
	return F.Pipe1(
		IOE.TryCatchError(w.probeFile),
		IOE.Chain(w.listLiveKeys),
	)
}

func (w saKeyWork) probeFile() (string, error) {
	data, err := w.files.ReadFile(w.keyFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read key file %s: %w", w.keyFile, err)
	}
	id, err := keyFileID(data)
	if err != nil {
		return "", fmt.Errorf("parse key file %s: %w", w.keyFile, err)
	}
	return id, nil
}

func (w saKeyWork) listLiveKeys(fileID string) IOE.IOEither[error, keyState] {
	return F.Pipe2(
		IOE.TryCatchError(w.rawListKeys),
		IOE.MapLeft[[]string](w.wrapListErr),
		IOE.Map[error](w.toKeyState(fileID)),
	)
}

func (w saKeyWork) rawListKeys() ([]string, error) {
	return w.api.ListKeys(context.Background(), w.saEmail)
}

func (w saKeyWork) wrapListErr(err error) error {
	return fmt.Errorf("list keys for %s: %w", w.saEmail, err)
}

func (w saKeyWork) toKeyState(fileID string) func([]string) keyState {
	return func(live []string) keyState {
		kind := ActionCreated
		if fileID != "" {
			kind = ActionUpdated
		}
		return keyState{fileID: fileID, live: live, mintKind: kind}
	}
}

func (w saKeyWork) convergeFromProbe(st keyState) IOE.IOEither[error, Result] {
	if st.fileID != "" && slices.Contains(st.live, st.fileID) {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionUnchanged})
	}
	if w.mode.DryRun {
		return IOE.Of[error](Result{Name: w.name(), Action: ActionDryRun})
	}
	return w.mintKey(st)
}

func (w saKeyWork) mintKey(st keyState) IOE.IOEither[error, Result] {
	return F.Pipe2(
		w.prepareKeyDir(),
		IOE.Chain(w.createFromDir),
		IOE.Chain(w.persistWith(st.mintKind)),
	)
}

// prepareKeyDir makes the key's parent directory (0700) BEFORE any cloud
// mint — a path that cannot hold the file must never receive a minted key.
func (w saKeyWork) prepareKeyDir() IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, w.files.MkdirAll(filepath.Dir(w.keyFile), keyDirPerm)
	})
}

func (w saKeyWork) createFromDir(struct{}) IOE.IOEither[error, keyMaterial] {
	return IOE.TryCatchError(w.rawCreateKey)
}

func (w saKeyWork) rawCreateKey() (keyMaterial, error) {
	keyID, keyJSON, err := w.api.CreateKey(context.Background(), w.saEmail)
	return keyMaterial{id: keyID, json: keyJSON}, err
}

func (w saKeyWork) persistWith(kind Action) func(keyMaterial) IOE.IOEither[error, Result] {
	return func(k keyMaterial) IOE.IOEither[error, Result] {
		return IOE.TryCatchError(func() (Result, error) {
			return w.writeCompensated(k, kind)
		})
	}
}

// writeCompensated writes the material and compensates an orphaned mint: a
// write failure deletes the just-created cloud key (best-effort) before the
// original error propagates.
func (w saKeyWork) writeCompensated(k keyMaterial, kind Action) (Result, error) {
	if err := w.files.WriteFile(w.keyFile, k.json, keyFilePerm); err != nil {
		_ = w.api.DeleteKey(context.Background(), w.saEmail, k.id)
		return Result{}, fmt.Errorf("write key file %s: %w", w.keyFile, err)
	}
	// Audit: key ID and path only — never the material.
	slog.Info("converged SA key", "sa", w.saEmail, "keyID", k.id, "path", w.keyFile)
	return Result{Name: w.name(), Action: kind}, nil
}

// keyFileID extracts private_key_id from a key JSON blob.
func keyFileID(keyJSON []byte) (string, error) {
	var parsed struct {
		PrivateKeyID string `json:"private_key_id"`
	}
	if err := json.Unmarshal(keyJSON, &parsed); err != nil {
		return "", err
	}
	if parsed.PrivateKeyID == "" {
		return "", errors.New("key file has no private_key_id")
	}
	return parsed.PrivateKeyID, nil
}

// keyIDFromPath normalizes a keys/<id> resource path to <id>.
func keyIDFromPath(resource string) string {
	if i := strings.LastIndexByte(resource, '/'); i >= 0 {
		return resource[i+1:]
	}
	return resource
}

// ---- production adapters ----

// OSKeyFiles is the production KeyFiles adapter. WriteFile is atomic and
// tightens the mode even when the target existed loose (temp + chmod +
// rename in the same directory).
type OSKeyFiles struct{}

func (OSKeyFiles) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func (OSKeyFiles) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

func (OSKeyFiles) WriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".keytmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// IAMSAKeyAPI adapts google.golang.org/api/iam/v1 to SAKeyAPI.
type IAMSAKeyAPI struct {
	Service *iamv1.Service
}

func (a IAMSAKeyAPI) ListKeys(ctx context.Context, saEmail string) ([]string, error) {
	name := "projects/-/serviceAccounts/" + saEmail
	res, err := a.Service.Projects.ServiceAccounts.Keys.List(name).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(res.Keys))
	for _, k := range res.Keys {
		ids = append(ids, keyIDFromPath(k.Name))
	}
	return ids, nil
}

// DeleteKey removes a minted key (compensation path for orphaned mints).
func (a IAMSAKeyAPI) DeleteKey(ctx context.Context, saEmail, keyID string) error {
	name := "projects/-/serviceAccounts/" + saEmail
	_, err := a.Service.Projects.ServiceAccounts.Keys.Delete(name + "/keys/" + keyID).Context(ctx).Do()
	return err
}

func (a IAMSAKeyAPI) CreateKey(ctx context.Context, saEmail string) (string, []byte, error) {
	name := "projects/-/serviceAccounts/" + saEmail
	key, err := a.Service.Projects.ServiceAccounts.Keys.Create(
		name,
		&iamv1.CreateServiceAccountKeyRequest{PrivateKeyType: "TYPE_GOOGLE_CREDENTIALS_FILE"},
	).Context(ctx).Do()
	if err != nil {
		return "", nil, err
	}
	return keyIDFromPath(key.Name), decodeKeyData(key.PrivateKeyData), nil
}

// decodeKeyData normalizes the discovery client's PrivateKeyData string
// (base64 on the wire) to the raw credential JSON bytes.
func decodeKeyData(data string) []byte {
	if raw, err := base64.StdEncoding.DecodeString(data); err == nil && json.Valid(raw) {
		return raw
	}
	return []byte(data)
}

// NewIAMService builds an iam/v1 service from an explicit credential file
// ("" = application default credentials).
func NewIAMService(credFile string) IOE.IOEither[error, *iamv1.Service] {
	return IOE.TryCatchError(func() (*iamv1.Service, error) {
		if credFile == "" {
			return iamv1.NewService(context.Background())
		}
		return iamv1.NewService(
			context.Background(),
			option.WithAuthCredentialsFile(option.ServiceAccount, credFile),
		)
	})
}
