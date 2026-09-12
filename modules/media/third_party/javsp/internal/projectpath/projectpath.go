// Package projectpath resolves standalone project runtime paths.
package projectpath

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResolveConfig returns the absolute path to an explicit configuration file or
// discovers config.yml from the working directory, executable directory, and
// executable parent directory, in that order.
func ResolveConfig(explicit, cwd, executable string) (string, error) {
	candidates := []string{explicit}
	if explicit == "" {
		executableDir := filepath.Dir(executable)
		candidates = []string{
			filepath.Join(cwd, "config.yml"),
			filepath.Join(executableDir, "config.yml"),
			filepath.Join(filepath.Dir(executableDir), "config.yml"),
		}
	}

	errs := make([]error, 0, len(candidates))
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve config path %q: %w", candidate, err))
			continue
		}
		info, err := os.Stat(absolute)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", absolute, err))
			continue
		}
		if !info.Mode().IsRegular() {
			errs = append(errs, fmt.Errorf("%s: not a regular file", absolute))
			continue
		}
		return absolute, nil
	}

	return "", fmt.Errorf("find config.yml: %w", errors.Join(errs...))
}

// ResourceRoot returns the absolute directory containing the selected config.
func ResourceRoot(configPath string) (string, error) {
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return "", fmt.Errorf("resolve config path %q: %w", configPath, err)
	}
	return filepath.Dir(absConfigPath), nil
}
