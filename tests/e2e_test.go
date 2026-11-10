package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/docker"
	"pangolin-port-manager/internal/models"
	"pangolin-port-manager/internal/storage"
	"pangolin-port-manager/internal/web"
)

// setupMockEnvironment prepares a complete temporary directory mimicking the full Pangolin stack,
// including a mock docker wrapper that validates compose files using real `docker compose config`.
func setupMockEnvironment(t *testing.T) (string, *config.Config, *storage.Store, *docker.Client, *web.Server) {
	t.Helper()
	tempDir := t.TempDir()

	// 1. Write a realistic main compose file (even including a legacy `include:` block to verify auto-sanitization)
	composeContent := `name: pangolin-stack
include:
  - path: docker-compose.ports.yml
    required: false

services:
  gerbil:
    image: alpine:latest
    ports:
      - "80:80"
      - "443:443"
  traefik:
    image: alpine:latest
    network_mode: service:gerbil
    command:
      - --configFile=/etc/traefik/traefik_config.yml
      - --api.dashboard=true
`
	mainComposePath := filepath.Join(tempDir, "compose.example.yml")
	if err := os.WriteFile(mainComposePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("Failed to write mock compose file: %v", err)
	}

	// 2. Create a mock `docker` CLI script that translates `up -d ...` into real `docker compose ... config`
	// so every Apply validates the real merged YAML via Docker Compose without requiring a live daemon socket.
	mockDockerPath := filepath.Join(tempDir, "mock-docker.sh")
	mockScript := `#!/usr/bin/env bash
set -e
args=()
for arg in "$@"; do
  if [ "$arg" = "up" ]; then
    args+=("config")
    break
  fi
  args+=("$arg")
done
/usr/bin/docker "${args[@]}" >/dev/null
echo "Container gerbil Recreated"
echo "Container pp-traefik Recreated"
`
	if err := os.WriteFile(mockDockerPath, []byte(mockScript), 0755); err != nil {
		t.Fatalf("Failed to write mock docker script: %v", err)
	}

	portsFilePath := filepath.Join(tempDir, "data", "ports.json")
	composeOutPath := filepath.Join(tempDir, "docker-compose.ports.yml")

	cfg := &config.Config{
		Port:                "8080",
		DataDir:             filepath.Join(tempDir, "data"),
		PortsFilePath:       portsFilePath,
		ComposeOutPath:      composeOutPath,
		ComposeProjectDir:   tempDir,
		ComposeFiles:        []string{"compose.example.yml"},
		GerbilServiceName:   "gerbil",
		TraefikServiceName:  "traefik",
		TraefikBaseCommands: []string{"--configFile=/etc/traefik/traefik_config.yml", "--api.dashboard=true"},
		DockerBin:           mockDockerPath,
	}

	store, err := storage.NewStore(portsFilePath)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	dockerClient := docker.NewClient(cfg)
	server, err := web.NewServer(cfg, store, dockerClient)
	if err != nil {
		t.Fatalf("Failed to create web server: %v", err)
	}

	return tempDir, cfg, store, dockerClient, server
}

