package util

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
)

/*
	The SetEnvironmentalVariable function modifies the `.envrc` file at the root of the project.

It first checks whether the environmental variable is defined in the `.envrc` file.
If it is not defined, the function simply appends the environmental variable definition on a new line in the file.
If it already defined, the function will update the value of that environmental variable
*/
func SetEnvironmentalVariable(cltx *cli.Context) error {
	envName := cltx.String("name")
	envValue := cltx.String("value")
	envrcPath := ".envrc"

	// Check if the environmental configuration file exists.
	_, err := os.Stat(envrcPath)
	if os.IsNotExist(err) {
		// Create the file if it does not exist.
		file, err := os.Create(envrcPath)
		if err != nil {
			return err
		}
		file.Close()
	}

	// Open the file.
	file, err := os.OpenFile(envrcPath, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lines := []string{}
	found := false
	// This loop reads each line from the file.
	for scanner.Scan() {
		line := scanner.Text()
		// This condition checks if the current line starts with "export {envName}=".
		// If it does, it means the environment variable is already defined in the file.
		if strings.HasPrefix(line, fmt.Sprintf("export %s=", envName)) {
			// In this case, we update the line with the new value.
			line = fmt.Sprintf("export %s=%s", envName, envValue)
			found = true
		}
		// We append the current line (either the original or the updated one) to the lines slice.
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// If the environment variable was not found in the file, we append a new line to define it.
	if !found {
		lines = append(lines, fmt.Sprintf("export %s=%s", envName, envValue))
	}

	// Truncate the file and write the updated lines
	err = file.Truncate(0)
	if err != nil {
		return err
	}
	_, err = file.Seek(0, 0)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Fprintln(file, line)
	}

	return nil
}
