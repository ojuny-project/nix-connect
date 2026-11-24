package pairing

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestCodeIsSymmetric(t *testing.T) {
	left := Code("device-a", "device-b")
	right := Code("device-b", "device-a")
	if left != right {
		t.Fatalf("codes differ: %s != %s", left, right)
	}
	if len(left) != codeSize {
		t.Fatalf("unexpected code length %d", len(left))
	}
}

func TestSealAndOpen(t *testing.T) {
	private, public, err := NewEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	rawPublic, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("administration ssh private key")
	code := Code("a", "b")
	envelope, err := Seal(rawPublic, secret, code)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(private, envelope, code)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != string(secret) {
		t.Fatal("sealed secret did not round trip")
	}
	if _, err := Open(private, envelope, "000000"); err == nil {
		t.Fatal("wrong code opened the envelope")
	}
}

func TestManagerExpiresRequests(t *testing.T) {
	manager := New("self")
	manager.TTL = 10 * time.Millisecond
	request, err := manager.Start("peer", "key")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := manager.Confirm(request.ID, request.Code); err != ErrExpired {
		t.Fatalf("expected expiry, got %v", err)
	}
}

func TestPendingAndWrongCode(t *testing.T) {
	manager := New("self")
	request, err := manager.Start("peer", "key")
	if err != nil {
		t.Fatal(err)
	}
	if len(manager.Pending()) != 1 {
		t.Fatalf("unexpected pending count %d", len(manager.Pending()))
	}
	if _, err := manager.Confirm(request.ID, "000000"); err == nil {
		t.Fatal("wrong code was accepted")
	}
	if len(manager.Pending()) != 1 {
		t.Fatal("wrong code removed the pending request")
	}
	if _, err := manager.Confirm(request.ID, request.Code); err != nil {
		t.Fatal(err)
	}
	if len(manager.Pending()) != 0 {
		t.Fatal("confirmed request remains pending")
	}
}

func TestOpenRejectsUnsupportedVersion(t *testing.T) {
	private, public, err := NewEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(raw, []byte("secret"), "123456")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Version = 2
	if _, err := Open(private, envelope, "123456"); err == nil {
		t.Fatal("unsupported envelope version was accepted")
	}
}

func TestSealRejectsInvalidRecipientKey(t *testing.T) {
	if _, err := Seal([]byte("short"), []byte("secret"), "123456"); err == nil {
		t.Fatal("invalid recipient key was accepted")
	}
}

func TestCodeDiffersForDifferentPeers(t *testing.T) {
	if Code("self", "one") == Code("self", "two") {
		t.Fatal("different peers produced the same code")
	}
}
