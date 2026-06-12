package main

import (
	"encoding/hex"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/gandr-net/gandr/pkg/proto"
)

// Config is gandrd's TOML configuration.
type Config struct {
	Identity     IdentityConfig     `toml:"identity"`
	Network      NetworkConfig      `toml:"network"`
	Peering      PeeringConfig      `toml:"peering"`
	Capabilities CapabilitiesConfig `toml:"capabilities"`
	Limits       LimitsConfig       `toml:"limits"`
	IPC          IPCConfig          `toml:"ipc"`
	Storage      StorageConfig      `toml:"storage"`
}

// IdentityConfig locates the node identity keyfile.
type IdentityConfig struct {
	Keyfile string `toml:"keyfile"`
	// PassphraseFile optionally holds the keyfile passphrase (one line),
	// for unattended starts under systemd. Alternatives: the
	// GANDRD_PASSPHRASE environment variable, or an interactive prompt.
	PassphraseFile string `toml:"passphrase_file"`
	// DisplayName is announced on first-run identity generation.
	DisplayName string `toml:"display_name"`
}

// NetworkConfig configures the embedded Yggdrasil node.
type NetworkConfig struct {
	// Listen are Yggdrasil link-layer listener URIs.
	Listen []string `toml:"listen"`
	// Peers are Yggdrasil link-layer peer URIs (public yggdrasil peers
	// or direct operator arrangements).
	Peers []string `toml:"peers"`
	// YggKeyfile stores the encrypted Yggdrasil transport key, distinct
	// from the identity key. Defaults to <storage.path>/yggdrasil.key.
	YggKeyfile string `toml:"ygg_keyfile"`
}

// PeeringConfig governs the federation layer.
type PeeringConfig struct {
	SeedNode      bool   `toml:"seed_node"`
	MaxPeers      int    `toml:"max_peers"`
	TrustNewPeers string `toml:"trust_new_peers"`
	// Seeds are Gandr seed nodes given as hex-encoded Yggdrasil node
	// keys; gandrd attempts federation with them at startup.
	Seeds []string `toml:"seeds"`
}

// CapabilitiesConfig is the announced capability set.
type CapabilitiesConfig struct {
	Chat    bool `toml:"chat"`
	Feed    bool `toml:"feed"`
	Forum   bool `toml:"forum"`
	Storage bool `toml:"storage"`
	Relay   bool `toml:"relay"`
	Seed    bool `toml:"seed"`
}

// Bitmask converts the capability flags to the wire bitmask.
func (c CapabilitiesConfig) Bitmask() uint32 {
	var m uint32
	if c.Chat {
		m |= proto.CapChat
	}
	if c.Feed {
		m |= proto.CapFeed
	}
	if c.Forum {
		m |= proto.CapForum
	}
	if c.Storage {
		m |= proto.CapStorage
	}
	if c.Relay {
		m |= proto.CapRelay
	}
	if c.Seed {
		m |= proto.CapSeed
	}
	return m
}

// LimitsConfig bounds resource usage.
type LimitsConfig struct {
	MaxPayloadSize uint32 `toml:"max_payload_size"`
	MaxMessageAge  uint32 `toml:"max_message_age"` // seconds
	RateLimitRPM   uint16 `toml:"rate_limit_rpm"`
	MaxConnections int    `toml:"max_connections"`
}

// IPCConfig locates the client socket.
type IPCConfig struct {
	Socket string `toml:"socket"`
}

// StorageConfig locates the object store.
type StorageConfig struct {
	Path string `toml:"path"`
}

// defaultConfig returns the documented defaults.
func defaultConfig() Config {
	return Config{
		Identity: IdentityConfig{
			Keyfile:     "/etc/gandrd/identity.key",
			DisplayName: "",
		},
		Network: NetworkConfig{
			Listen: []string{"tcp://0.0.0.0:4242"},
		},
		Peering: PeeringConfig{
			MaxPeers:      200,
			TrustNewPeers: "neutral",
		},
		Capabilities: CapabilitiesConfig{
			Chat: true, Feed: true, Forum: true, Relay: true,
		},
		Limits: LimitsConfig{
			MaxPayloadSize: 65535,
			MaxMessageAge:  604800, // 7 days
			RateLimitRPM:   600,
			MaxConnections: 500,
		},
		IPC: IPCConfig{
			Socket: "/var/run/gandrd/gandr.sock",
		},
		Storage: StorageConfig{
			Path: "/var/lib/gandrd",
		},
	}
}

// LoadConfig reads and validates a TOML config file. Missing keys keep
// their defaults.
func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validate enforces config invariants.
func (c *Config) Validate() error {
	if c.Identity.Keyfile == "" {
		return fmt.Errorf("config: identity.keyfile is required")
	}
	if c.Storage.Path == "" {
		return fmt.Errorf("config: storage.path is required")
	}
	if c.IPC.Socket == "" {
		return fmt.Errorf("config: ipc.socket is required")
	}
	if c.Peering.MaxPeers <= 0 {
		return fmt.Errorf("config: peering.max_peers must be positive")
	}
	if c.Limits.MaxPayloadSize > proto.MaxPayloadSize {
		return fmt.Errorf("config: limits.max_payload_size exceeds protocol maximum %d", proto.MaxPayloadSize)
	}
	if _, err := c.DefaultTrust(); err != nil {
		return err
	}
	if _, err := c.SeedKeys(); err != nil {
		return err
	}
	return nil
}

// DefaultTrust maps the configured trust name to its wire value.
func (c *Config) DefaultTrust() (uint8, error) {
	switch c.Peering.TrustNewPeers {
	case "untrusted":
		return proto.TrustUntrusted, nil
	case "", "neutral":
		return proto.TrustNeutral, nil
	case "trusted":
		return proto.TrustTrusted, nil
	case "vouched":
		return proto.TrustVouched, nil
	default:
		return 0, fmt.Errorf("config: unknown trust level %q", c.Peering.TrustNewPeers)
	}
}

// SeedKeys decodes the configured seed node keys.
func (c *Config) SeedKeys() ([][]byte, error) {
	out := make([][]byte, 0, len(c.Peering.Seeds))
	for _, s := range c.Peering.Seeds {
		k, err := hex.DecodeString(s)
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("config: seed %q is not a hex-encoded yggdrasil node key", s)
		}
		out = append(out, k)
	}
	return out, nil
}

// YggKeyfile resolves the transport keyfile path.
func (c *Config) YggKeyfile() string {
	if c.Network.YggKeyfile != "" {
		return c.Network.YggKeyfile
	}
	return c.Storage.Path + "/yggdrasil.key"
}
