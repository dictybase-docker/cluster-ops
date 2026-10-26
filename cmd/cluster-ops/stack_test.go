package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	E "github.com/IBM/fp-go/v2/either"
	"github.com/dictybase-docker/cluster-ops/internal/pulumiauto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// dashFolder is the shared --folder flag literal in the help contracts.
const (
	dashFolder = "--folder"
	dashStack  = "--stack"
)

func stackTestApp() *cli.App {
	app := &cli.App{
		Name:           appName,
		Commands:       []*cli.Command{stackCommand()},
		ExitErrHandler: func(*cli.Context, error) {},
	}
	return app
}

func stackHelp(t *testing.T, args ...string) string {
	t.Helper()
	app := stackTestApp()
	var buf bytes.Buffer
	app.Writer = &buf
	require.NoError(t, app.Run(append([]string{appName, stackFlag}, args...)))
	return buf.String()
}

// Review Focus: the flag contracts the wrappers pass argv against.
func TestStackTreeFlagContract(t *testing.T) {
	for sub, flags := range map[string][]string{
		"ensure":   {dashFolder, dashStack, "--create", "--copy-config-from"},
		"preview":  {dashFolder, dashStack, "--allow-protected"},
		"up":       {dashFolder, dashStack, "--dry-run", "--allow-protected"},
		destroyCmd: {dashFolder, dashStack, "--dry-run"},
		"outputs":  {dashFolder, dashStack, "--key"},
	} {
		out := stackHelp(t, sub, "--help")
		for _, flag := range flags {
			require.Contains(t, out, flag, "%s must accept %s", sub, flag)
		}
	}
}

// The dry-run and protected-override switches map onto DeployMode.
func TestDeployModeFromFlags(t *testing.T) {
	require.Equal(t, pulumiauto.DeployMode{}, deployModeFrom(false, false))
	require.Equal(t, pulumiauto.DeployMode{DryRun: true}, deployModeFrom(true, false))
	require.Equal(t, pulumiauto.DeployMode{AllowProtected: true}, deployModeFrom(false, true))
}

// errReader fails every Read; stands in for a broken stdin pipe.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

// The set-secret recipe pipes the hidden prompt; the CLI must trim the
// trailing newline and surface read failures as errors.
func TestReadSecretFromStdin(t *testing.T) {
	stdinIO := readSecretFromStdin(strings.NewReader("s3cr3t\n"))
	secret, err := E.UnwrapError(stdinIO())
	require.NoError(t, err)
	require.Equal(t, "s3cr3t", secret)

	stdinErr := readSecretFromStdin(errReader{})
	r, err2 := E.UnwrapError(stdinErr())
	require.Error(t, err2)
	require.Empty(t, r)
}

func TestStackConfigSetNormalizesDotIndexKey(t *testing.T) {
	opener := &fakeOpener{stack: &fakeStack{name: "s1"}}
	restoreOpener := newOpener
	newOpener = func() pulumiauto.StackOpener { return opener }
	defer func() { newOpener = restoreOpener }()

	t.Setenv("PULUMI_BACKEND_URL", "gs://test-bucket")
	t.Setenv("PULUMI_SECRET_PROVIDER", "gcpkms://projects/p/locations/l/keyRings/r/cryptoKeys/k")

	app := stackTestApp()
	err := app.Run([]string{
		appName,
		stackFlag,
		"config",
		"set",
		dashFolder, "gateway",
		dashStack, "unregistered-stack",
		"--key", "properties.routes.graphql.hosts.0",
		"--value", "slime.dictycr.org",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"set-config:properties.routes.graphql.hosts[0]"}, opener.stack.log)
}
