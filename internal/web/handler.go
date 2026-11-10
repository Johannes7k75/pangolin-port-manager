package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/docker"
	"pangolin-port-manager/internal/models"
	"pangolin-port-manager/internal/storage"
)

//go:embed templates/* static/*
var contentFS embed.FS

type PageData struct {
	Ports             []models.PortRule
	HasPendingChanges bool
	IsApplying        bool
	LastAppliedAt     *time.Time
	LastApplyResult   *docker.ApplyResult
	Config            *config.Config
}

type YamlPreviewData struct {
	FilePath string
	Content  string
}

type Server struct {
	cfg          *config.Config
	store        *storage.Store
	dockerClient *docker.Client
	templates    *template.Template
}

func NewServer(cfg *config.Config, store *storage.Store, dockerClient *docker.Client) (*Server, error) {
	tmpl, err := template.ParseFS(contentFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	return &Server{
		cfg:          cfg,
		store:        store,
		dockerClient: dockerClient,
		templates:    tmpl,
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets
	staticSub, err := fs.Sub(contentFS, "static")
	if err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}

	// Health check
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Web UI Dashboard
	mux.HandleFunc("GET /{$}", s.handleDashboard)

	// HTMX Partial Endpoints
	mux.HandleFunc("GET /partials/status-banner", s.handleStatusBannerPartial)
	mux.HandleFunc("GET /partials/yaml-preview", s.handleYamlPreviewPartial)

	// Port API
	mux.HandleFunc("GET /api/ports", s.handleListPortsJSON)
	mux.HandleFunc("POST /api/ports", s.handleAddPort)
	mux.HandleFunc("PUT /api/ports/{id}", s.handleUpdatePort)
	mux.HandleFunc("POST /api/ports/{id}", s.handleUpdatePort)
	mux.HandleFunc("POST /api/ports/{id}/toggle", s.handleTogglePort)
	mux.HandleFunc("DELETE /api/ports/{id}", s.handleDeletePort)

	// Apply / Recreate Traefik & Gerbil
	mux.HandleFunc("POST /api/apply", s.handleApply)

	return mux
}

func (s *Server) getPageData() PageData {
	ports := s.store.List()
	hasPending := docker.HasPendingChanges(ports, s.cfg)
	isApplying := s.dockerClient.IsApplying()
	lastRes := s.dockerClient.GetLastResult()
	var lastAppliedAt *time.Time
	if lastRes != nil {
		lastAppliedAt = &lastRes.AppliedAt
	}

	return PageData{
		Ports:             ports,
		HasPendingChanges: hasPending,
		IsApplying:        isApplying,
		LastAppliedAt:     lastAppliedAt,
		LastApplyResult:   lastRes,
		Config:            s.cfg,
	}
}

// renderHTMXResponse renders the port table and simultaneously updates the status banner
// and count via HTMX Out-of-Band (OOB) swaps without requiring a page reload.
func (s *Server) renderHTMXResponse(w http.ResponseWriter, data PageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 1. Out-of-band swap for status banner
	fmt.Fprint(w, `<div id="status-banner" hx-swap-oob="true">`)
	_ = s.templates.ExecuteTemplate(w, "status_banner.html", data)
	fmt.Fprint(w, `</div>`)

	// 2. Out-of-band swap for ports count
	fmt.Fprintf(w, `<span id="ports-count" hx-swap-oob="true" class="badge badge-neutral">%d</span>`, len(data.Ports))

	// 3. Main swap for ports table
	_ = s.templates.ExecuteTemplate(w, "port_list.html", data)
}

// renderFormError responds with an inline error fragment for HTMX forms or plain text for REST callers.
func (s *Server) renderFormError(w http.ResponseWriter, r *http.Request, targetSelector string, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Retarget", targetSelector)
		w.Header().Set("HX-Reswap", "innerHTML")
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprintf(w, `<div class="form-error-box" role="alert">%s</div>`, html.EscapeString(msg))
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(msg))
}

