package docker

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/models"
)

func usesStaticFile(cfg *config.Config) bool {
	for _, arg := range cfg.TraefikBaseCommands {
		if strings.EqualFold(arg, "--configFile") || strings.HasPrefix(strings.ToLower(arg), "--configfile=") {
			return true
		}
	}
	return false
}

func staticSourcePath(cfg *config.Config) string {
	if cfg.TraefikStaticConfigPath != "" {
		return cfg.TraefikStaticConfigPath
	}
	return filepath.Join(cfg.ComposeProjectDir, "config", "traefik", "traefik_config.yml")
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(n.Content); i += 2 {
		if strings.EqualFold(n.Content[i].Value, key) {
			return n.Content[i+1]
		}
	}
	return nil
}

const managedEntryPointComment = "pangolin-port-manager: managed entry point"

// Ownership lives with the entry point so configuration and ownership are
// updated in one atomic file replacement. Removing the marker relinquishes ownership.
func planStaticConfig(rules []models.PortRule, cfg *config.Config) ([]byte, []byte, error) {
	source := staticSourcePath(cfg)
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, nil, fmt.Errorf("read Traefik static YAML %q (set TRAEFIK_STATIC_CONFIG_PATH for a custom location): %w", source, err)
	}
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("parse Traefik static YAML: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("Traefik static configuration must be a YAML mapping")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, fmt.Errorf("Traefik static YAML must contain exactly one document")
	}
	root := doc.Content[0]
	// Validate duplicate keys before editing the document.
	var check map[string]interface{}
	if err := doc.Decode(&check); err != nil {
		return nil, nil, fmt.Errorf("invalid Traefik static configuration: %w", err)
	}
	changed := false
	entryPoints := mapValue(root, "entryPoints")
	if entryPoints == nil {
		hasEnabled := false
		for _, rule := range rules {
			hasEnabled = hasEnabled || rule.Enabled
		}
		if !hasEnabled {
			return data, data, nil
		}
		changed = true
		entryPoints = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "entryPoints", Tag: "!!str"}, entryPoints)
	}
	if entryPoints.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("Traefik entryPoints must be a YAML mapping")
	}
	desired := make(map[string]bool)
	for _, rule := range rules {
		if rule.Enabled {
			desired[fmt.Sprintf("%s-%d", rule.Protocol, rule.Port)] = true
		}
	}
	for i := 0; i < len(entryPoints.Content); {
		key, value := entryPoints.Content[i], entryPoints.Content[i+1]
		if hasManagedMarker(key) {
			proto, portText, ok := strings.Cut(key.Value, "-")
			port, err := strconv.Atoi(portText)
			if !ok || err != nil || port < 1 || port > 65535 || (proto != "tcp" && proto != "udp") || value.Kind != yaml.MappingNode {
				return nil, nil, fmt.Errorf("invalid managed entry point %q; remove its ownership comment to manage it manually", key.Value)
			}
			address := mapValue(value, "address")
			if address == nil || !matchesAddress(address.Value, proto, port) {
				return nil, nil, fmt.Errorf("managed entry point %s was manually changed; restore its address or remove its ownership comment", key.Value)
			}
			if !desired[key.Value] {
				entryPoints.Content = append(entryPoints.Content[:i], entryPoints.Content[i+2:]...)
				changed = true
				continue
			}
		}
		i += 2
	}
	ordered := append([]models.PortRule(nil), rules...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Port == ordered[j].Port {
			return ordered[i].Protocol < ordered[j].Protocol
		}
		return ordered[i].Port < ordered[j].Port
	})
	for _, rule := range ordered {
		if !rule.Enabled {
			continue
		}
		name := fmt.Sprintf("%s-%d", rule.Protocol, rule.Port)
		address := fmt.Sprintf(":%d/%s", rule.Port, rule.Protocol)
		if existing := mapValue(entryPoints, name); existing != nil {
			if existing.Kind != yaml.MappingNode {
				return nil, nil, fmt.Errorf("existing Traefik entry point %s must be a mapping", name)
			}
			addr := mapValue(existing, "address")
			if addr == nil || !matchesAddress(addr.Value, string(rule.Protocol), rule.Port) {
				return nil, nil, fmt.Errorf("existing Traefik entry point %s conflicts with managed address %s", name, address)
			}
			continue
		}
		var entry yaml.Node
		_ = entry.Encode(map[string]string{"address": address})
		entryPoints.Content = append(entryPoints.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name, HeadComment: managedEntryPointComment}, &entry)
		changed = true
	}
	if !changed {
		return data, data, nil
	}
	result, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, nil, err
	}
	return data, result, nil
}

func hasManagedMarker(key *yaml.Node) bool {
	for _, line := range strings.Split(key.HeadComment, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#")) == managedEntryPointComment {
			return true
		}
	}
	return false
}

func matchesAddress(address, protocol string, port int) bool {
	return address == fmt.Sprintf(":%d/%s", port, protocol) || (protocol == "tcp" && address == fmt.Sprintf(":%d", port))
}

func writeStaticConfig(rules []models.PortRule, cfg *config.Config) error {
	original, updated, err := planStaticConfig(rules, cfg)
	if err != nil {
		return err
	}
	if bytes.Equal(original, updated) {
		return nil
	}
	path, err := filepath.EvalSymlinks(staticSourcePath(cfg))
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Keep the exact previous bytes for recovery, with private permissions.
	if err := writeAtomic(path+".port-manager.bak", original, 0600); err != nil {
		return fmt.Errorf("back up Traefik static YAML: %w", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, original) {
		return fmt.Errorf("Traefik static YAML changed during Apply; retry")
	}
	if err := writeAtomic(path, updated, info.Mode().Perm()); err != nil {
		return fmt.Errorf("update Traefik static YAML (mount its parent directory writable in Port Manager): %w", err)
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".port-manager-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
