package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

/* The SetEnvironmentalVariable function modifies the `.envrc` file at the root of the project. 
It first checks whether the environmental variable is defined in the `.envrc` file. 
If it is not defined, the function simply appends the environmental variable definition on a new line in the file. 
If it already defined, the function will update the value of that environmental variable
*/
func SetEnvironmentalVariable(cltx *cli.Context) error {
	envName := cltx.String("name")
	envValue := cltx.String("value")

	envrcPath := ".envrc"
	_, err := os.Stat(envrcPath)
	if os.IsNotExist(err) {
		file, err := os.Create(envrcPath)
		if err != nil {
			return err
		}
		file.Close()
	}

	file, err := os.Open(envrcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := []string{}
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, fmt.Sprintf("export %s=", envName)) {
			line = fmt.Sprintf("export %s=%s", envName, envValue)
			found = true
		}
		lines = append(lines, line)
	}

	if !found {
		lines = append(lines, fmt.Sprintf("export %s=%s", envName, envValue))
	}

	file.Close()
	file, err = os.OpenFile(envrcPath, os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	for _, line := range lines {
		fmt.Fprintln(file, line)
	}

	return nil
}
