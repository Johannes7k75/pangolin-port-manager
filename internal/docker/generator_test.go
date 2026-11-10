package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/models"
)

func TestGenerateComposeYAML(t *testing.T) {
	cfg := &config.Config{
		GerbilServiceName:   "gerbil",
		TraefikServiceName:  "traefik",
		TraefikBaseCommands: []string{"--configFile=/etc/traefik/traefik_config.yml", "--api.dashboard=true"},
	}

	rules := []models.PortRule{
		{
			ID:       "1",
			Name:     "Minecraft",
			Port:     25565,
			Protocol: models.ProtocolTCP,
			Enabled:  true,
		},
		{
			ID:       "2",
			Name:     "Voice",
			Port:     9987,
			Protocol: models.ProtocolUDP,
			Enabled:  true,
		},
		{
			ID:       "3",
			Name:     "Disabled Web",
			Port:     8085,
			Protocol: models.ProtocolTCP,
			Enabled:  false,
		},
	}

	yaml := GenerateComposeYAML(rules, cfg)

	// Check gerbil ports
	if !strings.Contains(yaml, "\"9987:9987/udp\"") {
		t.Errorf("Expected 9987 udp port on gerbil, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "\"25565:25565/tcp\"") {
		t.Errorf("Expected 25565 tcp port on gerbil, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "8085") {
		t.Errorf("Disabled port 8085 should NOT be present in generated yaml, got:\n%s", yaml)
	}

	// Check Traefik entrypoints
	if !strings.Contains(yaml, "\"--entryPoints.tcp-25565.address=:25565/tcp\"") {
		t.Errorf("Expected tcp-25565 entrypoint on traefik, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "\"--entryPoints.udp-9987.address=:9987/udp\"") {
		t.Errorf("Expected udp-9987 entrypoint on traefik, got:\n%s", yaml)
	}
	// Check base commands
	if !strings.Contains(yaml, "\"--configFile=/etc/traefik/traefik_config.yml\"") {
		t.Errorf("Expected base command preserved, got:\n%s", yaml)
	}
}

func TestWriteComposeFileAndPendingChanges(t *testing.T) {
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "docker-compose.ports.yml")

	cfg := &config.Config{
		ComposeOutPath:      outPath,
		GerbilServiceName:   "gerbil",
		TraefikServiceName:  "traefik",
		TraefikBaseCommands: []string{"--api.dashboard=true"},
	}

	rules := []models.PortRule{
		{
			ID:       "1",
			Name:     "Minecraft",
			Port:     25565,
			Protocol: models.ProtocolTCP,
			Enabled:  true,
		},
	}

	// Before file exists, HasPendingChanges should be true
	if !HasPendingChanges(rules, cfg) {
		t.Errorf("Expected pending changes when file doesn't exist")
	}

	// Write file
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatalf("Failed to write compose file: %v", err)
	}

	// Now file is in sync, HasPendingChanges should be false
	if HasPendingChanges(rules, cfg) {
		t.Errorf("Expected NO pending changes immediately after write")
	}

	// Add another rule -> pending should be true again
	rules = append(rules, models.PortRule{
		ID:       "2",
		Name:     "Terraria",
		Port:     7777,
		Protocol: models.ProtocolTCP,
		Enabled:  true,
	})
	if !HasPendingChanges(rules, cfg) {
		t.Errorf("Expected pending changes after adding new rule")
	}

	// Verify file is readable and non-empty
	data, err := os.ReadFile(outPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("Output file is missing or empty")
	}
}
