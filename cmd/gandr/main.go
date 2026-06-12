// Command gandr is the Gandr client: a terminal UI that talks to a
// local gandrd over its Unix socket. It contains zero network code by
// construction — the daemon is the only process that touches the
// overlay.
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gandr-net/gandr/pkg/clientdb"
	"github.com/gandr-net/gandr/pkg/identity"
	"github.com/gandr-net/gandr/pkg/ipc"
	"github.com/gandr-net/gandr/pkg/tui"
)

// Version is stamped by the build.
var Version = "dev"

// BuildDate is stamped by the build.
var BuildDate = "unknown"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gandr:", err)
		os.Exit(1)
	}
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "gandr")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gandr"
	}
	return filepath.Join(home, ".local", "share", "gandr")
}

func run() error {
	socket := flag.String("socket", "/var/run/gandrd/gandr.sock", "path to gandrd socket")
	dataDir := flag.String("data", defaultDataDir(), "client data directory")
	name := flag.String("name", "", "display name for first-run identity generation")
	noMouse := flag.Bool("no-mouse", false, "disable mouse support (keyboard always works)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gandr %s (built %s)\n", Version, BuildDate)
		return nil
	}

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	fmt.Fprint(os.Stderr, "\n"+tui.SplashArt(60)+"\n\n")
	passphrase, err := readPassphrase()
	if err != nil {
		return err
	}

	keyfile := filepath.Join(*dataDir, "identity.key")
	id, created, err := identity.LoadOrGenerate(keyfile, passphrase, *name)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(os.Stderr, "\ngandr: new identity generated — you are %s...\n",
			hex.EncodeToString(id.PublicKey)[:8])
	} else {
		fmt.Fprintf(os.Stderr, "\ngandr: identity loaded — you are %s...\n",
			hex.EncodeToString(id.PublicKey)[:8])
	}

	db, err := clientdb.Open(filepath.Join(*dataDir, "client.db"), id.PrivateKey)
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintf(os.Stderr, "\nconnecting to gandrd at %s\n", *socket)
	cli, err := ipc.Dial(*socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, "\ngandr: gandrd not running")
		fmt.Fprintln(os.Stderr, "start with: sudo systemctl start gandrd")
		fmt.Fprintln(os.Stderr, "or:         gandrd --config /etc/gandrd/config.toml")
		fmt.Fprintln(os.Stderr, "\nnon-default socket: gandr --socket /path/to/gandr.sock")
		os.Exit(1)
	}
	defer cli.Close()

	tui.Version = Version
	model, err := tui.New(cli, db, id, *socket)
	if err != nil {
		return err
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if !*noMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(model, opts...)
	_, err = p.Run()
	return err
}

// readPassphrase reads the identity passphrase from GANDR_PASSPHRASE
// or prompts on stdin.
func readPassphrase() ([]byte, error) {
	if env := os.Getenv("GANDR_PASSPHRASE"); env != "" {
		return []byte(env), nil
	}
	fmt.Fprint(os.Stderr, "identity passphrase: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("reading passphrase: %w", err)
	}
	return []byte(strings.TrimRight(line, "\r\n")), nil
}
