package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"pangolin-port-manager/internal/config"
	"pangolin-port-manager/internal/models"
)

func fileConfig(t *testing.T, source string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "original.yml")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return &config.Config{ComposeProjectDir: dir, ComposeOutPath: filepath.Join(dir, "ports.yml"), TraefikStaticConfigPath: path,
		TraefikBaseCommands: []string{"--configFile=/etc/traefik/custom.yml", "--api.dashboard=true"}, GerbilServiceName: "gerbil", TraefikServiceName: "traefik"}
}

func TestFileConfiguredLifecycle(t *testing.T) {
	source := `# Original settings
entryPoints:
  web:
    address: ':80'
  websecure:
    address: ':443'
  tcp-465:
    address: ':465/tcp'
    proxyProtocol:
      trustedIPs: ['127.0.0.1/32']
providers:
  http:
    endpoint: http://pangolin:3001/api/v1/traefik-config
certificatesResolvers:
  letsencrypt:
    acme:
      email: admin@example.com
api:
  dashboard: true
`
	cfg := fileConfig(t, source)
	rules := []models.PortRule{
		{Port: 25, Protocol: models.ProtocolTCP, Enabled: true},
		{Port: 465, Protocol: models.ProtocolTCP, Enabled: true},
		{Port: 993, Protocol: models.ProtocolTCP, Enabled: true},
		{Port: 9987, Protocol: models.ProtocolUDP, Enabled: true},
		{Port: 7777, Protocol: models.ProtocolTCP, Enabled: false},
	}
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatal(err)
	}
	compose, _ := os.ReadFile(cfg.ComposeOutPath)
	if strings.Contains(string(compose), "--entryPoints") || strings.Contains(string(compose), "--api.dashboard") {
		t.Fatalf("mixed configuration sources:\n%s", compose)
	}
	if !strings.Contains(string(compose), "--configFile=/etc/traefik/custom.yml") || strings.Contains(string(compose), "volumes:") {
		t.Fatalf("original config command not preserved:\n%s", compose)
	}
	generated, err := os.ReadFile(cfg.TraefikStaticConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var original, actual map[string]interface{}
	if err := yaml.Unmarshal([]byte(source), &original); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(generated, &actual); err != nil {
		t.Fatal(err)
	}
	entries := actual["entryPoints"].(map[string]interface{})
	for _, name := range []string{"web", "websecure", "tcp-25", "tcp-465", "tcp-993", "udp-9987"} {
		if entries[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
	if entries["tcp-7777"] != nil {
		t.Error("disabled entry point emitted")
	}
	// Removing the generated entries must reproduce every original setting.
	delete(entries, "tcp-25")
	delete(entries, "tcp-993")
	delete(entries, "udp-9987")
	want, _ := yaml.Marshal(original)
	got, _ := yaml.Marshal(actual)
	if string(want) != string(got) {
		t.Fatalf("lost original settings:\n%s", got)
	}
	backup, _ := os.ReadFile(cfg.TraefikStaticConfigPath + ".port-manager.bak")
	if string(backup) != source {
		t.Fatal("backup did not preserve original bytes")
	}
	if !strings.Contains(string(generated), "# Original settings") {
		t.Fatal("original comment lost")
	}
	if !strings.Contains(string(generated), managedEntryPointComment) {
		t.Fatal("ownership marker missing")
	}
	first := string(generated)
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(cfg.TraefikStaticConfigPath)
	if string(second) != first {
		t.Fatal("repeated Apply rewrote configuration")
	}
	if HasPendingChanges(rules, cfg) {
		t.Fatal("freshly generated files should match")
	}
	if err := WriteComposeFile(nil, cfg); err != nil {
		t.Fatal(err)
	}
	generated, _ = os.ReadFile(cfg.TraefikStaticConfigPath)
	if strings.Contains(string(generated), "tcp-25:") || strings.Contains(string(generated), "udp-9987:") {
		t.Fatal("removed rules left stale entry points")
	}
	if !strings.Contains(string(generated), "tcp-465:") {
		t.Fatal("removed administrator-owned entry point")
	}
	if err := os.WriteFile(cfg.TraefikStaticConfigPath, []byte(source+"log:\n  level: DEBUG\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if HasPendingChanges(nil, cfg) {
		t.Fatal("unrelated manual edits should not need Apply")
	}
}

func TestInvalidStaticConfigDoesNotOverwriteOutput(t *testing.T) {
	for _, source := range []string{"[invalid", "", "entryPoints: {}\n---\nlog: {}", "entryPoints: []", "entryPoints: {}\nentryPoints: {}", "entryPoints:\n  tcp-25:\n    address: ':2525/tcp'"} {
		t.Run(source, func(t *testing.T) {
			cfg := fileConfig(t, source)
			if err := os.WriteFile(cfg.ComposeOutPath, []byte("original override"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := WriteComposeFile([]models.PortRule{{Port: 25, Protocol: models.ProtocolTCP, Enabled: true}}, cfg); err == nil {
				t.Fatal("expected configuration error")
			}
			got, _ := os.ReadFile(cfg.ComposeOutPath)
			if string(got) != "original override" {
				t.Fatal("overwrote override on failure")
			}
		})
	}
	cfg := fileConfig(t, "entryPoints: {}")
	cfg.TraefikStaticConfigPath = filepath.Join(t.TempDir(), "missing.yml")
	if err := WriteComposeFile(nil, cfg); err == nil {
		t.Fatal("missing source should fail")
	}
}

func TestDetectFileMode(t *testing.T) {
	for _, args := range [][]string{{"--configFile=/etc/traefik/config.yml"}, {"--configfile", "/etc/traefik/config.yml"}} {
		if !usesStaticFile(&config.Config{TraefikBaseCommands: args}) {
			t.Fatalf("not detected: %v", args)
		}
	}
	if usesStaticFile(&config.Config{TraefikBaseCommands: []string{"--api.dashboard=true"}}) {
		t.Fatal("CLI-only mode detected as file mode")
	}
}

func TestRuntimeEntryPointVerification(t *testing.T) {
	rules := []models.PortRule{{Port: 993, Protocol: models.ProtocolTCP, Enabled: true}, {Port: 9987, Protocol: models.ProtocolUDP, Enabled: true}, {Port: 25, Protocol: models.ProtocolTCP, Enabled: false}}
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"loaded", `[{"name":"tcp-993","address":":993/tcp"},{"name":"udp-9987","address":":9987/udp"}]`, 200, false},
		{"implicit TCP", `[{"name":"tcp-993","address":":993"},{"name":"udp-9987","address":":9987/udp"}]`, 200, false},
		{"missing", `[{"name":"web","address":":80"}]`, 200, true},
		{"wrong address", `[{"name":"tcp-993","address":":995"}]`, 200, true},
		{"bad response", `{}`, 200, true},
		{"unavailable", `unavailable`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/entrypoints" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			err := checkEntryPoints(context.Background(), server.URL, rules)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[]`)) }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	client := NewClient(&config.Config{TraefikAPIURL: server.URL})
	if err := client.verifyEntryPoints(ctx, rules); err == nil {
		t.Fatal("verification must fail after timeout")
	}
}

func TestApplyVerifiesRuntime(t *testing.T) {
	cfg := fileConfig(t, "entryPoints:\n  web:\n    address: ':80'\n")
	cfg.ComposeFiles = []string{"compose.yml"}
	if err := os.WriteFile(filepath.Join(cfg.ComposeProjectDir, "compose.yml"), []byte("services: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.DockerBin = filepath.Join(cfg.ComposeProjectDir, "docker-mock")
	if err := os.WriteFile(cfg.DockerBin, []byte("#!/bin/sh\nif [ \"$1\" = inspect ]; then exit 1; fi\necho Recreated\n"), 0700); err != nil {
		t.Fatal(err)
	}
	rules := []models.PortRule{{Port: 993, Protocol: models.ProtocolTCP, Enabled: true}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"tcp-993","address":":993/tcp"}]`))
	}))
	defer server.Close()
	cfg.TraefikAPIURL = server.URL
	client := NewClient(cfg)
	res, err := client.Apply(context.Background(), rules)
	if err != nil || !res.Success || !strings.Contains(res.Output, "runtime entry points verified") {
		t.Fatalf("apply failed: %+v, %v", res, err)
	}
	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"web","address":":80"}]`))
	}))
	defer missing.Close()
	cfg.TraefikAPIURL = missing.URL
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err = client.executeComposeRecreate(ctx, rules)
	if err == nil || res.Success || !strings.Contains(res.Error, "runtime verification failed") {
		t.Fatalf("missing listener reported success: %+v, %v", res, err)
	}
}

func TestManagedOwnershipAndManualEdits(t *testing.T) {
	cfg := fileConfig(t, "entryPoints:\n  web:\n    address: ':80'\n")
	rules := []models.PortRule{{Port: 25, Protocol: models.ProtocolTCP, Enabled: true}}
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg.TraefikStaticConfigPath)
	// Administrators may customize managed listeners without losing their settings.
	data = []byte(strings.Replace(string(data), "address: :25/tcp", "address: :25/tcp\n        proxyProtocol:\n            insecure: true", 1))
	if err := os.WriteFile(cfg.TraefikStaticConfigPath, data, 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg.TraefikStaticConfigPath)
	if string(data) != string(got) {
		t.Fatal("manual listener settings changed")
	}
	if !strings.Contains(string(got), "proxyProtocol:") {
		t.Fatal("test did not add listener settings")
	}
	additional := append(append([]models.PortRule(nil), rules...), models.PortRule{Port: 993, Protocol: models.ProtocolTCP, Enabled: true})
	if err := WriteComposeFile(additional, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg.TraefikStaticConfigPath)
	if !strings.Contains(string(got), "proxyProtocol:") {
		t.Fatal("lost manual listener settings when adding another port")
	}
	// Changing a managed address must not lead to silent deletion or replacement.
	modified := strings.Replace(string(got), ":25/tcp", ":2525/tcp", 1)
	if err := os.WriteFile(cfg.TraefikStaticConfigPath, []byte(modified), 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeFile(nil, cfg); err == nil {
		t.Fatal("manually changed managed address should fail")
	}
	got, _ = os.ReadFile(cfg.TraefikStaticConfigPath)
	if string(got) != modified {
		t.Fatal("changed file on conflict")
	}
	// Removing the marker explicitly transfers ownership to the administrator.
	manual := strings.Replace(modified, managedEntryPointComment, "manually maintained", 1)
	if err := os.WriteFile(cfg.TraefikStaticConfigPath, []byte(manual), 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeFile(nil, cfg); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg.TraefikStaticConfigPath)
	if !strings.Contains(string(got), "tcp-25:") || !strings.Contains(string(got), ":2525/tcp") || !strings.Contains(string(got), "proxyProtocol:") {
		t.Fatal("deleted or changed unowned listener")
	}
	if strings.Contains(string(got), "tcp-993:") {
		t.Fatal("did not remove remaining owned listener")
	}
}

func TestStaticConfigPreservesPermissionsAndSymlink(t *testing.T) {
	cfg := fileConfig(t, "entryPoints: {}\n")
	target := cfg.TraefikStaticConfigPath
	if err := os.Chmod(target, 0640); err != nil {
		t.Fatal(err)
	}
	cfg.TraefikStaticConfigPath = filepath.Join(cfg.ComposeProjectDir, "link.yml")
	if err := os.Symlink(target, cfg.TraefikStaticConfigPath); err != nil {
		t.Fatal(err)
	}
	if err := WriteComposeFile([]models.PortRule{{Port: 25, Protocol: models.ProtocolTCP, Enabled: true}}, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(cfg.TraefikStaticConfigPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("replaced configuration symlink")
	}
	info, err = os.Stat(target)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("lost file permissions")
	}
}

func TestMigrationFromSnapshot(t *testing.T) {
	cfg := fileConfig(t, "entryPoints:\n  web:\n    address: ':80'\n")
	legacy := []byte("services:\n  traefik:\n    command: ['--configFile=/etc/traefik/port-manager-static.yml']\n    volumes: ['./ports.yml.traefik.yml:/etc/traefik/port-manager-static.yml:ro']\n")
	if err := os.WriteFile(cfg.ComposeOutPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := cfg.ComposeOutPath + ".traefik.yml"
	if err := os.WriteFile(snapshot, []byte("entryPoints: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rules := []models.PortRule{{Port: 993, Protocol: models.ProtocolTCP, Enabled: true}}
	if !HasPendingChanges(rules, cfg) {
		t.Fatal("migration should need Apply")
	}
	if err := WriteComposeFile(rules, cfg); err != nil {
		t.Fatal(err)
	}
	override, _ := os.ReadFile(cfg.ComposeOutPath)
	if strings.Contains(string(override), "port-manager-static.yml") || strings.Contains(string(override), "volumes:") {
		t.Fatal("legacy mount retained")
	}
	source, _ := os.ReadFile(cfg.TraefikStaticConfigPath)
	if !strings.Contains(string(source), "tcp-993:") {
		t.Fatal("listener missing from original config")
	}
	if HasPendingChanges(rules, cfg) {
		t.Fatal("migration not in sync")
	}
}

func TestBackupFailureLeavesOriginalUntouched(t *testing.T) {
	source := "entryPoints: {}\n"
	cfg := fileConfig(t, source)
	if err := os.Mkdir(cfg.TraefikStaticConfigPath+".port-manager.bak", 0700); err != nil {
		t.Fatal(err)
	}
	err := WriteComposeFile([]models.PortRule{{Port: 25, Protocol: models.ProtocolTCP, Enabled: true}}, cfg)
	if err == nil {
		t.Fatal("expected backup error")
	}
	got, _ := os.ReadFile(cfg.TraefikStaticConfigPath)
	if string(got) != source {
		t.Fatal("changed source without backup")
	}
	if _, err := os.Stat(cfg.ComposeOutPath); !os.IsNotExist(err) {
		t.Fatal("wrote override after failed backup")
	}
}