func parsePortRuleFromRequest(r *http.Request) (models.PortRule, error) {
	var rule models.PortRule

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			return rule, fmt.Errorf("invalid JSON payload")
		}
		return rule, nil
	}

	if err := r.ParseForm(); err != nil {
		return rule, fmt.Errorf("invalid form data")
	}
	portNum, err := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	if err != nil {
		return rule, fmt.Errorf("port must be a valid number between 1 and 65535")
	}

	enabledVal := r.FormValue("enabled")
	rule = models.PortRule{
		Name:               r.FormValue("name"),
		Port:               portNum,
		Protocol:           models.PortProtocol(r.FormValue("protocol")),
		Enabled:            enabledVal == "true" || enabledVal == "on" || enabledVal == "1",
		PangolinResourceID: strings.TrimSpace(r.FormValue("pangolin_resource_id")),
	}
	return rule, nil
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.getPageData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleStatusBannerPartial(w http.ResponseWriter, r *http.Request) {
	data := s.getPageData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "status_banner.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleYamlPreviewPartial(w http.ResponseWriter, r *http.Request) {
	ports := s.store.List()
	yaml := docker.GenerateComposeYAML(ports, s.cfg)
	data := YamlPreviewData{
		FilePath: s.cfg.ComposeOutPath,
		Content:  yaml,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "yaml_preview.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleListPortsJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.store.List())
}

func (s *Server) handleAddPort(w http.ResponseWriter, r *http.Request) {
	rule, err := parsePortRuleFromRequest(r)
	if err != nil {
		log.Printf("[HTTP] AddPort validation error: %v", err)
		s.renderFormError(w, r, "#form-error", err.Error())
		return
	}

	created, err := s.store.Add(rule)
	if err != nil {
		log.Printf("[HTTP] AddPort rejected (%d/%s %q): %v", rule.Port, rule.Protocol, rule.Name, err)
		s.renderFormError(w, r, "#form-error", err.Error())
		return
	}
	log.Printf("[HTTP] Created port rule %s: %d/%s (%s) enabled=%v", created.ID, created.Port, created.Protocol, created.Name, created.Enabled)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "portSaved")
		data := s.getPageData()
		s.renderHTMXResponse(w, data)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

func (s *Server) handleUpdatePort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.renderFormError(w, r, "#edit-form-error", "missing rule id")
		return
	}

	rule, err := parsePortRuleFromRequest(r)
	if err != nil {
		log.Printf("[HTTP] UpdatePort validation error (id=%s): %v", id, err)
		s.renderFormError(w, r, "#edit-form-error", err.Error())
		return
	}
	rule.ID = id

	updated, err := s.store.Update(rule)
	if err != nil {
		log.Printf("[HTTP] UpdatePort rejected (id=%s %d/%s): %v", id, rule.Port, rule.Protocol, err)
		s.renderFormError(w, r, "#edit-form-error", err.Error())
		return
	}
	log.Printf("[HTTP] Updated port rule %s: %d/%s (%s) enabled=%v", updated.ID, updated.Port, updated.Protocol, updated.Name, updated.Enabled)

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Trigger", "portSaved")
		data := s.getPageData()
		s.renderHTMXResponse(w, data)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(updated)
}

func (s *Server) handleTogglePort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing rule id", http.StatusBadRequest)
		return
	}

	toggled, err := s.store.Toggle(id)
	if err != nil {
		log.Printf("[HTTP] TogglePort error (id=%s): %v", id, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[HTTP] Toggled port rule %s (%d/%s %s) -> enabled=%v", toggled.ID, toggled.Port, toggled.Protocol, toggled.Name, toggled.Enabled)

	if r.Header.Get("HX-Request") == "true" {
		data := s.getPageData()
		s.renderHTMXResponse(w, data)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeletePort(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing rule id", http.StatusBadRequest)
		return
	}

	if err := s.store.Delete(id); err != nil {
		log.Printf("[HTTP] DeletePort error (id=%s): %v", id, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[HTTP] Deleted port rule %s", id)

	if r.Header.Get("HX-Request") == "true" {
		data := s.getPageData()
		s.renderHTMXResponse(w, data)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	ports := s.store.List()
	log.Printf("[HTTP] POST /api/apply triggered (hx=%s, sync=%s, total_rules=%d)", r.Header.Get("HX-Request"), r.URL.Query().Get("sync"), len(ports))

	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("sync") != "1" {
		_ = s.dockerClient.StartApplyAsync(ports, 250*time.Millisecond)
		data := s.getPageData()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Trigger", "applyStarted")
		_ = s.templates.ExecuteTemplate(w, "status_banner.html", data)
		return
	}

	ctx := r.Context()
	_, _ = s.dockerClient.Apply(ctx, ports)

	if r.Header.Get("HX-Request") == "true" {
		data := s.getPageData()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.templates.ExecuteTemplate(w, "status_banner.html", data)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.dockerClient.GetLastResult())
}
