package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() {
		t.Fatalf("fingerprint changed: %s != %s", first.Fingerprint(), second.Fingerprint())
	}
	if len(first.ShortID()) != 8 {
		t.Fatalf("unexpected short id %q", first.ShortID())
	}
	info, err := os.Stat(filepath.Join(dir, privateKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("unexpected private key mode %o", info.Mode().Perm())
	}
	certificate, err := first.TLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	if len(certificate.Certificate) == 0 {
		t.Fatal("empty certificate")
	}
}

func TestPublicKeyTextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	generated, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePublic(PublicKeyText(generated.Public))
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(parsed) != generated.Fingerprint() {
		t.Fatal("public key text did not round trip")
	}
}

func TestFingerprintAndShortIDShape(t *testing.T) {
	identity, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.Fingerprint()) != 52 {
		t.Fatalf("unexpected fingerprint length %d", len(identity.Fingerprint()))
	}
	if len(identity.ShortID()) != 8 {
		t.Fatalf("unexpected short id length %d", len(identity.ShortID()))
	}
	if identity.ShortID() != identity.Fingerprint()[:8] {
		t.Fatal("short id is not the fingerprint prefix")
	}
}

func TestTLSCertificateMatchesIdentity(t *testing.T) {
	identity, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := identity.TLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	public, ok := parsed.PublicKey.(ed25519.PublicKey)
	if !ok {
		t.Fatal("certificate public key is not Ed25519")
	}
	if !bytes.Equal(public, identity.Public) {
		t.Fatal("certificate does not use the identity key")
	}
}

func TestLoadOrCreateRejectsCorruptKey(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, privateKeyFile), []byte("not a key"), 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreate(directory); err == nil {
		t.Fatal("corrupt key was accepted")
	}
}
