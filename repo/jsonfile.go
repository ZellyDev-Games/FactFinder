package repo

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type readPlanYAML struct {
	Name string `yaml:"Name"`
}

type Provider struct {
	FilePath string
	Name     string
}

func SetupPaths() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	planFolder := filepath.Join(home, "FactFinder", "Providers")
	err = os.MkdirAll(planFolder, os.ModePerm)
	if err != nil {
		return "", err
	}

	return planFolder, nil
}

func ScanReadPlans() ([]Provider, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	planFolder := filepath.Join(home, "FactFinder", "Providers")

	entries, err := os.ReadDir(planFolder)
	if err != nil {
		return nil, fmt.Errorf("read providers folder %q: %w", planFolder, err)
	}

	out := make([]Provider, 0)

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}

		dirPath := filepath.Join(planFolder, ent.Name())

		// Require both files to exist
		readPlanPath := filepath.Join(dirPath, "readplan.yml")
		factBuilderPath := filepath.Join(dirPath, "factbuilder.lua")

		if !isRegularFile(readPlanPath) || !isRegularFile(factBuilderPath) {
			continue
		}

		// Parse YAML and extract Name
		b, err := os.ReadFile(readPlanPath)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", readPlanPath, err)
		}

		var rp readPlanYAML
		if err := yaml.Unmarshal(b, &rp); err != nil {
			return nil, fmt.Errorf("parse %q: %w", readPlanPath, err)
		}
		if rp.Name == "" {
			return nil, fmt.Errorf("missing top-level Name in %q", readPlanPath)
		}

		absDir, err := filepath.Abs(dirPath)
		if err != nil {
			return nil, fmt.Errorf("abs path for %q: %w", dirPath, err)
		}

		out = append(out, Provider{
			FilePath: absDir,
			Name:     rp.Name,
		})
	}

	return out, nil
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
}
