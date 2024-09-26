package gcp

import (
	"context"
	"fmt"

	kms "cloud.google.com/go/kms/apiv1"
	"cloud.google.com/go/kms/apiv1/kmspb"
	"github.com/urfave/cli/v2"
	"google.golang.org/api/iterator"
)

func CreateKeyringAndKey(cltx *cli.Context) error {
	ctx := context.Background()
	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create KMS client: %v", err)
	}
	defer client.Close()

	projectID := cltx.String("project-id")
	keyringName := cltx.String("keyring-name")
	keyName := cltx.String("key-name")
	location := cltx.String("location")

	// The resource name of the location associated with the keyring.
	parentName := fmt.Sprintf("projects/%s/locations/%s", projectID, location)

	// Check if the keyring already exists
	keyringExists := false
	keyringIterator := client.ListKeyRings(ctx, &kmspb.ListKeyRingsRequest{
		Parent: parentName,
	})
	for {
		keyring, err := keyringIterator.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to list keyrings: %v", err)
		}
		if keyring.Name == fmt.Sprintf(
			"%s/keyRings/%s",
			parentName,
			keyringName,
		) {
			keyringExists = true
			break
		}
	}

	// Create the keyring if it doesn't exist
	if !keyringExists {
		fmt.Printf("Creating keyring %s...\n", keyringName)
		_, err = client.CreateKeyRing(ctx, &kmspb.CreateKeyRingRequest{
			Parent:    parentName,
			KeyRingId: keyringName,
		})
		if err != nil {
			return fmt.Errorf("failed to create keyring: %v", err)
		}
	} else {
		fmt.Printf("Keyring %s already exists.\n", keyringName)
	}

	// Create the key
	fmt.Printf("Creating key %s...\n", keyName)
	_, err = client.CreateCryptoKey(ctx, &kmspb.CreateCryptoKeyRequest{
		Parent:      fmt.Sprintf("%s/keyRings/%s", parentName, keyringName),
		CryptoKeyId: keyName,
		CryptoKey: &kmspb.CryptoKey{
			Purpose: kmspb.CryptoKey_ENCRYPT_DECRYPT,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create key: %v", err)
	}

	fmt.Printf(
		"Successfully created keyring %s and key %s in project %s, location %s\n",
		keyringName,
		keyName,
		projectID,
		location,
	)
	return nil
}
