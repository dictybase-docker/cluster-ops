package registry

import (
	"fmt"
	"regexp"

	E "github.com/IBM/fp-go/v2/either"
)

// KMSKey represents a parsed GCP KMS key URI.
type KMSKey struct {
	Project  string
	Location string
	KeyRing  string
	Key      string
}

var kmsURIPattern = regexp.MustCompile(
	`^gcpkms://projects/([^/]+)/locations/([^/]+)/keyRings/([^/]+)/cryptoKeys/([^/?]+)`,
)

// ParseKMSKey parses a GCP KMS URI into its components.
// Format: gcpkms://projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY
func ParseKMSKey(uri string) E.Either[error, KMSKey] {
	matches := kmsURIPattern.FindStringSubmatch(uri)
	if matches == nil {
		return E.Left[KMSKey](fmt.Errorf("malformed KMS URI: %s", uri))
	}
	return E.Right[error](KMSKey{
		Project:  matches[1],
		Location: matches[2],
		KeyRing:  matches[3],
		Key:      matches[4],
	})
}
