// Command gandrd is the Gandr node daemon: an embedded Yggdrasil node,
// the federation engine, content-addressed storage, and the Unix socket
// the gandr client talks to. It has no admin interface, no management
// API, and no web UI — a config file and a socket, nothing else.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/network"
	"github.com/gandr-net/gandr/pkg/store"
)

// Version is stamped by the build; see Makefile.
var Version = "dev"

// BuildDate is stamped by the build.
var BuildDate = "unknown"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gandrd:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "/etc/gandrd/config.toml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gandrd %s (built %s)\n", Version, BuildDate)
		return nil
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}

	passphrase, err := readPassphrase(cfg)
	if err != nil {
		return err
	}

	id, created, err := identity.LoadOrGenerate(cfg.Identity.Keyfile, passphrase, cfg.Identity.DisplayName)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintln(os.Stderr, "gandrd: generated new node identity")
	}

	// The Yggdrasil transport key is separate from the node identity.
	yggID, _, err := identity.LoadOrGenerate(cfg.YggKeyfile(), passphrase, "")
	if err != nil {
		return err
	}

	objects, err := store.Open(cfg.Storage.Path)
	if err != nil {
		return err
	}

	transport, err := network.NewEmbedded(network.EmbeddedConfig{
		PrivateKey: yggID.PrivateKey,
		Listen:     cfg.Network.Listen,
		Peers:      cfg.Network.Peers,
	})
	if err != nil {
		return err
	}
	// The node key is this node's public overlay address: what other
	// operators put in [peering] seeds to federate with it.
	fmt.Fprintf(os.Stderr, "gandrd: yggdrasil node key: %x\n", transport.LocalAddr().YggKey)

	if err := os.MkdirAll(filepath.Dir(cfg.IPC.Socket), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	daemon, err := NewDaemon(cfg, id, transport, objects)
	if err != nil {
		return err
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		daemon.Stop()
	}()

	return daemon.Run()
}

// readPassphrase resolves the keyfile passphrase: passphrase_file, then
// the GANDRD_PASSPHRASE environment variable, then an interactive
// prompt on stdin.
func readPassphrase(cfg Config) ([]byte, error) {
	if cfg.Identity.PassphraseFile != "" {
		data, err := os.ReadFile(cfg.Identity.PassphraseFile)
		if err != nil {
			return nil, fmt.Errorf("reading passphrase file: %w", err)
		}
		return []byte(strings.TrimRight(string(data), "\r\n")), nil
	}
	if env := os.Getenv("GANDRD_PASSPHRASE"); env != "" {
		return []byte(env), nil
	}
	fmt.Fprint(os.Stderr, "keyfile passphrase: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return nil, errors.New("no passphrase available: set identity.passphrase_file, GANDRD_PASSPHRASE, or run interactively")
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
