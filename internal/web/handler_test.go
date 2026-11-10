package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/docker"
	"pangolin-port-manager/internal/storage"
)

func setupTestServer(t *testing.T) (*Server, *storage.Store) {
	t.Helper()
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "ports.json")
	composeOut := filepath.Join(tmpDir, "docker-compose.ports.yml")

	store, err := storage.NewStore(storePath)
	if err != nil {
		t.Fatalf("Failed to init store: %v", err)
	}

	cfg := &config.Config{
		Port:                "8080",
		PortsFilePath:       storePath,
		ComposeOutPath:      composeOut,
		GerbilServiceName:   "gerbil",
		TraefikServiceName:  "traefik",
		TraefikBaseCommands: []string{"--configFile=/etc/traefik/traefik_config.yml"},
	}

	dockerClient := docker.NewClient(cfg)
	server, err := NewServer(cfg, store, dockerClient)
	if err != nil {
		t.Fatalf("Failed to init server: %v", err)
	}

	return server, store
}

func TestServer_Healthz(t *testing.T) {
	server, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("Expected 'ok', got %q", rec.Body.String())
	}
}

func TestServer_DashboardAddEditToggleDelete(t *testing.T) {
	server, store := setupTestServer(t)

	// 1. Initial dashboard get
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Dashboard returned %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Pangolin Ports") {
		t.Errorf("Dashboard missing title")
	}

	// 2. Add port via HTMX form
	form := url.Values{}
	form.Set("name", "Minecraft Bedrock")
	form.Set("port", "19132")
	form.Set("protocol", "udp")
	form.Set("enabled", "true")

	postReq := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(form.Encode()))
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("HX-Request", "true")
	postRec := httptest.NewRecorder()

	server.Routes().ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("Add port returned %d: %s", postRec.Code, postRec.Body.String())
	}
	if postRec.Header().Get("HX-Trigger") != "portSaved" {
		t.Errorf("Expected HX-Trigger: portSaved header on success")
	}
	if !strings.Contains(postRec.Body.String(), "Minecraft Bedrock") {
		t.Errorf("Response missing added rule name")
	}

	// 3. Verify in store
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("Expected 1 rule in store, got %d", len(list))
	}
	rule := list[0]

	// 4. Edit port via HTMX form
	editForm := url.Values{}
	editForm.Set("name", "Minecraft Java")
	editForm.Set("port", "25565")
	editForm.Set("protocol", "tcp")
	editForm.Set("pangolin_resource_id", "mc-java")
	editForm.Set("enabled", "true")

	editReq := httptest.NewRequest(http.MethodPost, "/api/ports/"+rule.ID, strings.NewReader(editForm.Encode()))
	editReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	editReq.Header.Set("HX-Request", "true")
	editRec := httptest.NewRecorder()

	server.Routes().ServeHTTP(editRec, editReq)
	if editRec.Code != http.StatusOK {
		t.Fatalf("Edit port returned %d: %s", editRec.Code, editRec.Body.String())
	}
	if editRec.Header().Get("HX-Trigger") != "portSaved" {
		t.Errorf("Expected HX-Trigger: portSaved header on edit success")
	}
	updatedRule, _ := store.Get(rule.ID)
	if updatedRule.Name != "Minecraft Java" || updatedRule.Port != 25565 || updatedRule.PangolinResourceID != "mc-java" {
		t.Errorf("Expected updated fields, got %+v", updatedRule)
	}

	// 5. Toggle port
	toggleReq := httptest.NewRequest(http.MethodPost, "/api/ports/"+rule.ID+"/toggle", nil)
	toggleReq.Header.Set("HX-Request", "true")
	toggleRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(toggleRec, toggleReq)

	if toggleRec.Code != http.StatusOK {
		t.Fatalf("Toggle returned %d", toggleRec.Code)
	}

	toggledRule, _ := store.Get(rule.ID)
	if toggledRule.Enabled {
		t.Errorf("Expected rule to be disabled after toggle")
	}

	// 6. Delete port
	delReq := httptest.NewRequest(http.MethodDelete, "/api/ports/"+rule.ID, nil)
	delReq.Header.Set("HX-Request", "true")
	delRec := httptest.NewRecorder()
	server.Routes().ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("Delete returned %d", delRec.Code)
	}

	if len(store.List()) != 0 {
		t.Errorf("Expected store to be empty after delete")
	}
}

func TestServer_DuplicatePortRejection_HTMXAndREST(t *testing.T) {
	server, _ := setupTestServer(t)

	form := url.Values{}
	form.Set("name", "Minecraft")
	form.Set("port", "25565")
	form.Set("protocol", "tcp")

	req1 := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(form.Encode()))
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusCreated && rec1.Code != http.StatusOK {
		t.Fatalf("First insert failed: %d", rec1.Code)
	}

	// Second insert via REST (no HX-Request)
	form.Set("name", "Duplicate REST")
	req2 := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("Expected 400 Bad Request on REST duplicate port, got %d", rec2.Code)
	}

	// Third insert via HTMX (HX-Request: true) -> must return 422 with HX-Retarget: #form-error
	form.Set("name", "Duplicate HTMX")
	req3 := httptest.NewRequest(http.MethodPost, "/api/ports", strings.NewReader(form.Encode()))
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req3.Header.Set("HX-Request", "true")
	rec3 := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Expected 422 Unprocessable Entity on HTMX duplicate port, got %d", rec3.Code)
	}
	if rec3.Header().Get("HX-Retarget") != "#form-error" {
		t.Errorf("Expected HX-Retarget: #form-error, got %q", rec3.Header().Get("HX-Retarget"))
	}
	if !strings.Contains(rec3.Body.String(), "form-error-box") || !strings.Contains(rec3.Body.String(), "25565/tcp") {
		t.Errorf("Expected visual error HTML fragment, got %q", rec3.Body.String())
	}
}
