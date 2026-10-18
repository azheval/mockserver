package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// parseTOML uses a standards-compliant TOML parser. TOML is converted into a
// generic map and then decoded into the existing validated configuration model.
func parseTOML(data []byte, source string) (map[string]any, error) {
	root := map[string]any{}
	if err := toml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse TOML %s: %w", source, err)
	}
	return root, nil
}

func loadTOMLConfig(path string, raw []byte) (*Config, error) {
	root, err := parseTOML(raw, path)
	if err != nil {
		return nil, err
	}
	filesAny := root["scenario_files"]
	delete(root, "scenario_files")
	b, err := yaml.Marshal(root)
	if err != nil {
		return nil, err
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse TOML settings %s: %w", path, err)
	}
	base, err := absoluteDir(path)
	if err != nil {
		return nil, err
	}
	c.SourceDir = base
	if c.Server.FixturesDir == "" {
		c.Server.FixturesDir = "fixtures"
	}
	if !isAbsPath(c.Server.FixturesDir) {
		c.Server.FixturesDir = joinPath(base, c.Server.FixturesDir)
	}
	if filesAny != nil {
		files, ok := filesAny.([]any)
		if !ok {
			return nil, fmt.Errorf("%s: scenario_files must be an array of strings", path)
		}
		for _, f := range files {
			name, ok := f.(string)
			if !ok {
				return nil, fmt.Errorf("%s: scenario_files entries must be strings", path)
			}
			full := name
			if !isAbsPath(full) {
				full = joinPath(base, full)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return nil, fmt.Errorf("read scenario file %s: %w", full, err)
			}
			routes, scenarios, err := LoadDSL(data, full)
			if err != nil {
				return nil, err
			}
			c.Routes = append(c.Routes, routes...)
			c.Scenarios = append(c.Scenarios, scenarios...)
		}
	}
	return &c, nil
}

// Keep path handling in this file small and platform-native.
func absoluteDir(path string) (string, error) { return filepath.Abs(filepath.Dir(path)) }
func isAbsPath(path string) bool              { return filepath.IsAbs(path) }
func joinPath(a, b string) string             { return filepath.Join(a, b) }
