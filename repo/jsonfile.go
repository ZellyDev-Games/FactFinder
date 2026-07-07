package repo

import (
	"FactFinder/logger"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

var log = logger.Module("repo/jsonfile").SetLevel(logger.DebugLevel)

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

	log.Info("provider folder initialized: %s", planFolder)

	return planFolder, nil
}

func ScanReadPlans() ([]Provider, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	log.Debug("scanning provider folder")

	planFolder := filepath.Join(home, "FactFinder", "Providers")

	entries, err := os.ReadDir(planFolder)
	if err != nil {
		return nil, fmt.Errorf("read providers folder %q: %w", planFolder, err)
	}

	log.Debug("found %d entries in provider folder", len(entries))
	out := make([]Provider, 0)

	for _, ent := range entries {
		if !ent.IsDir() {
			log.Debug("skipping non-directory entry: %s", ent.Name())
			continue
		}

		dirPath := filepath.Join(planFolder, ent.Name())

		// Require both files to exist
		readPlanPath := filepath.Join(dirPath, "readplan.yml")
		factBuilderPath := filepath.Join(dirPath, "factbuilder.lua")

		if !isRegularFile(readPlanPath) || !isRegularFile(factBuilderPath) {
			log.Debug("skipping %s: missing readplan.yml or factbuilder.lua", ent.Name())
			continue
		}

		// Parse YAML and extract Name
		b, err := os.ReadFile(readPlanPath)
		if err != nil {
			log.Error("failed reading %s: %v", readPlanPath, err)
			return nil, fmt.Errorf("read %q: %w", readPlanPath, err)
		}

		var rp readPlanYAML
		if err := yaml.Unmarshal(b, &rp); err != nil {
			log.Error("failed parsing yaml %s: %v", readPlanPath, err)
			return nil, fmt.Errorf("parse %q: %w", readPlanPath, err)
		}

		if rp.Name == "" {
			log.Warn("skipping provider with missing Name: %s", readPlanPath)
			continue
		}

		absDir, err := filepath.Abs(dirPath)
		if err != nil {
			log.Error("failed resolving absolute path for %s: %v", dirPath, err)
			return nil, fmt.Errorf("abs path for %q: %w", dirPath, err)
		}

		log.Debug("loaded provider: %s (%s)", rp.Name, absDir)

		out = append(out, Provider{
			FilePath: absDir,
			Name:     rp.Name,
		})
	}

	log.Info("loaded %d providers", len(out))

	return out, nil
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
}
