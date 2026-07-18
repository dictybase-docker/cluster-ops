package kops

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/urfave/cli/v2"
)

// DeleteCluster tears down a kOps cluster.
// Without --yes: dry-run only (prints what would be destroyed).
// With --yes: confirms (unless --non-interactive), destroys, verifies cleanup.
func DeleteCluster(cltx *cli.Context) error {
	name := cltx.String("cluster-name")
	state := cltx.String("state")
	project := cltx.String("project-id")
	if name == "" || state == "" {
		return fmt.Errorf("--cluster-name and --state are required")
	}

	dryRun := !cltx.Bool("yes")

	dryRunOut, err := runKopsCapture("delete", "cluster",
		"--name="+name, "--state="+state)
	if err != nil {
		return fmt.Errorf("kops delete dry-run failed: %w", err)
	}
	if dryRun {
		fmt.Println(dryRunOut)
		fmt.Println("\nDry-run complete. No resources touched.")
		fmt.Println("Re-run with --yes to destroy.")
		return nil
	}
	return executeTeardown(cltx, name, state, project, dryRunOut)
}

// executeTeardown runs the confirmation, destroy, and cleanup phases.
func executeTeardown(cltx *cli.Context, name, state, project, dryRunOut string) error {
	effect := F.Pipe2(
		IOE.TryCatchError(func() (string, error) {
			return "", confirmTeardown(cltx)
		}),
		IOE.Chain(func(_ string) IOE.IOEither[error, string] {
			return runKopsIOE("delete", "cluster",
				"--name="+name, "--state="+state, "--yes")
		}),
		IOE.Chain(func(_ string) IOE.IOEither[error, string] {
			return verifyTeardownCleanup(project, dryRunOut)
		}),
	)
	return E.Fold(
		F.Identity[error],
		func(string) error { return nil },
	)(effect())
}

// confirmTeardown prompts for confirmation unless --non-interactive is set.
func confirmTeardown(cltx *cli.Context) error {
	showRunningWorkloads()
	if cltx.Bool("non-interactive") {
		return nil
	}
	fmt.Print("Type 'destroy' to confirm: ")
	s := bufio.NewScanner(os.Stdin)
	s.Scan()
	if s.Text() != "destroy" {
		return fmt.Errorf("aborted")
	}
	return nil
}

// showRunningWorkloads prints pods in all namespaces (best-effort).
func showRunningWorkloads() {
	cmd := exec.Command("kubectl", "get", "pods", "--all-namespaces")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

// verifyTeardownCleanup checks GCP inventory against the dry-run output.
func verifyTeardownCleanup(project, dryRunOutput string) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		survivors := checkSurvivingResources(project, dryRunOutput)
		if len(survivors) > 0 {
			fmt.Println("WARNING: The following resources may not have been cleaned up:")
			for _, s := range survivors {
				fmt.Printf("  - %s\n", s)
			}
			return "", fmt.Errorf("%d resources survived teardown", len(survivors))
		}
		fmt.Println("Cleanup verification: all expected resources destroyed.")
		return "cleanup verified", nil
	})
}

// checkSurvivingResources inspects GCP for resources referenced in dry-run output.
func checkSurvivingResources(project, dryRunOutput string) []string {
	expected := parseDryRunOutput(dryRunOutput)
	var survivors []string
	for _, r := range expected {
		if resourceStillExists(project, r) {
			survivors = append(survivors, r)
		}
	}
	return survivors
}

// parseDryRunOutput extracts resource descriptions from kOps dry-run text.
// kOps output format: "Instance <zone>/<name>", "PersistentDisk <zone>/<name>", etc.
func parseDryRunOutput(output string) []string {
	var resources []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Instance ", "PersistentDisk ", "ForwardingRule ", "TargetPool ", "FirewallRule ", "Network "} {
			if strings.HasPrefix(line, prefix) {
				resources = append(resources, line)
				break
			}
		}
	}
	return resources
}

// resourceStillExists checks whether a GCP resource still exists.
func resourceStillExists(project, resource string) bool {
	parts := strings.SplitN(resource, " ", 2)
	if len(parts) < 2 {
		return false // unrecognized format, assume gone
	}
	resType, resName := parts[0], parts[1]

	var cmd *exec.Cmd
	switch resType {
	case "Instance", "PersistentDisk":
		// Format: "Instance us-central1-c/masters-..." or "PersistentDisk us-central1-c/..."
		zoneAndName := strings.SplitN(resName, "/", 2)
		if len(zoneAndName) != 2 {
			return false
		}
		if resType == "Instance" {
			cmd = exec.Command("gcloud", "compute", "instances", "describe",
				zoneAndName[1], "--project="+project, "--zone="+zoneAndName[0])
		} else {
			cmd = exec.Command("gcloud", "compute", "disks", "describe",
				zoneAndName[1], "--project="+project, "--zone="+zoneAndName[0])
		}
	case "ForwardingRule":
		cmd = exec.Command("gcloud", "compute", "forwarding-rules", "describe",
			resName, "--project="+project)
	case "TargetPool":
		cmd = exec.Command("gcloud", "compute", "target-pools", "describe",
			resName, "--project="+project)
	case "FirewallRule":
		cmd = exec.Command("gcloud", "compute", "firewall-rules", "describe",
			resName, "--project="+project)
	case "Network":
		cmd = exec.Command("gcloud", "compute", "networks", "describe",
			resName, "--project="+project)
	default:
		return false
	}
	return cmd.Run() == nil
}
