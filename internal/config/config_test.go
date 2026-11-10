package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseCommandString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple flags",
			input:    "--configFile=/etc/traefik/traefik.yml --api.dashboard=true",
			expected: []string{"--configFile=/etc/traefik/traefik.yml", "--api.dashboard=true"},
		},
		{
			name:     "quoted flags with spaces",
			input:    `--entryPoints.web.address=":80" --defaultRule="Host('example.com')"`,
			expected: []string{"--entryPoints.web.address=:80", "--defaultRule=Host('example.com')"},
		},
		{
			name:     "single quotes",
			input:    `'--flag1=hello world' '--flag2=foo'`,
			expected: []string{"--flag1=hello world", "--flag2=foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCommandString(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("parseCommandString() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLoadTraefikCommandFromComposeFile(t *testing.T) {
	tempDir := t.TempDir()

	// Test 1: List format command
	listYaml := `
services:
  traefik:
    image: traefik:v3.7
    command:
      - --configFile=/etc/traefik/traefik_config.yml
      - --api.insecure=true
      - --api.dashboard=true
`
	listPath := filepath.Join(tempDir, "compose.list.yml")
	if err := os.WriteFile(listPath, []byte(listYaml), 0644); err != nil {
		t.Fatal(err)
	}

	cmds, err := LoadTraefikCommandFromComposeFile(listPath, "traefik")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{"--configFile=/etc/traefik/traefik_config.yml", "--api.insecure=true", "--api.dashboard=true"}
	if !reflect.DeepEqual(cmds, expected) {
		t.Errorf("got %v, want %v", cmds, expected)
	}

	// Test 2: String format command
	strYaml := `
services:
  traefik:
    command: "--configFile=/etc/traefik/config.yml --api.dashboard=true"
`
	strPath := filepath.Join(tempDir, "compose.str.yml")
	if err := os.WriteFile(strPath, []byte(strYaml), 0644); err != nil {
		t.Fatal(err)
	}

	cmdsStr, err := LoadTraefikCommandFromComposeFile(strPath, "traefik")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedStr := []string{"--configFile=/etc/traefik/config.yml", "--api.dashboard=true"}
	if !reflect.DeepEqual(cmdsStr, expectedStr) {
		t.Errorf("got %v, want %v", cmdsStr, expectedStr)
	}

	// Test 3: Service not found
	_, err = LoadTraefikCommandFromComposeFile(listPath, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent service, got nil")
	}

	// Test 4: Service has no command
	noCmdYaml := `
services:
  traefik:
    image: traefik:v3.7
`
	noCmdPath := filepath.Join(tempDir, "compose.nocmd.yml")
	if err := os.WriteFile(noCmdPath, []byte(noCmdYaml), 0644); err != nil {
		t.Fatal(err)
	}
	cmdsEmpty, err := LoadTraefikCommandFromComposeFile(noCmdPath, "traefik")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmdsEmpty) != 0 {
		t.Errorf("expected empty commands, got %v", cmdsEmpty)
	}
}

func TestFindBaseComposeFile(t *testing.T) {
	tempDir := t.TempDir()

	outPath := filepath.Join(tempDir, "docker-compose.ports.yml")
	if err := os.WriteFile(outPath, []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Should not select the outPath even if specified in composeFiles
	base := FindBaseComposeFile(tempDir, []string{"docker-compose.ports.yml"}, outPath)
	if base != "" {
		t.Errorf("expected empty base, got %q", base)
	}

	// Now create compose.yml
	composeYml := filepath.Join(tempDir, "compose.yml")
	if err := os.WriteFile(composeYml, []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}

	base = FindBaseComposeFile(tempDir, []string{"compose.yml"}, outPath)
	if base != composeYml {
		t.Errorf("got %q, want %q", base, composeYml)
	}
}
