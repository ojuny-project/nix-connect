package state

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ojuny-project/nix-connect/internal/trust"
)

func TestOpenInitializesState(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if store.Identity == nil || len(store.AdminKey) == 0 {
		t.Fatal("identity or administration key is missing")
	}
	if store.Config.InfoPort != 5060 || store.Config.ControlPort != 5061 {
		t.Fatalf("unexpected ports: %+v", store.Config)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity", "identity.key")); err != nil {
		t.Fatal(err)
	}
	authorized, err := store.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if authorized == "" {
		t.Fatal("empty authorized key")
	}
}

func TestRegistrationAndTrustPersist(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "abc")
	if err := store.SaveRegistration(); err != nil {
		t.Fatal(err)
	}
	store.Trust.Add(trust.Record{Fingerprint: "peer"})
	if err := store.SaveTrust(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Registration.Repository != "https://example.invalid/flake" {
		t.Fatalf("registration was not persisted: %+v", reopened.Registration)
	}
	if !reopened.Trust.Has("peer") {
		t.Fatal("trust record was not persisted")
	}
}

func TestConfigNormalize(t *testing.T) {
	config := Config{}
	config.Normalize()
	defaults := DefaultConfig()
	if config.InfoPort != defaults.InfoPort || config.ControlPort != defaults.ControlPort {
		t.Fatalf("ports were not defaulted: %+v", config)
	}
	if config.Discovery.Interval != defaults.Discovery.Interval || config.Discovery.Workers != defaults.Discovery.Workers {
		t.Fatalf("discovery was not defaulted: %+v", config.Discovery)
	}
	if config.MagicRollback.Mode != defaults.MagicRollback.Mode || config.MagicRollback.AutomaticTimeout != defaults.MagicRollback.AutomaticTimeout {
		t.Fatalf("magic rollback was not defaulted: %+v", config.MagicRollback)
	}
	config = Config{Account: "alice", Discovery: DiscoveryConfig{Workers: -1}}
	config.Normalize()
	if config.SSHUser != "alice" {
		t.Fatalf("SSH user did not follow the account: %q", config.SSHUser)
	}
	if config.Discovery.Workers != defaults.Discovery.Workers {
		t.Fatalf("negative worker count was not defaulted: %d", config.Discovery.Workers)
	}
}

func TestConfigPersists(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	store.Config.InfoPort = 16060
	store.Config.ControlPort = 16061
	store.Config.Kind = "live"
	store.Config.Discovery.Ranges = []string{"10.0.0.0/24"}
	if err := store.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Config.InfoPort != 16060 || reopened.Config.ControlPort != 16061 || reopened.Config.Kind != "live" {
		t.Fatalf("config was not persisted: %+v", reopened.Config)
	}
	if len(reopened.Config.Discovery.Ranges) != 1 || reopened.Config.Discovery.Ranges[0] != "10.0.0.0/24" {
		t.Fatalf("ranges were not persisted: %+v", reopened.Config.Discovery.Ranges)
	}
}

func TestAdministrationKeyPermissionsAndFormats(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(directory, AdminKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("unexpected key mode %o", info.Mode().Perm())
	}
	authorized, err := store.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authorized, "ssh-ed25519 ") {
		t.Fatalf("unexpected authorized key %q", authorized)
	}
	publicData, err := os.ReadFile(filepath.Join(directory, AdminPublicKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(publicData)) != authorized {
		t.Fatalf("public key file does not match the authorized key: %q", publicData)
	}
	publicInfo, err := os.Stat(filepath.Join(directory, AdminPublicKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if publicInfo.Mode().Perm() != 0o644 {
		t.Fatalf("unexpected public key mode %o", publicInfo.Mode().Perm())
	}
	pem, err := store.AdminKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pem), "BEGIN PRIVATE KEY") {
		t.Fatal("admin key PEM is malformed")
	}
	if _, err := store.AdminSigner(); err != nil {
		t.Fatal(err)
	}
}

func TestRotateAdministrationKey(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := store.RotateAdminKey()
	if err != nil {
		t.Fatal(err)
	}
	if len(rotated) != ed25519.PrivateKeySize {
		t.Fatalf("unexpected key length %d", len(rotated))
	}
	after, err := store.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("authorized key did not change")
	}
	publicData, err := os.ReadFile(filepath.Join(directory, AdminPublicKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(publicData)) != after {
		t.Fatalf("public key file was not rotated: %q", publicData)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reopened.AdminAuthorizedKey()
	if err != nil || persisted != after {
		t.Fatalf("rotated key was not persisted: %q %v", persisted, err)
	}
	if info, err := os.Stat(filepath.Join(directory, AdminKeyFile)); err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("rotated key permissions: %v %v", info, err)
	}
}

func TestAtomicWriteLeavesNoTemporaryFiles(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	if err := store.SaveRegistration(); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary files were left behind: %v", matches)
	}
}

func TestTrustReplaceAndSnapshot(t *testing.T) {
	directory := t.TempDir()
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	store.Trust.Add(trust.Record{Fingerprint: "one"})
	store.Trust.Add(trust.Record{Fingerprint: "two"})
	if err := store.SaveTrust(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reopened.Trust.Snapshot()
	if len(snapshot) != 2 || !reopened.Trust.Has("one") || !reopened.Trust.Has("two") {
		t.Fatalf("unexpected trust snapshot: %+v", snapshot)
	}
	reopened.Trust.Replace(map[string]trust.Record{"three": {Fingerprint: "three"}})
	if reopened.Trust.Has("one") || !reopened.Trust.Has("three") {
		t.Fatalf("replace did not replace: %+v", reopened.Trust.Snapshot())
	}
}
