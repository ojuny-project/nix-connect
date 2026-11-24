package state

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"

	"github.com/ojuny-project/nix-connect/internal/identity"
	"github.com/ojuny-project/nix-connect/internal/registration"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

const (
	ConfigFile       = "config.json"
	RegistrationFile = "registration.json"
	TrustFile        = "trust.json"
	AdminKeyFile     = "admin_ssh.key"
	JobsDir          = "jobs"
	ISOdir           = "iso"
)

type DiscoveryConfig struct {
	Auto     bool     `json:"auto"`
	Ranges   []string `json:"ranges"`
	Interval int      `json:"interval"`
	Timeout  int      `json:"timeout"`
	Workers  int      `json:"workers"`
}

type MagicRollbackConfig struct {
	Enable           bool   `json:"enable"`
	Mode             string `json:"mode"`
	AutomaticTimeout int    `json:"automaticTimeout"`
	ManualTimeout    int    `json:"manualTimeout"`
}

type Config struct {
	Kind               string              `json:"kind"`
	InfoPort           int                 `json:"infoPort"`
	ControlPort        int                 `json:"controlPort"`
	Discovery          DiscoveryConfig     `json:"discovery"`
	TrustedNetworks    []string            `json:"trustedNetworks"`
	PublicIPURL        string              `json:"publicIpUrl"`
	PAMService         string              `json:"pamService"`
	Account            string              `json:"account"`
	SSHUser            string              `json:"sshUser"`
	SSHPort            int                 `json:"sshPort"`
	AdminAuthorizedKey string              `json:"adminAuthorizedKey,omitempty"`
	MagicRollback      MagicRollbackConfig `json:"magicRollback"`
}

func DefaultConfig() Config {
	return Config{
		Kind:        "installed",
		InfoPort:    5060,
		ControlPort: 5061,
		Discovery: DiscoveryConfig{
			Auto:     true,
			Interval: 60,
			Timeout:  2,
			Workers:  64,
		},
		PAMService: "nixconnect",
		Account:    "nixos",
		SSHUser:    "nixos",
		SSHPort:    22,
		MagicRollback: MagicRollbackConfig{
			Enable:           true,
			Mode:             "automatic",
			AutomaticTimeout: 60,
			ManualTimeout:    600,
		},
	}
}

func (c *Config) Normalize() {
	defaults := DefaultConfig()
	if c.Kind == "" {
		c.Kind = defaults.Kind
	}
	if c.InfoPort == 0 {
		c.InfoPort = defaults.InfoPort
	}
	if c.ControlPort == 0 {
		c.ControlPort = defaults.ControlPort
	}
	if c.Discovery.Interval <= 0 {
		c.Discovery.Interval = defaults.Discovery.Interval
	}
	if c.Discovery.Timeout <= 0 {
		c.Discovery.Timeout = defaults.Discovery.Timeout
	}
	if c.Discovery.Workers <= 0 {
		c.Discovery.Workers = defaults.Discovery.Workers
	}
	if c.PAMService == "" {
		c.PAMService = defaults.PAMService
	}
	if c.Account == "" {
		c.Account = defaults.Account
	}
	if c.SSHUser == "" {
		c.SSHUser = c.Account
	}
	if c.SSHPort == 0 {
		c.SSHPort = defaults.SSHPort
	}
	if c.MagicRollback.Mode == "" {
		c.MagicRollback.Mode = defaults.MagicRollback.Mode
	}
	if c.MagicRollback.AutomaticTimeout <= 0 {
		c.MagicRollback.AutomaticTimeout = defaults.MagicRollback.AutomaticTimeout
	}
	if c.MagicRollback.ManualTimeout <= 0 {
		c.MagicRollback.ManualTimeout = defaults.MagicRollback.ManualTimeout
	}
}

type Store struct {
	Dir          string
	Config       Config
	Identity     *identity.Identity
	AdminKey     ed25519.PrivateKey
	Registration registration.Record
	Trust        *trust.Store

	mutex sync.Mutex
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, JobsDir), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, ISOdir), 0o700); err != nil {
		return nil, err
	}

	store := &Store{
		Dir:   dir,
		Trust: trust.New(),
	}
	if err := store.loadConfig(); err != nil {
		return nil, err
	}
	device, err := identity.LoadOrCreate(filepath.Join(dir, "identity"))
	if err != nil {
		return nil, err
	}
	store.Identity = device
	if err := store.loadAdminKey(); err != nil {
		return nil, err
	}
	if err := store.loadRegistration(); err != nil {
		return nil, err
	}
	if err := store.loadTrust(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) loadConfig() error {
	s.Config = DefaultConfig()
	path := filepath.Join(s.Dir, ConfigFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.Config); err != nil {
		return fmt.Errorf("state: parse %s: %w", ConfigFile, err)
	}
	s.Config.Normalize()
	return nil
}

func (s *Store) SaveConfig() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return writeJSON(filepath.Join(s.Dir, ConfigFile), s.Config, 0o600)
}

func (s *Store) loadAdminKey() error {
	path := filepath.Join(s.Dir, AdminKeyFile)
	data, err := os.ReadFile(path)
	if err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return errors.New("state: invalid administration key")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		privateKey, ok := key.(ed25519.PrivateKey)
		if !ok {
			return errors.New("state: administration key is not Ed25519")
		}
		s.AdminKey = privateKey
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := writeFile(path, block, 0o400); err != nil {
		return err
	}
	s.AdminKey = private
	return nil
}

func (s *Store) AdminSigner() (ssh.Signer, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return ssh.NewSignerFromKey(s.AdminKey)
}

func (s *Store) AdminKeyPEM() ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return adminKeyPEM(s.AdminKey)
}

func (s *Store) AdminAuthorizedKey() (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return adminAuthorizedKey(s.AdminKey)
}

func (s *Store) RotateAdminKey() (ed25519.PrivateKey, error) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := s.SetAdminKey(private); err != nil {
		return nil, err
	}
	return private, nil
}

func (s *Store) AdminKeyCopy() ed25519.PrivateKey {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.AdminKey
}

func (s *Store) SetAdminKey(private ed25519.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return err
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := writeFile(filepath.Join(s.Dir, AdminKeyFile), block, 0o400); err != nil {
		return err
	}
	s.AdminKey = private
	return nil
}

func ParseAdminKeyPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("state: invalid administration key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("state: administration key is not Ed25519")
	}
	return private, nil
}

func adminKeyPEM(private ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func adminAuthorizedKey(private ed25519.PrivateKey) (string, error) {
	public, err := ssh.NewPublicKey(private.Public())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))), nil
}

func (s *Store) loadRegistration() error {
	path := filepath.Join(s.Dir, RegistrationFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.Registration)
}

func (s *Store) SaveRegistration() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return writeJSON(filepath.Join(s.Dir, RegistrationFile), s.Registration, 0o600)
}

func (s *Store) loadTrust() error {
	path := filepath.Join(s.Dir, TrustFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	records := make(map[string]trust.Record)
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	s.Trust.Replace(records)
	return nil
}

func (s *Store) SaveTrust() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return writeJSON(filepath.Join(s.Dir, TrustFile), s.Trust.Snapshot(), 0o600)
}

func (s *Store) JobDir(id string) string {
	return filepath.Join(s.Dir, JobsDir, id)
}

func (s *Store) ISODir() string {
	return filepath.Join(s.Dir, ISOdir)
}

func writeJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data, mode)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}
