package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// exampleConfigPath locates scripts/config.example.toml from the
// package directory.
const exampleConfigPath = "../../scripts/config.example.toml"

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := LoadConfig(exampleConfigPath)
	if err != nil {
		t.Fatalf("example config rejected: %v", err)
	}
	if cfg.Identity.Keyfile == "" || cfg.IPC.Socket == "" {
		t.Fatal("example config missing essentials")
	}
	if cfg.Limits.MaxMessageAge != 604800 {
		t.Fatalf("max_message_age = %d", cfg.Limits.MaxMessageAge)
	}
	if _, err := cfg.DefaultTrust(); err != nil {
		t.Fatal(err)
	}
}

// TestInstallerConfigTransform applies the exact sed transformation
// scripts/install.sh performs and verifies the result still loads,
// with the keyfile relocated into the unit's writable path.
func TestInstallerConfigTransform(t *testing.T) {
	if _, err := exec.LookPath("sed"); err != nil {
		t.Skip("sed not available")
	}
	out, err := exec.Command("sed",
		"-e", `s|^keyfile = .*|keyfile = "/var/lib/gandrd/identity.key"|`,
		"-e", `s|^# passphrase_file = .*|passphrase_file = "/etc/gandrd/passphrase"|`,
		exampleConfigPath).Output()
	if err != nil {
		t.Fatalf("sed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("installer-transformed config rejected: %v", err)
	}
	if cfg.Identity.Keyfile != "/var/lib/gandrd/identity.key" {
		t.Fatalf("keyfile = %q", cfg.Identity.Keyfile)
	}
	if cfg.Identity.PassphraseFile != "/etc/gandrd/passphrase" {
		t.Fatalf("passphrase_file = %q", cfg.Identity.PassphraseFile)
	}
}
