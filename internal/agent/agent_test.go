package agent

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestServeListsTheAdministrationKeyAndCleansUp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.sock")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := Serve(path, private)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("unexpected socket mode %o", info.Mode().Perm())
	}

	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := agent.NewClient(connection)
	keys, err := client.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("unexpected key count %d", len(keys))
	}
	if keys[0].Type() != "ssh-ed25519" {
		t.Fatalf("unexpected key type %q", keys[0].Type())
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket still exists after cleanup: %v", err)
	}
}

func TestRotateReplacesTheAdministrationKey(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.sock")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, err := Start(path, private)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	_, replacement, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Rotate(replacement); err != nil {
		t.Fatal(err)
	}

	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	keys, err := agent.NewClient(connection).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("unexpected key count %d", len(keys))
	}
	expected, err := ssh.NewPublicKey(replacement.Public())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keys[0].Marshal(), expected.Marshal()) {
		t.Fatal("rotated key was not served")
	}
}

func TestServeReplacesAStaleSocket(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "agent.sock")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := Serve(path, private)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
}
