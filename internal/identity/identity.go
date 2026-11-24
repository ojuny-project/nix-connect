package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	privateKeyFile = "identity.key"
	publicKeyFile  = "identity.pub"
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

type Identity struct {
	Private ed25519.PrivateKey
	Public  ed25519.PublicKey
}

func LoadOrCreate(dir string) (*Identity, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}

	privatePath := filepath.Join(dir, privateKeyFile)
	data, err := os.ReadFile(privatePath)
	if err == nil {
		return parsePrivate(data)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFile(privatePath, block, 0o400); err != nil {
		return nil, err
	}
	if err := writeFile(filepath.Join(dir, publicKeyFile), []byte(PublicKeyText(public)), 0o444); err != nil {
		return nil, err
	}
	return &Identity{Private: private, Public: public}, nil
}

func parsePrivate(data []byte) (*Identity, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("identity: invalid private key encoding")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("identity: private key is not Ed25519")
	}
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("identity: private key has no public key")
	}
	return &Identity{Private: private, Public: public}, nil
}

func PublicKeyText(public ed25519.PublicKey) string {
	return b32.EncodeToString(public)
}

func Fingerprint(public ed25519.PublicKey) string {
	sum := sha256.Sum256(public)
	return b32.EncodeToString(sum[:])
}

func (i *Identity) Fingerprint() string {
	return Fingerprint(i.Public)
}

func (i *Identity) ShortID() string {
	fingerprint := i.Fingerprint()
	if len(fingerprint) < 8 {
		return fingerprint
	}
	return fingerprint[:8]
}

func (i *Identity) TLSCertificate() (tls.Certificate, error) {
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "nix-connect",
			Organization: []string{"nix-connect"},
		},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"nix-connect"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, i.Public, i.Private)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  i.Private,
	}, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func ParsePublic(text string) (ed25519.PublicKey, error) {
	raw, err := b32.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("identity: decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("identity: public key has %d bytes", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}
