package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ojuny-project/nix-connect/internal/identity"
)

type peerKey struct{}

func WithPeer(ctx context.Context, fingerprint string) context.Context {
	return context.WithValue(ctx, peerKey{}, fingerprint)
}

func Peer(ctx context.Context) string {
	value, _ := ctx.Value(peerKey{}).(string)
	return value
}

type Server struct {
	service Service
	logger  *slog.Logger
}

func NewServer(service Service, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{service: service, logger: logger}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.service.Status(r.Context()))
	})
	mux.HandleFunc("GET /v1/devices", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.service.Devices(r.Context()))
	})
	mux.HandleFunc("POST /v1/devices/{id}/register", func(w http.ResponseWriter, r *http.Request) {
		var request RegisterRequest
		if !decode(w, r, &request) {
			return
		}
		if err := s.service.Register(r.Context(), r.PathValue("id"), request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/devices/{id}/unregister", func(w http.ResponseWriter, r *http.Request) {
		var request UnregisterRequest
		if !decodeOptional(w, r, &request) {
			return
		}
		if err := s.service.Unregister(r.Context(), r.PathValue("id"), request.Password); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/devices/{id}/deploy", func(w http.ResponseWriter, r *http.Request) {
		var request DeployRequest
		if !decode(w, r, &request) {
			return
		}
		id, err := s.service.Deploy(r.Context(), r.PathValue("id"), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
	})
	mux.HandleFunc("POST /v1/devices/{id}/update", func(w http.ResponseWriter, r *http.Request) {
		var request UpdateRequest
		if !decode(w, r, &request) {
			return
		}
		id, err := s.service.Update(r.Context(), r.PathValue("id"), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"job": id})
	})
	mux.HandleFunc("POST /v1/devices/{id}/ssh", func(w http.ResponseWriter, r *http.Request) {
		info, err := s.service.SSH(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	})
	mux.HandleFunc("POST /v1/registration/reset", func(w http.ResponseWriter, r *http.Request) {
		if err := s.service.ResetRegistration(r.Context()); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/trust", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.service.TrustRecords(r.Context()))
	})
	mux.HandleFunc("POST /v1/trust/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		if err := s.service.RevokeTrust(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/key/rotate", func(w http.ResponseWriter, r *http.Request) {
		publicKey, err := s.service.RotateAdminKey(r.Context())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": publicKey})
	})
	mux.HandleFunc("POST /v1/key/import", func(w http.ResponseWriter, r *http.Request) {
		var request KeyImportRequest
		if !decode(w, r, &request) {
			return
		}
		publicKey, err := s.service.ImportSharedKey(r.Context(), request.PeerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": publicKey})
	})
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.service.Jobs(r.Context()))
	})
	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := s.service.Job(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
	})
	mux.HandleFunc("GET /v1/jobs/{id}/events", s.events)
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := s.service.Cancel(r.Context(), r.PathValue("id")); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/jobs/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		confirmer := Peer(r.Context())
		if confirmer == "" {
			confirmer = "local"
		}
		if err := s.service.Confirm(r.Context(), r.PathValue("id"), confirmer); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/pair/start", func(w http.ResponseWriter, r *http.Request) {
		var request PairRequest
		if !decode(w, r, &request) {
			return
		}
		response, err := s.service.PairStart(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST /v1/pair/confirm", func(w http.ResponseWriter, r *http.Request) {
		var request PairConfirmRequest
		if !decode(w, r, &request) {
			return
		}
		if err := s.service.PairConfirm(r.Context(), request); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/pair/pending", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.service.PairPending(r.Context()))
	})
	mux.HandleFunc("POST /v1/key/share", func(w http.ResponseWriter, r *http.Request) {
		var request KeyShareRequest
		if !decode(w, r, &request) {
			return
		}
		envelope, err := s.service.ShareKey(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, envelope)
	})
	mux.HandleFunc("POST /v1/iso/deploytarget", func(w http.ResponseWriter, r *http.Request) {
		var request ISORequest
		if !decode(w, r, &request) {
			return
		}
		job, err := s.service.BuildISO(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"job": job})
	})
	mux.HandleFunc("POST /v1/publish", func(w http.ResponseWriter, r *http.Request) {
		var request PublishRequest
		if !decode(w, r, &request) {
			return
		}
		job, err := s.service.Publish(r.Context(), request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"job": job})
	})

	return mux
}

func (s *Server) ServeLocal(ctx context.Context, socketPath string) error {
	_ = os.Remove(socketPath)
	if err := os.MkdirAll(filepathDir(socketPath), 0o750); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.Handler().ServeHTTP(w, r.WithContext(WithPeer(r.Context(), "local")))
		}),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ServeTLS(ctx context.Context, address string, certificate tls.Certificate, verify func(*x509.Certificate) error) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return s.ServeTLSListener(ctx, listener, certificate, verify)
}

func (s *Server) ServeTLSListener(ctx context.Context, listener net.Listener, certificate tls.Certificate, verify func(*x509.Certificate) error) error {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("httpapi: missing client certificate")
			}
			cert, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			if verify != nil {
				return verify(cert)
			}
			return nil
		},
	}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer := ""
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				if public, ok := r.TLS.PeerCertificates[0].PublicKey.(ed25519.PublicKey); ok {
					peer = identity.Fingerprint(public)
				}
			}
			s.Handler().ServeHTTP(w, r.WithContext(WithPeer(r.Context(), peer)))
		}),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	err := server.Serve(tls.NewListener(listener, tlsConfig))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("httpapi: streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var last time.Time
	send := func() bool {
		job, err := s.service.Job(r.Context(), id)
		if err != nil {
			return false
		}
		if !job.Updated.Equal(last) {
			last = job.Updated
			data, _ := json.Marshal(job)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
		return true
	}
	if !send() {
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !send() {
				return
			}
		}
	}
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, errors.New("httpapi: missing body"))
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func decodeOptional(w http.ResponseWriter, r *http.Request, value any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(value); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func filepathDir(path string) string {
	for index := len(path) - 1; index >= 0; index-- {
		if path[index] == '/' {
			return path[:index]
		}
	}
	return "."
}
