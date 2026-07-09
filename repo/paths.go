package repo

import (
	"os"
	"path/filepath"

	"FactFinder/platform"
)

type Paths struct {
	AppDir      string
	ProviderDir string
	LogDir      string
}

func SetupPaths() (*Paths, error) {
	fp := platform.NewFileRuntime()

	base, err := fp.UserConfigDir()
	if err != nil {
		return nil, err
	}

	appDir := filepath.Join(base, "FactFinder")

	paths := &Paths{
		AppDir:      appDir,
		ProviderDir: filepath.Join(appDir, "Providers"),
		LogDir:      filepath.Join(appDir, "logs"),
	}

	for _, dir := range []string{
		paths.AppDir,
		paths.ProviderDir,
		paths.LogDir,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
	}

	return paths, nil
}
