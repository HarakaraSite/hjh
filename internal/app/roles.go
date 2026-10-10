package app

import (
	"path/filepath"

	"hjh/internal/extensions"
)

// prepareConfig shares the locator and missing-file preparation for execution
// and catalog queries. It never replaces an existing user configuration.
func prepareConfig(workspace, configPath string) (string, error) {
	if configPath != "" {
		return filepath.Abs(configPath)
	}
	dir, err := filepath.Abs(filepath.Join(workspace, ".hjh", "extensions"))
	if err != nil {
		return "", err
	}
	if err := extensions.InstallDefaults(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Roles reads the current catalog without opening history or contacting a model.
func Roles(workspace, configPath string) (extensions.RoleCatalog, error) {
	path, err := prepareConfig(workspace, configPath)
	if err != nil {
		return extensions.RoleCatalog{}, err
	}
	return extensions.LoadCatalog(path)
}
