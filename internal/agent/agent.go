package agent

import (
	"context"
	"crypto/ed25519"
	"net"
	"os"
	"sync"

	"golang.org/x/crypto/ssh/agent"
)

type Server struct {
	keyring agent.Agent
	key     ed25519.PrivateKey
	mutex   sync.Mutex
	cleanup func()
}

func Start(path string, privateKey ed25519.PrivateKey) (*Server, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		listener.Close()
		return nil, err
	}

	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{
		PrivateKey: privateKey,
		Comment:    "nix-connect administration key",
	}); err != nil {
		listener.Close()
		os.Remove(path)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				_ = agent.ServeAgent(keyring, connection)
			}()
		}
	}()

	return &Server{
		keyring: keyring,
		key:     privateKey,
		cleanup: func() {
			cancel()
			_ = listener.Close()
			_ = os.Remove(path)
		},
	}, nil
}

func Serve(path string, privateKey ed25519.PrivateKey) (func(), error) {
	server, err := Start(path, privateKey)
	if err != nil {
		return nil, err
	}
	return server.Close, nil
}

func (s *Server) Close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.cleanup != nil {
		s.cleanup()
		s.cleanup = nil
	}
}

func (s *Server) Rotate(privateKey ed25519.PrivateKey) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if err := s.keyring.RemoveAll(); err != nil {
		return err
	}
	if err := s.keyring.Add(agent.AddedKey{
		PrivateKey: privateKey,
		Comment:    "nix-connect administration key",
	}); err != nil {
		_ = s.keyring.Add(agent.AddedKey{
			PrivateKey: s.key,
			Comment:    "nix-connect administration key",
		})
		return err
	}
	s.key = privateKey
	return nil
}
