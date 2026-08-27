package main

import (
	"fmt"
	"regexp"
)

const (
	defaultServer         = "arangodb"
	defaultPort           = 8529
	defaultSnapshot       = "latest"
	defaultResticImage    = "restic/restic"
	defaultResticImageTag = "0.17.0"
	defaultArangoImage    = "arangodb/arangodb"
	defaultArangoImageTag = "3.12.10.1"
	defaultStorageName    = "arangodb-restore-scratch"

	scratchMountPath  = "/restore"
	dumpSubdir        = "arangodump"
	initContainerName = "restic-restore"
	mainContainerName = "arangorestore"
	jobNamePrefix     = "arangodb-restore"

	// Job names are DNS-1123 subdomains capped at 63 chars. Reserve room for
	// "arangodb-restore-" (17 chars) so jobName() never exceeds the limit.
	maxRestoreIDLen = 63 - len(jobNamePrefix) - 1
)

// restoreIDPattern enforces a DNS-1123 label: lowercase alphanumeric and
// hyphens, must not start or end with a hyphen. This value becomes part of
// the Job's metadata.name.
var restoreIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// SecretKeyPair names a Kubernetes Secret and the data key inside it.
type SecretKeyPair struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// StorageConfig describes the ephemeral scratch volume for the dump.
type StorageConfig struct {
	Class string `json:"class"`
	Size  string `json:"size"`
	Name  string `json:"name,omitempty"`
}

// ImageConfig names a container image and tag.
type ImageConfig struct {
	Name string `json:"name,omitempty"`
	Tag  string `json:"tag,omitempty"`
}

// RestoreConfig holds the complete configuration for one restore run.
//
// ConfirmTarget is a deliberate safety gate: the operator must type back
// "<namespace>/<server>/<restoreId>" exactly, so a copy-pasted or stale
// config cannot silently restore into the wrong cluster or overwrite a
// previous restore attempt's Job.
type RestoreConfig struct {
	Namespace      string        `json:"namespace"`
	Server         string        `json:"server,omitempty"`
	Port           int           `json:"port,omitempty"`
	Bucket         string        `json:"bucket"`
	Snapshot       string        `json:"snapshot,omitempty"`
	RestoreID      string        `json:"restoreId"`
	ConfirmTarget  string        `json:"confirmTarget"`
	Storage        StorageConfig `json:"storage"`
	ArangodbSecret SecretKeyPair `json:"arangodbSecret"`
	ResticSecret   SecretKeyPair `json:"resticSecret"`
	BucketSecret   SecretKeyPair `json:"bucketSecret"`
	ProjectSecret  SecretKeyPair `json:"projectSecret"`
	ResticImage    ImageConfig   `json:"resticImage,omitempty"`
	ArangoImage    ImageConfig   `json:"arangoImage,omitempty"`
}

// applyDefaults fills optional fields. Required fields (Namespace, Bucket,
// RestoreID, ConfirmTarget, Storage, and the four secret refs) are never
// defaulted — a restore target must always be explicit.
func (cfg *RestoreConfig) applyDefaults() {
	if cfg.Server == "" {
		cfg.Server = defaultServer
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.Snapshot == "" {
		cfg.Snapshot = defaultSnapshot
	}
	if cfg.Storage.Name == "" {
		cfg.Storage.Name = defaultStorageName
	}
	if cfg.ResticImage.Name == "" {
		cfg.ResticImage.Name = defaultResticImage
	}
	if cfg.ResticImage.Tag == "" {
		cfg.ResticImage.Tag = defaultResticImageTag
	}
	if cfg.ArangoImage.Name == "" {
		cfg.ArangoImage.Name = defaultArangoImage
	}
	if cfg.ArangoImage.Tag == "" {
		cfg.ArangoImage.Tag = defaultArangoImageTag
	}
}

// targetIdentity is the exact string ConfirmTarget must match.
func (cfg *RestoreConfig) targetIdentity() string {
	return fmt.Sprintf("%s/%s/%s", cfg.Namespace, cfg.Server, cfg.RestoreID)
}

// jobName is deterministic from RestoreID so a repeated apply with the same
// RestoreID updates the same Job, while a new RestoreID always creates a new
// one instead of silently colliding with a prior restore attempt.
func (cfg *RestoreConfig) jobName() string {
	return fmt.Sprintf("%s-%s", jobNamePrefix, cfg.RestoreID)
}

// Validate checks the config is complete and internally consistent. Called
// after applyDefaults, so optional fields are already filled.
func (cfg *RestoreConfig) Validate() error {
	if err := cfg.validateTarget(); err != nil {
		return err
	}
	if err := cfg.validateRestoreID(); err != nil {
		return err
	}
	if err := cfg.validateStorage(); err != nil {
		return err
	}
	if err := cfg.validateSecrets(); err != nil {
		return err
	}
	return cfg.validateConfirmTarget()
}

func (cfg *RestoreConfig) validateTarget() error {
	if cfg.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if cfg.Bucket == "" {
		return fmt.Errorf("bucket cannot be empty")
	}
	return nil
}

func (cfg *RestoreConfig) validateRestoreID() error {
	if cfg.RestoreID == "" {
		return fmt.Errorf("restoreId cannot be empty")
	}
	if len(cfg.RestoreID) > maxRestoreIDLen {
		return fmt.Errorf(
			"restoreId %q is %d chars, must be at most %d so the Job "+
				"name %q stays within the 63-char Kubernetes limit",
			cfg.RestoreID, len(cfg.RestoreID), maxRestoreIDLen, cfg.jobName(),
		)
	}
	if !restoreIDPattern.MatchString(cfg.RestoreID) {
		return fmt.Errorf(
			"restoreId %q must be a valid DNS-1123 label: lowercase "+
				"alphanumeric and hyphens, not starting or ending with a hyphen",
			cfg.RestoreID,
		)
	}
	return nil
}

func (cfg *RestoreConfig) validateStorage() error {
	if cfg.Storage.Class == "" {
		return fmt.Errorf("storage.class cannot be empty")
	}
	if cfg.Storage.Size == "" {
		return fmt.Errorf("storage.size cannot be empty")
	}
	return nil
}

func (cfg *RestoreConfig) validateSecrets() error {
	if cfg.ArangodbSecret.Name == "" || cfg.ArangodbSecret.Key == "" {
		return fmt.Errorf("arangodbSecret name and key are required")
	}
	if cfg.ResticSecret.Name == "" || cfg.ResticSecret.Key == "" {
		return fmt.Errorf("resticSecret name and key are required")
	}
	if cfg.BucketSecret.Name == "" || cfg.BucketSecret.Key == "" {
		return fmt.Errorf("bucketSecret name and key are required")
	}
	if cfg.ProjectSecret.Name == "" || cfg.ProjectSecret.Key == "" {
		return fmt.Errorf("projectSecret name and key are required")
	}
	return nil
}

func (cfg *RestoreConfig) validateConfirmTarget() error {
	if want := cfg.targetIdentity(); cfg.ConfirmTarget != want {
		return fmt.Errorf(
			"confirmTarget must exactly equal %q (namespace/server/restoreId) "+
				"to prevent an accidental restore into the wrong target; got %q",
			want, cfg.ConfirmTarget,
		)
	}
	return nil
}