func TestE2E_FullLifecycle(t *testing.T) {
	tempDir, cfg, store, _, server := setupMockEnvironment(t)
	handler := server.Routes()

	// 1. Initial Dashboard View
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for dashboard, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pangolin Ports") {
		t.Errorf("Dashboard missing title")
	}
	if !strings.Contains(body, "No port mappings configured") {
		t.Errorf("Expected empty state initially")
	}

	// 2. Add Port Rule (Minecraft 25565/tcp)
	form := url.Values{}
	form.Set("name", "Minecraft Survival")
	form.Set("port", "25565")
	form.Set("protocol", "tcp")
	form.Set("enabled", "true")
	form.Set("pangolin_resource_id", "mc-survival")

	addReq := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(form.Encode()))
	addReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addReq.Header.Set("HX-Request", "true")
	addRec := httptest.NewRecorder()
	handler.ServeHTTP(addRec, addReq)

	if addRec.Code != http.StatusOK {
		t.Fatalf("Add port failed with status %d: %s", addRec.Code, addRec.Body.String())
	}
	addResp := addRec.Body.String()

	if !strings.Contains(addResp, `id="status-banner" hx-swap-oob="true"`) {
		t.Fatalf("Expected OOB swap for status banner in HTMX response, got:\n%s", addResp)
	}
	if !strings.Contains(addResp, "Unapplied changes") {
		t.Errorf("Expected status banner to show unapplied changes alert")
	}
	if !strings.Contains(addResp, "Apply changes") {
		t.Errorf("Expected Apply changes button in status banner OOB response")
	}

	// 3. Toggle Rule (Disable & Re-enable)
	rules := store.List()
	if len(rules) != 1 {
		t.Fatalf("Expected 1 rule in store, got %d", len(rules))
	}
	ruleID := rules[0].ID

	toggleReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/ports/%s/toggle", ruleID), nil)
	toggleReq.Header.Set("HX-Request", "true")
	toggleRec := httptest.NewRecorder()
	handler.ServeHTTP(toggleRec, toggleReq)

	if toggleRec.Code != http.StatusOK {
		t.Fatalf("Toggle failed with code %d", toggleRec.Code)
	}
	toggleResp := toggleRec.Body.String()
	if !strings.Contains(toggleResp, `id="status-banner" hx-swap-oob="true"`) {
		t.Fatalf("Toggle response missing reactive status banner OOB update")
	}
	if !strings.Contains(toggleResp, "Disabled") {
		t.Errorf("Rule should show as Disabled")
	}

	// Re-enable
	toggleReq2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/ports/%s/toggle", ruleID), nil)
	toggleReq2.Header.Set("HX-Request", "true")
	toggleRec2 := httptest.NewRecorder()
	handler.ServeHTTP(toggleRec2, toggleReq2)
	if toggleRec2.Code != http.StatusOK {
		t.Fatalf("Re-enable toggle failed")
	}

	// 4. Apply Endpoint (executes mock-docker.sh which runs real `docker compose -f ... config`)
	applyReq := httptest.NewRequest(http.MethodPost, "/api/apply?sync=1", nil)
	applyReq.Header.Set("HX-Request", "true")
	applyRec := httptest.NewRecorder()
	handler.ServeHTTP(applyRec, applyReq)

	if applyRec.Code != http.StatusOK {
		t.Fatalf("Apply returned error %d: %s", applyRec.Code, applyRec.Body.String())
	}
	if !strings.Contains(applyRec.Body.String(), "Synced") {
		t.Fatalf("Expected Synced banner after Apply, got:\n%s", applyRec.Body.String())
	}

	// Verify base compose file had `include:` stripped and multi-file `-f` config succeeds
	cmd := exec.Command("docker", "compose", "--project-directory", tempDir, "-f", filepath.Join(tempDir, "compose.example.yml"), "-f", cfg.ComposeOutPath, "config")
	cmd.Dir = tempDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config validation failed: %v\nOutput:\n%s", err, string(output))
	}
	if !strings.Contains(string(output), "25565") {
		t.Errorf("Merged compose config does not contain port 25565:\n%s", string(output))
	}

	// 5. Delete Port Rule
	delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/ports/%s", ruleID), nil)
	delReq.Header.Set("HX-Request", "true")
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("Delete failed with code %d", delRec.Code)
	}
	delResp := delRec.Body.String()
	if !strings.Contains(delResp, "No port mappings configured") {
		t.Errorf("Expected empty state after delete")
	}
}

func TestE2E_JSONAPI(t *testing.T) {
	_, _, _, _, server := setupMockEnvironment(t)
	handler := server.Routes()

	bodyJSON := `{"name":"Terraria Server","port":7777,"protocol":"tcp","enabled":true,"pangolin_resource_id":"terraria"}`
	req := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d: %s", rec.Code, rec.Body.String())
	}

	var created models.PortRule
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("Failed to decode response JSON: %v", err)
	}
	if created.Port != 7777 || created.Name != "Terraria Server" {
		t.Errorf("Unexpected created rule: %+v", created)
	}
}

func TestE2E_BrowserPlaywright(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Skipping Playwright test: python3 not found")
	}
	if err := exec.Command("python3", "-c", "import playwright").Run(); err != nil {
		t.Skip("Skipping Playwright test: python playwright module not installed")
	}

	_, _, _, _, server := setupMockEnvironment(t)
	ts := httptest.NewServer(server.Routes())
	defer ts.Close()

	cmd := exec.Command("python3", "./playwright_e2e.py", ts.URL)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Playwright browser E2E test failed: %v\nOutput:\n%s", err, string(out))
	}
}
