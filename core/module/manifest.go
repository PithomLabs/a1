package module

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Manifest is the validated form of a module's module.yaml.
type Manifest struct {
	Module struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
		Status      string `yaml:"status"`
	} `yaml:"module"`
	Platform struct {
		MinimumVersion string `yaml:"minimum_version"`
	} `yaml:"platform"`
	Capabilities []string `yaml:"capabilities"`
	Dependencies []string `yaml:"dependencies"`
	Database     struct {
		Schemas []string `yaml:"schemas"`
	} `yaml:"database"`
	Entrypoint struct {
		Registration string `yaml:"registration"`
	} `yaml:"entrypoint"`
}

// LoadManifest parses and validates a module.yaml file. The manifest is
// descriptive metadata only; registration happens through Go code.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.Module.ID == "" {
		return nil, fmt.Errorf("module.id is required")
	}
	if m.Module.Version == "" {
		return nil, fmt.Errorf("module.version is required")
	}
	if m.Platform.MinimumVersion == "" {
		return nil, fmt.Errorf("platform.minimum_version is required")
	}
	if !coreSatisfied(m.Platform.MinimumVersion) {
		return nil, fmt.Errorf("module requires core %s (have %s)", m.Platform.MinimumVersion, CoreVersion)
	}
	return &m, nil
}
