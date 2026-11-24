package info

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ojuny-project/nix-connect/internal/identity"
)

const Version = "1"

type Payload struct {
	ID           string
	Version      string
	Hostname     string
	Kind         string
	Addresses    []string
	SSHPort      int
	ControlPort  int
	Registered   bool
	Capabilities []string
	Job          string
	Daemon       string
	Timestamp    int64
	PublicKey    ed25519.PublicKey
}

func (p Payload) Encode(private ed25519.PrivateKey) ([]byte, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, errors.New("info: invalid signing key")
	}
	if len(p.PublicKey) != ed25519.PublicKeySize {
		return nil, errors.New("info: missing public key")
	}
	if p.Version == "" {
		p.Version = Version
	}
	if p.Timestamp == 0 {
		p.Timestamp = time.Now().Unix()
	}
	lines := []string{
		"id=" + p.ID,
		"version=" + p.Version,
		"hostname=" + p.Hostname,
		"kind=" + p.Kind,
		"addresses=" + strings.Join(p.Addresses, ","),
		"ssh-port=" + strconv.Itoa(p.SSHPort),
		"control-port=" + strconv.Itoa(p.ControlPort),
		"registered=" + boolText(p.Registered),
		"capabilities=" + strings.Join(p.Capabilities, ","),
		"job=" + p.Job,
		"daemon=" + p.Daemon,
		"timestamp=" + strconv.FormatInt(p.Timestamp, 10),
		"key=" + base64.StdEncoding.EncodeToString(p.PublicKey),
	}
	signed := []byte(strings.Join(lines, "\n"))
	signature := ed25519.Sign(private, signed)
	return append(append(signed, []byte("\nsig=")...), []byte(base64.StdEncoding.EncodeToString(signature))...), nil
}

func Decode(data []byte) (Payload, error) {
	index := bytes.LastIndex(data, []byte("\nsig="))
	if index < 0 {
		return Payload{}, errors.New("info: missing signature")
	}
	signed := data[:index]
	signatureText := string(data[index+len("\nsig="):])

	fields := make(map[string]string)
	for _, line := range strings.Split(string(signed), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fields[key] = value
	}

	rawKey, err := base64.StdEncoding.DecodeString(fields["key"])
	if err != nil || len(rawKey) != ed25519.PublicKeySize {
		return Payload{}, errors.New("info: invalid public key")
	}
	publicKey := ed25519.PublicKey(rawKey)
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureText))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Payload{}, errors.New("info: invalid signature encoding")
	}
	if !ed25519.Verify(publicKey, signed, signature) {
		return Payload{}, errors.New("info: signature verification failed")
	}

	port, _ := strconv.Atoi(fields["ssh-port"])
	controlPort, _ := strconv.Atoi(fields["control-port"])
	timestamp, _ := strconv.ParseInt(fields["timestamp"], 10, 64)
	payload := Payload{
		ID:           fields["id"],
		Version:      fields["version"],
		Hostname:     fields["hostname"],
		Kind:         fields["kind"],
		Addresses:    splitList(fields["addresses"]),
		SSHPort:      port,
		ControlPort:  controlPort,
		Registered:   fields["registered"] == "1",
		Capabilities: splitList(fields["capabilities"]),
		Job:          fields["job"],
		Daemon:       fields["daemon"],
		Timestamp:    timestamp,
		PublicKey:    publicKey,
	}
	if payload.ID == "" {
		payload.ID = identity.Fingerprint(publicKey)
	}
	if payload.ID != identity.Fingerprint(publicKey) {
		return Payload{}, errors.New("info: identity does not match key")
	}
	if payload.Version != Version {
		return Payload{}, fmt.Errorf("info: unsupported version %q", payload.Version)
	}
	return payload, nil
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
