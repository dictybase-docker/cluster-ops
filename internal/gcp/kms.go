package gcp

import (
	"context"
	"fmt"
	"log/slog"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// keyringKeyWork carries everything the converge pipeline needs: the KMS
// client (bound once acquired via WithResource), the parent resource name,
// and the keyring/key names to ensure.
type keyringKeyWork struct {
	Client      *kms.KeyManagementClient
	ParentName  string
	KeyringName string
	KeyName     string
}

// CreateKeyringAndKey converges the keyring and crypto key named by the CLI
// flags: each is created only when missing, so re-running is safe.
func CreateKeyringAndKey(cltx *cli.Context) error {
	work := keyringKeyWork{
		ParentName: fmt.Sprintf(
			"projects/%s/locations/%s",
			cltx.String("project-id"),
			cltx.String("location"),
		),
		KeyringName: cltx.String("keyring-name"),
		KeyName:     cltx.String("key-name"),
	}
	use := ensureKeyringAndKey(work)
	credentials := cltx.String("credentials")
	acquire := newKMSClient(credentials)
	outcome := F.Pipe1(
		use,
		IOE.WithResource[struct{}](
			acquire,
			closeKMSClient,
		),
	)
	result := outcome()
	if err := E.ToError(result); err != nil {
		return err
	}
	slog.Info("Keyring and key ready",
		"keyring", work.KeyringName,
		"key", work.KeyName,
		"parent", work.ParentName,
	)
	return nil
}

func ensureKeyringAndKey(
	work keyringKeyWork,
) func(*kms.KeyManagementClient) IOE.IOEither[error, struct{}] {
	return func(c *kms.KeyManagementClient) IOE.IOEither[error, struct{}] {
		w := work
		w.Client = c
		return F.Pipe2(
			struct{}{},
			w.ensureKeyring,
			IOE.Chain(w.ensureKey),
		)
	}
}

// ensureKeyring converges the keyring: probe, then create only when missing.
// It takes and ignores its input so it composes in an IOEither pipeline.
func (w keyringKeyWork) ensureKeyring(_ struct{}) IOE.IOEither[error, struct{}] {
	return F.Pipe1(
		IOE.TryCatchError(w.probeKeyring),
		IOE.Chain(w.keyringFromProbe),
	)
}

// probeKeyring reports whether the keyring already exists.
func (w keyringKeyWork) probeKeyring() (bool, error) {
	want := fmt.Sprintf("%s/keyRings/%s", w.ParentName, w.KeyringName)
	rings := w.Client.ListKeyRings(
		context.Background(),
		&kmspb.ListKeyRingsRequest{Parent: w.ParentName},
	)
	for {
		keyring, err := rings.Next()
		if err == iterator.Done {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("failed to list keyrings: %w", err)
		}
		if keyring.Name == want {
			return true, nil
		}
	}
}

// keyringFromProbe converges the keyring: an existing one passes through,
// a missing one is created.
func (w keyringKeyWork) keyringFromProbe(exists bool) IOE.IOEither[error, struct{}] {
	if exists {
		slog.Info("Keyring already exists", "keyring", w.KeyringName)
		return IOE.Of[error](struct{}{})
	}
	slog.Info("Creating keyring", "keyring", w.KeyringName)
	return F.Pipe1(
		w.createKeyring(),
		IOE.Map[error](F.Constant1[*kmspb.KeyRing](struct{}{})),
	)
}

func (w keyringKeyWork) createKeyring() IOE.IOEither[error, *kmspb.KeyRing] {
	return IOE.TryCatchError(func() (*kmspb.KeyRing, error) {
		return w.Client.CreateKeyRing(
			context.Background(),
			&kmspb.CreateKeyRingRequest{
				Parent:    w.ParentName,
				KeyRingId: w.KeyringName,
			},
		)
	})
}

// ensureKey converges the crypto key: probe, then create only when missing.
// It takes and ignores its input so it composes in an IOEither pipeline.
func (w keyringKeyWork) ensureKey(_ struct{}) IOE.IOEither[error, struct{}] {
	return F.Pipe1(
		IOE.TryCatchError(w.probeKey),
		IOE.Chain(w.keyFromProbe),
	)
}

// probeKey uses a direct Get — NotFound means missing, any other error is real.
func (w keyringKeyWork) probeKey() (bool, error) {
	name := fmt.Sprintf(
		"%s/keyRings/%s/cryptoKeys/%s",
		w.ParentName,
		w.KeyringName,
		w.KeyName,
	)
	_, err := w.Client.GetCryptoKey(
		context.Background(),
		&kmspb.GetCryptoKeyRequest{Name: name},
	)
	if err == nil {
		return true, nil
	}
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return false, fmt.Errorf("failed to check crypto key: %w", err)
}

// keyFromProbe converges the crypto key: an existing one passes through, a
// missing one is created with ENCRYPT_DECRYPT purpose.
func (w keyringKeyWork) keyFromProbe(exists bool) IOE.IOEither[error, struct{}] {
	if exists {
		slog.Info("Crypto key already exists", "key", w.KeyName)
		return IOE.Of[error](struct{}{})
	}
	slog.Info("Creating key", "key", w.KeyName)
	return F.Pipe1(
		w.createKey(),
		IOE.Map[error](F.Constant1[*kmspb.CryptoKey](struct{}{})),
	)
}

func (w keyringKeyWork) createKey() IOE.IOEither[error, *kmspb.CryptoKey] {
	return IOE.TryCatchError(func() (*kmspb.CryptoKey, error) {
		return w.Client.CreateCryptoKey(
			context.Background(),
			&kmspb.CreateCryptoKeyRequest{
				Parent:      fmt.Sprintf("%s/keyRings/%s", w.ParentName, w.KeyringName),
				CryptoKeyId: w.KeyName,
				CryptoKey: &kmspb.CryptoKey{
					Purpose: kmspb.CryptoKey_ENCRYPT_DECRYPT,
				},
			},
		)
	})
}

func newKMSClient(credentialsFile string) IOE.IOEither[error, *kms.KeyManagementClient] {
	return F.Pipe1(
		IOE.TryCatchError(func() (*kms.KeyManagementClient, error) {
			ctx := context.Background()
			return kms.NewKeyManagementClient(
				ctx,
				option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile),
			)
		}),
		IOE.MapLeft[*kms.KeyManagementClient](func(err error) error {
			return fmt.Errorf("failed to create KMS client: %w", err)
		}),
	)
}

func closeKMSClient(c *kms.KeyManagementClient) IOE.IOEither[error, struct{}] {
	return IOE.TryCatchError(func() (struct{}, error) {
		return struct{}{}, c.Close()
	})
}
