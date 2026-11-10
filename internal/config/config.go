package config

import (
	"os"
	"strings"
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

func Load() *Config {
	composeFilesRaw := getEnvOrDefault("COMPOSE_FILES", "compose.example.yml,docker-compose.yml,compose.yml,docker-compose.ports.yml")
	var composeFiles []string
	for _, f := range strings.Split(composeFilesRaw, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			composeFiles = append(composeFiles, f)
		}
	}

	baseCmdsRaw := getEnvOrDefault("TRAEFIK_BASE_COMMANDS", "--configFile=/etc/traefik/traefik_config.yml,--api.insecure=true,--api.dashboard=true")
	var baseCmds []string
	for _, cmd := range strings.Split(baseCmdsRaw, ",") {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			baseCmds = append(baseCmds, cmd)
		}
	}

	return &Config{
		Port:                getEnvOrDefault("PORT", "8080"),
		DataDir:             getEnvOrDefault("DATA_DIR", "./data"),
		PortsFilePath:       getEnvOrDefault("PORTS_FILE_PATH", "./data/ports.json"),
		ComposeOutPath:      getEnvOrDefault("COMPOSE_OUT_PATH", "./docker-compose.ports.yml"),
		ComposeProjectDir:   getEnvOrDefault("COMPOSE_PROJECT_DIR", "."),
		ComposeFiles:        composeFiles,
		GerbilServiceName:   getEnvOrDefault("GERBIL_SERVICE_NAME", "gerbil"),
		TraefikServiceName:  getEnvOrDefault("TRAEFIK_SERVICE_NAME", "traefik"),
		TraefikBaseCommands: baseCmds,
		DockerBin:           getEnvOrDefault("DOCKER_BIN", "docker"),
	}
}
