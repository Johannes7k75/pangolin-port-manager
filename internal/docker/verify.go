package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"pangolin-port-manager/internal/models"
)

func checkEntryPoints(ctx context.Context, apiURL string, rules []models.PortRule) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/api/entrypoints", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Traefik entrypoints API returned HTTP %d", resp.StatusCode)
	}
	var entries []struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return err
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		name := fmt.Sprintf("%s-%d", rule.Protocol, rule.Port)
		address := fmt.Sprintf(":%d/%s", rule.Port, rule.Protocol)
		found := false
		for _, entry := range entries {
			// TCP addresses may be returned without the implicit /tcp suffix.
			if entry.Name == name && (entry.Address == address || (rule.Protocol == models.ProtocolTCP && entry.Address == fmt.Sprintf(":%d", rule.Port))) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Traefik runtime entry point %s at %s is missing", name, address)
		}
	}
	return nil
}

func (c *Client) verifyEntryPoints(ctx context.Context, rules []models.PortRule) error {
	if c.cfg.TraefikAPIURL == "" {
		return nil
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastErr error
	for {
		lastErr = checkEntryPoints(verifyCtx, c.cfg.TraefikAPIURL, rules)
		if lastErr == nil {
			return nil
		}
		select {
		case <-verifyCtx.Done():
			return fmt.Errorf("Traefik runtime verification failed: %w", lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
