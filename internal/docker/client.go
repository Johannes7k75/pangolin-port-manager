package docker

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pangolin-provision/internal/config"
	"pangolin-provision/internal/models"
)

type ApplyResult struct {
	Success   bool      `json:"success"`
	Output    string    `json:"output"`
	AppliedAt time.Time `json:"applied_at"`
	Error     string    `json:"error,omitempty"`
}

type Client struct {
	mu         sync.RWMutex
	cfg        *config.Config
	lastResult *ApplyResult
	applying   bool
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
	}
}

func (c *Client) GetLastResult() *ApplyResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastResult
}

func (c *Client) IsApplying() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.applying
}

// sanitizeBaseComposeFile removes any top-level `include:` referencing the generated ports file,
// because Docker Compose v2 rejects `include:` when both files define the same service names
// ("services.<name> conflicts with imported resource"). Multi-file `-f` merges them cleanly.
func sanitizeBaseComposeFile(absPath, portsBaseName string) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	content := string(data)
	if !strings.Contains(content, "include:") || !strings.Contains(content, portsBaseName) {
		return
	}

	log.Printf("[Docker] Sanitizing legacy top-level 'include:' block from %s", absPath)
	lines := strings.Split(content, "\n")
	var out []string
	inInclude := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "include:" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inInclude = true
			continue
		}
		if inInclude {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(trimmed, "-") {
				continue
			}
			inInclude = false
		}
		out = append(out, line)
	}

	_ = os.WriteFile(absPath, []byte(strings.Join(out, "\n")), 0644)
}

// resolveHostProjectDir discovers the real host path mounted at c.cfg.ComposeProjectDir (e.g. /compose)
// and creates an in-container symlink so relative volume mounts (like ./config:/var/config) passed to
// the host Docker daemon (/var/run/docker.sock) resolve to the real host directory instead of /compose/config.
func (c *Client) resolveHostProjectDir(ctx context.Context, dockerBin string) string {
	projectDir := c.cfg.ComposeProjectDir
	if projectDir == "" {
		return "."
	}
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return projectDir
	}

	// Query Docker daemon for the host source path mounted at absProjectDir on this container
	var candidates []string
	if hn, err := os.Hostname(); err == nil && hn != "" {
		candidates = append(candidates, hn)
	}
	candidates = append(candidates, "pangolin-provision")

	tmpl := fmt.Sprintf(`{{range .Mounts}}{{if eq .Destination %q}}{{.Source}}{{end}}{{end}}`, absProjectDir)
	var hostSource string
	for _, id := range candidates {
		out, err := exec.CommandContext(ctx, dockerBin, "inspect", "--format", tmpl, id).Output()
		if err == nil {
			s := strings.TrimSpace(string(out))
			if s != "" {
				hostSource = s
				break
			}
		}
	}

	// Fallback: inspect `pangolin` container's compose working_dir label
	if hostSource == "" {
		out, err := exec.CommandContext(ctx, dockerBin, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.project.working_dir"}}`, "pangolin").Output()
		if err == nil {
			s := strings.TrimSpace(string(out))
			if s != "" && s != absProjectDir {
				hostSource = s
			}
		}
	}

	if hostSource == "" || hostSource == absProjectDir {
		return absProjectDir
	}

	// Create symlink inside our container from hostSource -> absProjectDir so docker compose
	// can read the files locally while sending real host paths to /var/run/docker.sock
	if _, err := os.Lstat(hostSource); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(hostSource), 0755); err == nil {
			if err := os.Symlink(absProjectDir, hostSource); err == nil {
				log.Printf("[Docker] Linked host project path %s -> %s", hostSource, absProjectDir)
			} else {
				log.Printf("[Docker] Warning: failed to symlink %s -> %s: %v", hostSource, absProjectDir, err)
				return absProjectDir
			}
		}
	}

	return hostSource
}

// resolveComposeFiles finds the base compose file and appends the generated override file
// so `docker compose -f <base> -f <ports>` merges services without `include:` conflicts.
func (c *Client) resolveComposeFiles(effectiveProjectDir string) []string {
	var found []string
	portsBaseName := filepath.Base(c.cfg.ComposeOutPath)

	findBase := func(filePath string) bool {
		if filePath == portsBaseName || filePath == c.cfg.ComposeOutPath {
			return false
		}
		baseName := filepath.Base(filePath)
		absPath := filepath.Join(effectiveProjectDir, baseName)
		if _, err := os.Stat(absPath); err == nil {
			sanitizeBaseComposeFile(absPath, portsBaseName)
			found = append(found, absPath)
			return true
		}
		return false
	}

	for _, f := range c.cfg.ComposeFiles {
		if findBase(f) {
			break
		}
	}

	if len(found) == 0 {
		candidates := []string{
			"compose.example.yml",
			"docker-compose.yml",
			"compose.yml",
			"compose.yaml",
			"docker-compose.yaml",
		}
		for _, cand := range candidates {
			if findBase(cand) {
				break
			}
		}
	}

	portsInDir := filepath.Join(effectiveProjectDir, portsBaseName)
	if _, err := os.Stat(portsInDir); err == nil {
		found = append(found, portsInDir)
	} else if _, err := os.Stat(c.cfg.ComposeOutPath); err == nil {
		found = append(found, c.cfg.ComposeOutPath)
	}

	return found
}

