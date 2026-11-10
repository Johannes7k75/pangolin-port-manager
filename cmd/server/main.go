package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pangolin-provision/internal/config"
	"pangolin-provision/internal/docker"
	"pangolin-provision/internal/storage"
	"pangolin-provision/internal/web"
)

func main() {
	log.Println("[Pangolin Provisioner] Starting raw TCP/UDP port manager...")

	cfg := config.Load()
	log.Printf("[Pangolin Provisioner] Config: Storage=%s, ComposeOut=%s, GerbilService=%s, TraefikService=%s",
		cfg.PortsFilePath, cfg.ComposeOutPath, cfg.GerbilServiceName, cfg.TraefikServiceName)

	store, err := storage.NewStore(cfg.PortsFilePath)
	if err != nil {
		log.Fatalf("[Pangolin Provisioner] Failed to initialize storage: %v", err)
	}

	dockerClient := docker.NewClient(cfg)

	// Ensure the compose file exists on disk if store already has rules
	rules := store.List()
	if _, err := os.Stat(cfg.ComposeOutPath); os.IsNotExist(err) && len(rules) > 0 {
		log.Printf("[Pangolin Provisioner] Generating initial compose override file at %s", cfg.ComposeOutPath)
		_ = docker.WriteComposeFile(rules, cfg)
	}

	server, err := web.NewServer(cfg, store, dockerClient)
	if err != nil {
		log.Fatalf("[Pangolin Provisioner] Failed to initialize web server: %v", err)
	}

	httpServer := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      server.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second, // Allow compose up commands to complete
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("[Pangolin Provisioner] Listening on http://0.0.0.0:%s", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[Pangolin Provisioner] HTTP server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[Pangolin Provisioner] Shutting down server gracefully...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Pangolin Provisioner] Server forced to shutdown: %v", err)
	}

	log.Println("[Pangolin Provisioner] Server stopped.")
}
