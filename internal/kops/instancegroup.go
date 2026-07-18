package kops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	E "github.com/IBM/fp-go/v2/either"
	F "github.com/IBM/fp-go/v2/function"
	IOE "github.com/IBM/fp-go/v2/ioeither"

	"github.com/urfave/cli/v2"
)

var templateVarPattern = regexp.MustCompile(`\$\{(\w+)(?::-([^}]*))?\}`)

// ApplyInstanceGroups processes all InstanceGroup YAML templates and applies
// them via kops replace. With --dry-run, renders and validates without applying.
func ApplyInstanceGroups(cltx *cli.Context) error {
	templateDir := cltx.String("template-dir")
	if templateDir == "" {
		templateDir = "config/kops/instancegroups"
	}
	dryRun := cltx.Bool("dry-run")

	tmplFiles, err := collectTemplates(templateDir)
	if err != nil {
		return err
	}

	return F.Pipe1(
		processTemplates(tmplFiles, dryRun),
		func(effect IOE.IOEither[error, string]) error {
			return E.Fold(
				F.Identity[error],
				func(string) error { return nil },
			)(effect())
		},
	)
}

// collectTemplates finds all .yaml.tmpl files in the template directory.
func collectTemplates(templateDir string) ([]string, error) {
	entries, err := os.ReadDir(templateDir)
	if err != nil {
		return nil, fmt.Errorf("template dir %s: %w", templateDir, err)
	}

	var tmplFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml.tmpl") {
			tmplFiles = append(tmplFiles, filepath.Join(templateDir, e.Name()))
		}
	}
	if len(tmplFiles) == 0 {
		return nil, fmt.Errorf("no .yaml.tmpl files found in %s", templateDir)
	}
	return tmplFiles, nil
}

// processTemplates iterates through templates, rendering and optionally applying each.
func processTemplates(files []string, dryRun bool) IOE.IOEither[error, string] {
	return IOE.TryCatchError(func() (string, error) {
		for _, path := range files {
			name := filepath.Base(path)

			// Pre-validation: check for unresolved env vars
			raw, _ := os.ReadFile(path)
			unresolved := findUnresolvedVars(string(raw))
			if len(unresolved) > 0 {
				fmt.Fprintf(os.Stderr, "WARNING: %s references env vars not set: %v\n", name, unresolved)
			}

			if dryRun {
				rendered, err := renderTemplate(path)
				if err != nil {
					return "", fmt.Errorf("%s: render: %w", name, err)
				}
				fmt.Printf("--- Would apply: %s ---\n%s\n", name, rendered)
				continue
			}

			if err := renderAndApply(path); err != nil {
				return "", fmt.Errorf("%s: %w", name, err)
			}
		}

		if dryRun {
			fmt.Println("\nDry-run complete. No changes applied.")
		} else {
			fmt.Println("All InstanceGroups applied.")
		}
		return "done", nil
	})
}

// renderTemplate renders a template file via envsubst.
func renderTemplate(tmplPath string) (string, error) {
	cmd := exec.Command("envsubst")
	f, err := os.Open(tmplPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cmd.Stdin = f
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("envsubst: %w", err)
	}
	return string(out), nil
}

// renderAndApply renders a template and pipes it to kops replace.
func renderAndApply(tmplPath string) error {
	rendered, err := renderTemplate(tmplPath)
	if err != nil {
		return err
	}
	cmd := exec.Command("kops", "replace", "-f", "-")
	cmd.Stdin = strings.NewReader(rendered)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findUnresolvedVars finds ${VAR} patterns where VAR is not set in the
// environment. Skips ${VAR:-default} patterns that have fallbacks.
func findUnresolvedVars(tmpl string) []string {
	matches := templateVarPattern.FindAllStringSubmatch(tmpl, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var unresolved []string

	for _, m := range matches {
		varName := m[1]
		hasDefault := m[2] != "" || strings.Contains(m[0], ":-")
		if hasDefault {
			continue
		}
		if _, ok := os.LookupEnv(varName); !ok {
			if !seen[varName] {
				unresolved = append(unresolved, varName)
				seen[varName] = true
			}
		}
	}
	return unresolved
}

// ListInstanceGroups prints all kops InstanceGroups for the current cluster.
func ListInstanceGroups(cltx *cli.Context) error {
	cmd := exec.Command("kops", "get", "instancegroups")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
