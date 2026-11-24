package pairing

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/hkdf"
)

const (
	Version  = 1
	codeSize = 6
)

var ErrExpired = errors.New("pairing: request expired")

type Request struct {
	ID      string    `json:"id"`
	PeerID  string    `json:"peerId"`
	PeerKey string    `json:"peerKey"`
	Code    string    `json:"code"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

type Manager struct {
	SelfID string
	TTL    time.Duration

	mutex   sync.Mutex
	pending map[string]Request
}

func New(selfID string) *Manager {
	return &Manager{
		SelfID:  selfID,
		TTL:     2 * time.Minute,
		pending: make(map[string]Request),
	}
}

func (m *Manager) Start(peerID, peerKey string) (Request, error) {
	id, err := randomHex(8)
	if err != nil {
		return Request{}, err
	}
	now := time.Now().UTC()
	ttl := m.TTL
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	request := Request{
		ID:      id,
		PeerID:  peerID,
		PeerKey: peerKey,
		Code:    Code(m.SelfID, peerID),
		Created: now,
		Expires: now.Add(ttl),
	}
	m.mutex.Lock()
	m.pending[id] = request
	m.mutex.Unlock()
	return request, nil
}

func (m *Manager) Confirm(id, code string) (Request, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	request, ok := m.pending[id]
	if !ok {
		return Request{}, fmt.Errorf("pairing: unknown request %q", id)
	}
	if time.Now().After(request.Expires) {
		delete(m.pending, id)
		return Request{}, ErrExpired
	}
	if subtle.ConstantTimeCompare([]byte(request.Code), []byte(code)) != 1 {
		return Request{}, errors.New("pairing: code mismatch")
	}
	delete(m.pending, id)
	return request, nil
}

func (m *Manager) Pending() []Request {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	result := make([]Request, 0, len(m.pending))
	for _, request := range m.pending {
		if time.Now().Before(request.Expires) {
			result = append(result, request)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Created.Before(result[j].Created)
	})
	return result
}

func Code(left, right string) string {
	parts := []string{left, right}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, ":")))
	value := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	return fmt.Sprintf("%06d", value%1000000)
}

type Envelope struct {
	Version    int    `json:"version"`
	Ephemeral  string `json:"ephemeral"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func NewEphemeral() (*ecdh.PrivateKey, string, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return private, base64.StdEncoding.EncodeToString(private.PublicKey().Bytes()), nil
}

func Seal(recipientPublic []byte, plaintext []byte, code string) (Envelope, error) {
	curve := ecdh.X25519()
	publicKey, err := curve.NewPublicKey(recipientPublic)
	if err != nil {
		return Envelope{}, err
	}
	ephemeral, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return Envelope{}, err
	}
	shared, err := ephemeral.ECDH(publicKey)
	if err != nil {
		return Envelope{}, err
	}
	key, err := deriveKey(shared, code)
	if err != nil {
		return Envelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return Envelope{
		Version:    Version,
		Ephemeral:  base64.StdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}, nil
}

func Open(recipientPrivate *ecdh.PrivateKey, envelope Envelope, code string) ([]byte, error) {
	if envelope.Version != Version {
		return nil, errors.New("pairing: unsupported envelope version")
	}
	ephemeralBytes, err := base64.StdEncoding.DecodeString(envelope.Ephemeral)
	if err != nil {
		return nil, err
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralBytes)
	if err != nil {
		return nil, err
	}
	shared, err := recipientPrivate.ECDH(ephemeral)
	if err != nil {
		return nil, err
	}
	key, err := deriveKey(shared, code)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func deriveKey(shared []byte, code string) ([]byte, error) {
	reader := hkdf.New(sha256.New, shared, []byte(code), []byte("nix-connect-key-share"))
	key := make([]byte, 32)
	if _, err := io.ReadFull(reader, key); err != nil {
		return nil, err
	}
	return key, nil
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
