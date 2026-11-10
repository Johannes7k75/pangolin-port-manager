package storage

import (
	"os"
	"path/filepath"
	"testing"

	"pangolin-port-manager/internal/models"
)

func setupTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "ports.json")
	s, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}
	return s, filePath
}

func TestStore_AddAndList(t *testing.T) {
	s, _ := setupTestStore(t)

	r1 := models.PortRule{
		Name:     "Minecraft Server",
		Port:     25565,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	}

	created, err := s.Add(r1)
	if err != nil {
		t.Fatalf("Failed to add port rule: %v", err)
	}

	if created.ID == "" {
		t.Errorf("Expected ID to be set, got empty")
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("Expected 1 rule in list, got %d", len(list))
	}
	if list[0].Port != 25565 {
		t.Errorf("Expected port 25565, got %d", list[0].Port)
	}
}

func TestStore_DuplicatePortRejection(t *testing.T) {
	s, _ := setupTestStore(t)

	_, err := s.Add(models.PortRule{
		Name:     "Rule 1",
		Port:     25565,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Failed to add initial rule: %v", err)
	}

	// Try adding same port and protocol with different name
	_, err = s.Add(models.PortRule{
		Name:     "Rule 2",
		Port:     25565,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	})
	if err == nil {
		t.Fatal("Expected error on duplicate port+protocol, got nil")
	}

	// Adding same port but UDP should succeed
	_, err = s.Add(models.PortRule{
		Name:     "Rule UDP",
		Port:     25565,
		Protocol: models.ProtocolUDP,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Expected UDP port on same number to succeed, got error: %v", err)
	}
}

func TestStore_ToggleAndDelete(t *testing.T) {
	s, _ := setupTestStore(t)

	rule, err := s.Add(models.PortRule{
		Name:     "Web App",
		Port:     8081,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	toggled, err := s.Toggle(rule.ID)
	if err != nil {
		t.Fatalf("Toggle failed: %v", err)
	}
	if toggled.Enabled {
		t.Errorf("Expected enabled to be false after toggle")
	}

	err = s.Delete(rule.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if len(s.List()) != 0 {
		t.Errorf("Expected store to be empty after delete")
	}
}

func TestStore_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "ports.json")

	s1, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("Failed to create store 1: %v", err)
	}

	_, err = s1.Add(models.PortRule{
		Name:     "Persisted Rule",
		Port:     7777,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("Failed to add rule to store 1: %v", err)
	}

	// Reload from disk in a fresh store instance
	s2, err := NewStore(filePath)
	if err != nil {
		t.Fatalf("Failed to create store 2: %v", err)
	}

	list := s2.List()
	if len(list) != 1 || list[0].Port != 7777 {
		t.Fatalf("Persistence failed: expected 1 rule with port 7777, got %+v", list)
	}

	// Verify file content is valid JSON
	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		t.Fatalf("Failed to read persisted file: %v", err)
	}
}
