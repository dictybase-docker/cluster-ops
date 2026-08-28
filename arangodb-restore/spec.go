package main

import "fmt"

// boolTrue is the string form arangorestore's boolean flags expect.
const boolTrue = "true"

// resticRepository builds the restic GCS repository URI from the bucket.
// Matches the format arangodb-backup's dump side already writes to, so a
// restore reads back from the same repository the backup created.
func resticRepository(cfg *RestoreConfig) string {
	return fmt.Sprintf("gs:%s:/", cfg.Bucket)
}

// scratchInputDirectory is where arangorestore looks for the dump once
// restic has restored it onto the scratch volume.
//
// arangodb-backup's container runs arangodump with --output-directory
// "/arangodump" (an absolute path, container root, no WORKDIR override —
// arangodb-backup's base image sets no WORKDIR). restic therefore snapshots
// the absolute path "/arangodump", and "restic restore --target
// scratchMountPath" reconstructs that absolute path under the target, giving
// "<scratchMountPath>/arangodump" here.
func scratchInputDirectory() string {
	return fmt.Sprintf("%s/%s", scratchMountPath, dumpSubdir)
}

// buildResticRestoreArgs is passed to the restic/restic image's entrypoint
// (which forwards args to the restic binary directly — no Command override
// needed for this container).
//
// When cfg.NoLock is set, --no-lock is prepended so it reaches restic as a
// global option ahead of the "restore" subcommand, which is where restic
// expects repository-level flags.
func buildResticRestoreArgs(cfg *RestoreConfig) []string {
	args := []string{}
	if cfg.NoLock {
		args = append(args, "--no-lock")
	}
	return append(args,
		"-r", resticRepository(cfg),
		"restore", cfg.Snapshot,
		"--target", scratchMountPath,
	)
}

// buildArangorestoreArgs is passed to an explicit "arangorestore" Command
// override on the arangodb/arangodb image, whose default entrypoint starts
// an arangod server rather than a restore client.
func buildArangorestoreArgs(cfg *RestoreConfig) []string {
	return []string{
		"--server.endpoint", fmt.Sprintf("http+tcp://%s:%d", cfg.Server, cfg.Port),
		"--server.username", "root",
		"--server.password", "$(ARANGO_PASSWORD)",
		"--input-directory", scratchInputDirectory(),
		"--all-databases", boolTrue,
		"--include-system-collections",
		"--create-database", boolTrue,
	}
}

func resticImageRef(cfg *RestoreConfig) string {
	return fmt.Sprintf("%s:%s", cfg.ResticImage.Name, cfg.ResticImage.Tag)
}

func arangoImageRef(cfg *RestoreConfig) string {
	return fmt.Sprintf("%s:%s", cfg.ArangoImage.Name, cfg.ArangoImage.Tag)
}