func (c *Client) executeComposeRecreate(ctx context.Context) (*ApplyResult, error) {
	start := time.Now()
	dockerBin := c.cfg.DockerBin
	if dockerBin == "" {
		dockerBin = "docker"
	}

	effectiveDir := c.resolveHostProjectDir(ctx, dockerBin)
	resolvedFiles := c.resolveComposeFiles(effectiveDir)
	if len(resolvedFiles) == 0 {
		err := fmt.Errorf("no compose file found in project dir %q", effectiveDir)
		log.Printf("[Docker] Apply error: %v", err)
		res := &ApplyResult{
			Success:   false,
			Output:    "",
			AppliedAt: time.Now().UTC(),
			Error:     err.Error(),
		}
		return res, err
	}

	baseArgs := []string{"compose", "--project-directory", effectiveDir}
	for _, f := range resolvedFiles {
		baseArgs = append(baseArgs, "-f", f)
	}

	// Step 1: Recreate gerbil and traefik
	upArgs := append([]string{}, baseArgs...)
	upArgs = append(upArgs, "up", "-d", "--no-deps", "--force-recreate", c.cfg.GerbilServiceName, c.cfg.TraefikServiceName)

	log.Printf("[Docker] Executing: %s %s", dockerBin, strings.Join(upArgs, " "))
	cmd := exec.CommandContext(ctx, dockerBin, upArgs...)
	cmd.Dir = effectiveDir

	output, err := cmd.CombinedOutput()
	appliedAt := time.Now().UTC()

	if err != nil {
		log.Printf("[Docker] Recreate failed after %s: %v\nOutput:\n%s", time.Since(start).Round(time.Millisecond), err, string(output))
		res := &ApplyResult{
			Success:   false,
			Output:    string(output),
			AppliedAt: appliedAt,
			Error:     fmt.Sprintf("docker compose error: %v (output: %s)", err, strings.TrimSpace(string(output))),
		}
		return res, err
	}

	// Step 2: Ensure traefik is running and attached to the new gerbil network namespace
	ensureArgs := append([]string{}, baseArgs...)
	ensureArgs = append(ensureArgs, "up", "-d", "--no-deps", c.cfg.TraefikServiceName)
	log.Printf("[Docker] Ensuring Traefik is up: %s %s", dockerBin, strings.Join(ensureArgs, " "))
	ensureCmd := exec.CommandContext(ctx, dockerBin, ensureArgs...)
	ensureCmd.Dir = effectiveDir
	ensureOut, ensureErr := ensureCmd.CombinedOutput()
	if ensureErr != nil {
		log.Printf("[Docker] Warning ensuring Traefik up: %v (output: %s)", ensureErr, string(ensureOut))
	}

	combinedOut := strings.TrimSpace(string(output) + "\n" + string(ensureOut))
	log.Printf("[Docker] Apply succeeded in %s:\n%s", time.Since(start).Round(time.Millisecond), combinedOut)

	res := &ApplyResult{
		Success:   true,
		Output:    combinedOut,
		AppliedAt: appliedAt,
	}
	return res, nil
}

// Apply writes the compose ports file and synchronously executes docker compose recreate
func (c *Client) Apply(ctx context.Context, rules []models.PortRule) (*ApplyResult, error) {
	c.mu.Lock()
	log.Printf("[Docker] Writing compose override file to %s (%d total rules)", c.cfg.ComposeOutPath, len(rules))
	if err := WriteComposeFile(rules, c.cfg); err != nil {
		log.Printf("[Docker] Failed to write compose file: %v", err)
		res := &ApplyResult{
			Success:   false,
			Output:    "",
			AppliedAt: time.Now().UTC(),
			Error:     fmt.Sprintf("failed to write compose file: %v", err),
		}
		c.lastResult = res
		c.mu.Unlock()
		return res, err
	}
	c.applying = true
	c.mu.Unlock()

	execCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := c.executeComposeRecreate(execCtx)

	c.mu.Lock()
	c.lastResult = res
	c.applying = false
	c.mu.Unlock()

	return res, err
}

// StartApplyAsync writes the compose file immediately and triggers the container recreation
// in a background goroutine after a brief delay so the HTTP response finishes flushing through
// Traefik/Gerbil before Gerbil's network stack restarts.
func (c *Client) StartApplyAsync(rules []models.PortRule, delay time.Duration) error {
	c.mu.Lock()
	if c.applying {
		log.Printf("[Docker] Apply already in progress; ignoring duplicate trigger")
		c.mu.Unlock()
		return nil
	}

	log.Printf("[Docker] Writing compose override file to %s (%d total rules)", c.cfg.ComposeOutPath, len(rules))
	if err := WriteComposeFile(rules, c.cfg); err != nil {
		log.Printf("[Docker] Failed to write compose file: %v", err)
		c.lastResult = &ApplyResult{
			Success:   false,
			Output:    "",
			AppliedAt: time.Now().UTC(),
			Error:     fmt.Sprintf("failed to write compose file: %v", err),
		}
		c.mu.Unlock()
		return err
	}

	c.applying = true
	c.mu.Unlock()

	go func() {
		log.Printf("[Docker] Starting async container recreate (flush delay=%s)...", delay)
		if delay > 0 {
			time.Sleep(delay)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		res, _ := c.executeComposeRecreate(ctx)

		c.mu.Lock()
		c.lastResult = res
		c.applying = false
		c.mu.Unlock()
	}()

	return nil
}
