package module

import (
	"os"
	"path/filepath"
	"testing"
)

type stubModule struct {
	desc Descriptor
	reg  func(Registrar) error
}

func (s stubModule) Descriptor() Descriptor { return s.desc }
func (s stubModule) Register(r Registrar) error {
	if s.reg == nil {
		return nil
	}
	return s.reg(r)
}

func TestUseRejectsDuplicates(t *testing.T) {
	r := NewRegistry()
	m := stubModule{desc: Descriptor{ID: "dup", Version: "0.1.0", MinimumCore: "0.1"}}
	if err := r.Use(m); err != nil {
		t.Fatalf("first Use: %v", err)
	}
	if err := r.Use(m); err == nil {
		t.Fatal("expected duplicate module error")
	}
}

func TestUseRejectsIncompatibleCore(t *testing.T) {
	r := NewRegistry()
	m := stubModule{desc: Descriptor{ID: "future", Version: "9.9.9", MinimumCore: "9"}}
	if err := r.Use(m); err == nil {
		t.Fatal("expected incompatible core error")
	}
}

func TestRegisterRejectsDuplicateToolsAndScenarios(t *testing.T) {
	r := NewRegistry()
	tools := ToolSet{Tools: []Tool{{Name: "t", Description: "d"}, {Name: "t", Description: "d2"}}}
	if err := r.RegisterTools(tools); err == nil {
		t.Fatal("expected duplicate tool error")
	}
	scenarios := ScenarioSet{Scenarios: []Scenario{{ID: "s"}, {ID: "s"}}}
	if err := r.RegisterScenarios(scenarios); err == nil {
		t.Fatal("expected duplicate scenario error")
	}
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	valid := "module:\n  id: demo\n  name: Demo\n  version: 0.1.0\nplatform:\n  minimum_version: \"0.1\"\n"
	path := filepath.Join(dir, "module.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Module.ID != "demo" {
		t.Fatalf("manifest ID = %q", m.Module.ID)
	}

	bad := "module:\n  id: \"\"\n  version: 0.1.0\nplatform:\n  minimum_version: \"0.1\"\n"
	badPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(badPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(badPath); err == nil {
		t.Fatal("expected error for manifest with empty id")
	}
}
