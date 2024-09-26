package gcp

import (
	"context"
	"fmt"
	"log/slog"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type KeyringAndKeyParams struct {
	ProjectID       string
	KeyringName     string
	KeyName         string
	Location        string
	CredentialsFile string
}

type KeyringParams struct {
	Ctx         context.Context
	Client      *kms.KeyManagementClient
	ParentName  string
	KeyringName string
}

type KeyParams struct {
	Ctx         context.Context
	Client      *kms.KeyManagementClient
	ParentName  string
	KeyringName string
	KeyName     string
}

func CreateKeyringAndKey(cltx *cli.Context) error {
	ctx := context.Background()

	params := KeyringAndKeyParams{
		ProjectID:       cltx.String("project-id"),
		KeyringName:     cltx.String("keyring-name"),
		KeyName:         cltx.String("key-name"),
		Location:        cltx.String("location"),
		CredentialsFile: cltx.String("credentials"),
	}

	client, err := createKMSClient(ctx, params.CredentialsFile)
	if err != nil {
		return err
	}
	defer client.Close()

	parentName := fmt.Sprintf("projects/%s/locations/%s", params.ProjectID, params.Location)

	keyringParams := KeyringParams{
		Ctx:         ctx,
		Client:      client,
		ParentName:  parentName,
		KeyringName: params.KeyringName,
	}
	if err := createKeyringIfNotExists(keyringParams); err != nil {
		return err
	}

	keyParams := KeyParams{
		Ctx:         ctx,
		Client:      client,
		ParentName:  parentName,
		KeyringName: params.KeyringName,
		KeyName:     params.KeyName,
	}
	if err := createKey(keyParams); err != nil {
		return err
	}

	slog.Info("Successfully created keyring and key",
		"keyring", params.KeyringName,
		"key", params.KeyName,
		"project", params.ProjectID,
		"location", params.Location,
	)
	return nil
}

func createKMSClient(
	ctx context.Context,
	credentialsFile string,
) (*kms.KeyManagementClient, error) {
	client, err := kms.NewKeyManagementClient(
		ctx,
		option.WithCredentialsFile(credentialsFile),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create KMS client: %v", err)
	}
	return client, nil
}

func createKeyringIfNotExists(params KeyringParams) error {
	keyringExists, err := checkKeyringExists(params)
	if err != nil {
		return err
	}

	if !keyringExists {
		slog.Info("Creating keyring", "keyring", params.KeyringName)
		_, err = params.Client.CreateKeyRing(params.Ctx, &kmspb.CreateKeyRingRequest{
			Parent:    params.ParentName,
			KeyRingId: params.KeyringName,
		})
		if err != nil {
			return fmt.Errorf("failed to create keyring: %v", err)
		}
	} else {
		slog.Info("Keyring already exists", "keyring", params.KeyringName)
	}
	return nil
}

func checkKeyringExists(params KeyringParams) (bool, error) {
	keyringIterator := params.Client.ListKeyRings(params.Ctx, &kmspb.ListKeyRingsRequest{
		Parent: params.ParentName,
	})
	for {
		keyring, err := keyringIterator.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return false, fmt.Errorf("failed to list keyrings: %v", err)
		}
		if keyring.Name == fmt.Sprintf(
			"%s/keyRings/%s",
			params.ParentName,
			params.KeyringName,
		) {
			return true, nil
		}
	}
	return false, nil
}

func createKey(params KeyParams) error {
	slog.Info("Creating key", "key", params.KeyName)
	_, err := params.Client.CreateCryptoKey(params.Ctx, &kmspb.CreateCryptoKeyRequest{
		Parent:      fmt.Sprintf("%s/keyRings/%s", params.ParentName, params.KeyringName),
		CryptoKeyId: params.KeyName,
		CryptoKey: &kmspb.CryptoKey{
			Purpose: kmspb.CryptoKey_ENCRYPT_DECRYPT,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create key: %v", err)
	}
	return nil
}
