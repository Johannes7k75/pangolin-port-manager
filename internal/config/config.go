package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port                string
	DataDir             string
	PortsFilePath       string
	ComposeOutPath      string
	ComposeProjectDir   string
	ComposeFiles        []string
	GerbilServiceName   string
	TraefikServiceName  string
	TraefikBaseCommands []string
	DockerBin           string
}

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

var defaultTraefikBaseCommands = []string{
	"--configFile=/etc/traefik/traefik_config.yml",
	"--api.insecure=true",
	"--api.dashboard=true",
}

// FindBaseComposeFile searches for the main compose file within projectDir or according to composeFiles,
// deliberately ignoring the generated composeOutPath override file.
func FindBaseComposeFile(projectDir string, composeFiles []string, composeOutPath string) string {
	if projectDir == "" {
		projectDir = "."
	}
	outBase := filepath.Base(composeOutPath)

	isValidBase := func(p string) bool {
		if p == "" || p == composeOutPath || filepath.Base(p) == outBase {
			return false
		}
		stat, err := os.Stat(p)
		return err == nil && !stat.IsDir()
	}

	for _, f := range composeFiles {
		f = strings.TrimSpace(f)
		if f == "" || f == composeOutPath || filepath.Base(f) == outBase {
			continue
		}
		if filepath.IsAbs(f) && isValidBase(f) {
			return f
		}
		joined := filepath.Join(projectDir, f)
		if isValidBase(joined) {
			return joined
		}
		baseJoined := filepath.Join(projectDir, filepath.Base(f))
		if isValidBase(baseJoined) {
			return baseJoined
		}
		if isValidBase(f) {
			return f
		}
	}

	candidates := []string{
		"compose.yml",
		"compose.yaml",
		"docker-compose.yml",
		"docker-compose.yaml",
		"compose.example.yml",
	}
	for _, cand := range candidates {
		joined := filepath.Join(projectDir, cand)
		if isValidBase(joined) {
			return joined
		}
		if isValidBase(cand) {
			return cand
		}
	}

	return ""
}

// LoadTraefikCommandFromComposeFile parses a compose file and extracts the command defined for serviceName.
func LoadTraefikCommandFromComposeFile(filePath string, serviceName string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file %q: %w", filePath, err)
	}

	var comp struct {
		Services map[string]struct {
			Command any `yaml:"command"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(data, &comp); err != nil {
		return nil, fmt.Errorf("failed to parse compose file %q: %w", filePath, err)
	}

	svc, exists := comp.Services[serviceName]
	if !exists {
		return nil, fmt.Errorf("service %q not found in %s", serviceName, filePath)
	}

	if svc.Command == nil {
		return []string{}, nil
	}

	switch v := svc.Command.(type) {
	case []any:
		var cmds []string
		for _, item := range v {
			if item != nil {
				cmds = append(cmds, fmt.Sprint(item))
			}
		}
		return cmds, nil
	case []string:
		return v, nil
	case string:
		return parseCommandString(v), nil
	default:
		return []string{fmt.Sprint(v)}, nil
	}
}

// parseCommandString breaks a command line string into separate arguments, respecting quotes.
func parseCommandString(cmdStr string) []string {
	var args []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	for _, r := range cmdStr {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// ReloadTraefikBaseCommands attempts to re-read the Traefik base command from the base compose file.
// If found and successfully parsed, it updates cfg.TraefikBaseCommands and returns them.
func (cfg *Config) ReloadTraefikBaseCommands() []string {
	baseFile := FindBaseComposeFile(cfg.ComposeProjectDir, cfg.ComposeFiles, cfg.ComposeOutPath)
	if baseFile != "" {
		cmds, err := LoadTraefikCommandFromComposeFile(baseFile, cfg.TraefikServiceName)
		if err == nil {
			cfg.TraefikBaseCommands = cmds
			return cmds
		}
		log.Printf("[Config] Warning: could not load Traefik commands from base compose file %s: %v", baseFile, err)
	}
	return cfg.TraefikBaseCommands
}

func Load() *Config {
	composeFilesRaw := getEnvOrDefault("COMPOSE_FILES", "compose.example.yml,docker-compose.yml,compose.yml,docker-compose.ports.yml")
	var composeFiles []string
	for _, f := range strings.Split(composeFilesRaw, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			composeFiles = append(composeFiles, f)
		}
	}

	projectDir := getEnvOrDefault("COMPOSE_PROJECT_DIR", ".")
	outPath := getEnvOrDefault("COMPOSE_OUT_PATH", "./docker-compose.ports.yml")
	traefikService := getEnvOrDefault("TRAEFIK_SERVICE_NAME", "traefik")

	cfg := &Config{
		Port:                getEnvOrDefault("PORT", "8080"),
		DataDir:             getEnvOrDefault("DATA_DIR", "./data"),
		PortsFilePath:       getEnvOrDefault("PORTS_FILE_PATH", "./data/ports.json"),
		ComposeOutPath:      outPath,
		ComposeProjectDir:   projectDir,
		ComposeFiles:        composeFiles,
		GerbilServiceName:   getEnvOrDefault("GERBIL_SERVICE_NAME", "gerbil"),
		TraefikServiceName:  traefikService,
		TraefikBaseCommands: defaultTraefikBaseCommands,
		DockerBin:           getEnvOrDefault("DOCKER_BIN", "docker"),
	}

	// Read base Traefik command from the base compose file
	baseFile := FindBaseComposeFile(cfg.ComposeProjectDir, cfg.ComposeFiles, cfg.ComposeOutPath)
	if baseFile != "" {
		cmds, err := LoadTraefikCommandFromComposeFile(baseFile, cfg.TraefikServiceName)
		if err != nil {
			log.Printf("[Config] Warning: could not read Traefik command from %s: %v (using fallback defaults)", baseFile, err)
		} else {
			log.Printf("[Config] Successfully loaded Traefik base command from %s (%d args)", baseFile, len(cmds))
			cfg.TraefikBaseCommands = cmds
		}
	} else {
		log.Printf("[Config] Notice: base compose file not found in project dir %s; using fallback Traefik command", cfg.ComposeProjectDir)
	}

	return cfg
}
