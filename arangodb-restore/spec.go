package main

import "fmt"

// boolTrue is the string form arangorestore's boolean flags expect.
const boolTrue = "true"

// CLI flag names shared by the restic and arangorestore argument builders.
// They form the interface between this program and two external binaries: a
// typo here is a failed restore, so both the builders and their tests use
// these names instead of repeating string literals.
const (
	argRepository      = "-r"
	argNoLock          = "--no-lock"
	argRestore         = "restore"
	argTarget          = "--target"
	argInclude         = "--include"
	argServerEndpoint  = "--server.endpoint"
	argServerUsername  = "--server.username"
	argServerPassword  = "--server.password"
	argServerDatabase  = "--server.database"
	argInputDirectory  = "--input-directory"
	argAllDatabases    = "--all-databases"
	argSystemColls     = "--include-system-collections"
	argCreateDatabase  = "--create-database"
	argOverwrite       = "--overwrite"
	argRootUser        = "root"
	argPasswordFromEnv = "$(ARANGO_PASSWORD)"
)

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
//
// With Database set, the restic side restores only "/arangodump/<db>" (see
// buildResticRestoreArgs), so arangorestore's input directory is that same
// per-database subdirectory.
func scratchInputDirectory(cfg *RestoreConfig) string {
	if cfg.Database != "" {
		return fmt.Sprintf("%s/%s/%s", scratchMountPath, dumpSubdir, cfg.Database)
	}
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
		args = append(args, argNoLock)
	}
	args = append(args,
		argRepository, resticRepository(cfg),
		argRestore, cfg.Snapshot,
		argTarget, scratchMountPath,
	)
	// --include filters on the ORIGINAL absolute path recorded in the
	// snapshot (/arangodump/<db>), not on anything under --target, so a
	// single-database restore reconstructs exactly that database's subtree.
	// With no Database set, the whole /arangodump tree is restored as before.
	if cfg.Database != "" {
		args = append(args, argInclude, fmt.Sprintf("/%s/%s", dumpSubdir, cfg.Database))
	}
	return args
}

// buildArangorestoreArgs is passed to an explicit "arangorestore" Command
// override on the arangodb image, whose default entrypoint starts
// an arangod server rather than a restore client.
func buildArangorestoreArgs(cfg *RestoreConfig) []string {
	args := []string{
		argServerEndpoint, fmt.Sprintf("http+tcp://%s:%d", cfg.Server, cfg.Port),
		argServerUsername, argRootUser,
		argServerPassword, argPasswordFromEnv,
		argInputDirectory, scratchInputDirectory(cfg),
	}
	// Single-database mode takes the database from --server.database and
	// reads only that database's dump subdirectory; whole-instance mode keeps
	// sweeping every subdirectory with --all-databases, unchanged.
	if cfg.Database != "" {
		args = append(args, argServerDatabase, cfg.Database)
	} else {
		args = append(args, argAllDatabases, boolTrue)
	}
	args = append(args, argSystemColls, argCreateDatabase, boolTrue)
	// --overwrite never reaches arangorestore on the whole-instance path:
	// Validate() rejects overwrite without database, and this branch keeps the
	// argument list honest about that.
	if cfg.Database != "" && cfg.Overwrite {
		args = append(args, argOverwrite, boolTrue)
	}
	return args
}

func resticImageRef(cfg *RestoreConfig) string {
	return fmt.Sprintf("%s:%s", cfg.ResticImage.Name, cfg.ResticImage.Tag)
}

func arangoImageRef(cfg *RestoreConfig) string {
	return fmt.Sprintf("%s:%s", cfg.ArangoImage.Name, cfg.ArangoImage.Tag)
}
