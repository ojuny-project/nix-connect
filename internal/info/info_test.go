package info

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/identity"
)

func TestPayloadRoundTrip(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := Payload{
		ID:           identity.Fingerprint(public),
		Version:      Version,
		Hostname:     "target",
		Kind:         "installed",
		Addresses:    []string{"10.0.0.10", "10.0.0.11"},
		SSHPort:      22,
		Registered:   true,
		Capabilities: []string{"ssh", "update"},
		Job:          "activating",
		Daemon:       "0.1.0",
		Timestamp:    time.Now().Unix(),
		PublicKey:    public,
	}
	data, err := payload.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ID != payload.ID || decoded.Hostname != payload.Hostname {
		t.Fatalf("unexpected payload: %+v", decoded)
	}
	if !decoded.Registered || decoded.SSHPort != 22 || len(decoded.Addresses) != 2 {
		t.Fatalf("unexpected fields: %+v", decoded)
	}
}

func TestPayloadRejectsTampering(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := Payload{
		ID:        identity.Fingerprint(public),
		Version:   Version,
		Hostname:  "target",
		Kind:      "installed",
		SSHPort:   22,
		Timestamp: time.Now().Unix(),
		PublicKey: public,
	}
	data, err := payload.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(data, []byte("hostname=target"), []byte("hostname=evil"), 1)
	if _, err := Decode(tampered); err == nil {
		t.Fatal("tampered payload was accepted")
	}
}

func TestDecodeRejectsMissingSignature(t *testing.T) {
	if _, err := Decode([]byte("id=device")); err == nil {
		t.Fatal("payload without a signature was accepted")
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := Payload{
		ID:        identity.Fingerprint(public),
		Version:   "2",
		Hostname:  "target",
		Kind:      "installed",
		Timestamp: time.Now().Unix(),
		PublicKey: public,
	}
	data, err := payload.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("unsupported version was accepted")
	}
}

func TestDecodeRejectsFingerprintMismatch(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := Payload{
		ID:        "wrong",
		Version:   Version,
		Hostname:  "target",
		Kind:      "installed",
		Timestamp: time.Now().Unix(),
		PublicKey: public,
	}
	data, err := payload.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("fingerprint mismatch was accepted")
	}
}

func TestControlPortRoundTrip(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := Payload{
		ID:           identity.Fingerprint(public),
		Version:      Version,
		Hostname:     "target",
		Kind:         "installed",
		SSHPort:      2222,
		ControlPort:  15061,
		Registered:   true,
		Capabilities: []string{"ssh", "magic-rollback"},
		Timestamp:    time.Now().Unix(),
		PublicKey:    public,
	}
	data, err := payload.Encode(private)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ControlPort != 15061 || decoded.SSHPort != 2222 || len(decoded.Capabilities) != 2 {
		t.Fatalf("unexpected decoded payload: %+v", decoded)
	}
}
