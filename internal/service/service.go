package service

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ojuny-project/nix-connect/internal/consent"
	"github.com/ojuny-project/nix-connect/internal/deploy"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/identity"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/registration"
	"github.com/ojuny-project/nix-connect/internal/registry"
	"github.com/ojuny-project/nix-connect/internal/state"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type Service struct {
	Store        *state.Store
	JobManager   *jobs.Manager
	Registry     *registry.Registry
	Pairing      *pairing.Manager
	Gate         *consent.Gate
	AgentSocket  string
	Version      string
	Hostname     string
	Logger       *slog.Logger
	EngineRunner deploy.Runner
	InstallRoot  string
	ISOSource    string
	PublishRoot  string

	DeployFunc  func(ctx context.Context, jobID, targetID string, request httpapi.DeployRequest) error
	UpdateFunc  func(ctx context.Context, jobID, targetID string, request httpapi.UpdateRequest) error
	ISOFunc     func(ctx context.Context, jobID string, request httpapi.ISORequest) error
	PublishFunc func(ctx context.Context, jobID string, request httpapi.PublishRequest) error
	ConfirmFunc func(ctx context.Context, job jobs.Job) error
	RotateAgent func(key ed25519.PrivateKey) error

	remoteMutex sync.Mutex
	remoteJobs  map[string]remoteJob
	remotePairs map[string]remotePair
}

type remoteJob struct {
	TargetID string `json:"targetId"`
	JobID    string `json:"jobId"`
}

type remotePair struct {
	TargetID string
	Code     string
	Expires  time.Time
}

func (s *Service) Status(ctx context.Context) httpapi.Status {
	trusted := s.requireTrusted(ctx) == nil
	record := s.Store.Registration
	if !trusted {
		record = registration.Record{}
	}
	return httpapi.Status{
		ID:            s.Store.Identity.Fingerprint(),
		ShortID:       s.Store.Identity.ShortID(),
		Hostname:      s.hostname(),
		InfoPort:      s.Store.Config.InfoPort,
		ControlPort:   s.Store.Config.ControlPort,
		Peers:         s.Registry.Count(),
		Registration:  record,
		MagicRollback: s.Store.Config.MagicRollback,
	}
}

func (s *Service) Devices(ctx context.Context) []httpapi.Device {
	_ = s.requireTrusted(ctx)
	result := []httpapi.Device{{
		ID:         s.Store.Identity.Fingerprint(),
		ShortID:    s.Store.Identity.ShortID(),
		Hostname:   s.hostname(),
		Kind:       s.Store.Config.Kind,
		SSHPort:    s.Store.Config.SSHPort,
		Registered: s.Store.Registration.Registered(),
		Paired:     true,
		Self:       true,
	}}
	for _, peer := range s.Registry.List() {
		result = append(result, httpapi.Device{
			ID:         peer.Payload.ID,
			ShortID:    shortID(peer.Payload.ID),
			Hostname:   peer.Payload.Hostname,
			Address:    peer.Address,
			Kind:       peer.Payload.Kind,
			Job:        peer.Payload.Job,
			SSHPort:    peer.Payload.SSHPort,
			Registered: peer.Payload.Registered,
			Paired:     s.Store.Trust.Has(peer.Payload.ID),
			LastSeen:   peer.LastSeen,
		})
	}
	return result
}

func (s *Service) Register(ctx context.Context, id string, request httpapi.RegisterRequest) error {
	if err := s.requireTrusted(ctx); err != nil {
		return err
	}
	if id != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(id)
		if err != nil {
			return err
		}
		if err := client.Register(ctx, id, request); err != nil {
			return err
		}
		s.rememberPeerTrust(id)
		return nil
	}
	account := request.Account
	if account == "" {
		account = s.Store.Config.Account
	}
	if err := s.authorize(ctx, account, request.Password); err != nil {
		return err
	}
	if request.Repository == "" || request.Branch == "" || request.Host == "" {
		return errors.New("service: repository, branch and host are required")
	}
	user := request.SSHUser
	if user == "" {
		user = s.Store.Config.SSHUser
	}
	port := request.SSHPort
	if port == 0 {
		port = s.Store.Config.SSHPort
	}
	record := stateRegistration(request.Repository, request.Branch, request.Host, user, port, account)
	s.Store.Registration = record
	s.Store.Registration.EnableUpdates()
	return s.Store.SaveRegistration()
}

func (s *Service) Unregister(ctx context.Context, id string, password string) error {
	if err := s.requireTrusted(ctx); err != nil {
		return err
	}
	if id != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(id)
		if err != nil {
			return err
		}
		if err := client.Unregister(ctx, id, password); err != nil {
			return err
		}
		s.rememberPeerTrust(id)
		return nil
	}
	if err := s.authorize(ctx, s.Store.Config.Account, password); err != nil {
		return err
	}
	s.Store.Registration = registration.Record{}
	return s.Store.SaveRegistration()
}

func (s *Service) ResetRegistration(ctx context.Context) error {
	if err := s.requireLocal(ctx); err != nil {
		return err
	}
	if !s.Store.Registration.Registered() {
		return errors.New("service: device is not registered")
	}
	s.Store.Registration.DisableUpdates("no-commit")
	return s.Store.SaveRegistration()
}

func (s *Service) RotateAdminKey(ctx context.Context) (string, error) {
	if err := s.requireLocal(ctx); err != nil {
		return "", err
	}
	previous := s.Store.AdminKeyCopy()
	key, err := s.Store.RotateAdminKey()
	if err != nil {
		return "", err
	}
	if s.RotateAgent != nil {
		if err := s.RotateAgent(key); err != nil {
			if restore := s.Store.SetAdminKey(previous); restore != nil {
				return "", fmt.Errorf("service: restore administration key: %w", restore)
			}
			return "", err
		}
	}
	publicKey, err := s.Store.AdminAuthorizedKey()
	if err != nil {
		return "", err
	}
	s.Store.Config.AdminAuthorizedKey = publicKey
	if err := s.Store.SaveConfig(); err != nil {
		return "", err
	}
	return publicKey, nil
}

func (s *Service) ImportSharedKey(ctx context.Context, peerID string) (string, error) {
	if err := s.requireLocal(ctx); err != nil {
		return "", err
	}
	if peerID == "" || peerID == s.Store.Identity.Fingerprint() {
		return "", errors.New("service: a peer id is required")
	}
	if !s.Store.Trust.Has(peerID) {
		return "", errors.New("service: peer is not paired")
	}
	client, err := s.remoteClient(peerID)
	if err != nil {
		return "", err
	}
	ephemeral, publicKey, err := pairing.NewEphemeral()
	if err != nil {
		return "", err
	}
	fingerprint := s.Store.Identity.Fingerprint()
	envelope, err := client.ShareKey(ctx, httpapi.KeyShareRequest{PeerID: fingerprint, PeerPublicKey: publicKey})
	if err != nil {
		return "", err
	}
	pem, err := pairing.Open(ephemeral, envelope, pairing.Code(fingerprint, peerID))
	if err != nil {
		return "", err
	}
	private, err := state.ParseAdminKeyPEM(pem)
	if err != nil {
		return "", err
	}
	previous := s.Store.AdminKeyCopy()
	if err := s.Store.SetAdminKey(private); err != nil {
		return "", err
	}
	if s.RotateAgent != nil {
		if err := s.RotateAgent(private); err != nil {
			if restore := s.Store.SetAdminKey(previous); restore != nil {
				return "", fmt.Errorf("service: restore administration key: %w", restore)
			}
			return "", err
		}
	}
	public, err := s.Store.AdminAuthorizedKey()
	if err != nil {
		return "", err
	}
	s.Store.Config.AdminAuthorizedKey = public
	if err := s.Store.SaveConfig(); err != nil {
		return "", err
	}
	return public, nil
}

func (s *Service) TrustRecords(ctx context.Context) []trust.Record {
	if err := s.requireTrusted(ctx); err != nil {
		return nil
	}
	return s.Store.Trust.List()
}

func (s *Service) RevokeTrust(ctx context.Context, fingerprint string) error {
	if err := s.requireTrusted(ctx); err != nil {
		return err
	}
	if fingerprint == "" {
		return errors.New("service: fingerprint is required")
	}
	caller := httpapi.Peer(ctx)
	if caller != "" && caller != "local" && caller != fingerprint {
		return errors.New("service: a peer can only revoke its own trust record")
	}
	var notifyErr error
	if caller == "local" && fingerprint != s.Store.Identity.Fingerprint() {
		if _, ok := s.Registry.Get(fingerprint); ok {
			client, err := s.remoteClient(fingerprint)
			if err != nil {
				notifyErr = err
			} else if err := client.RevokeTrust(ctx, s.Store.Identity.Fingerprint()); err != nil {
				notifyErr = err
			}
		}
	}
	s.Store.Trust.Remove(fingerprint)
	if err := s.Store.SaveTrust(); err != nil {
		return err
	}
	if notifyErr != nil {
		return fmt.Errorf("service: revoked locally but the peer was not notified: %w", notifyErr)
	}
	return nil
}

func (s *Service) Deploy(ctx context.Context, id string, request httpapi.DeployRequest) (string, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return "", err
	}
	if request.AdminKey == "" {
		if key, err := s.Store.AdminAuthorizedKey(); err == nil {
			request.AdminKey = key
		}
	}
	if id != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(id)
		if err != nil {
			return "", err
		}
		jobID, err := client.Deploy(ctx, id, request)
		if err != nil {
			return "", err
		}
		s.rememberPeerTrust(id)
		s.rememberRemoteJob(id, jobID)
		if peer, ok := s.Registry.Get(id); !ok || peer.Payload.Kind != "live" {
			s.startAutoConfirm(id, jobID, client, request.Mode, request.Timeout, request.MagicRollback)
		}
		return jobID, nil
	}
	if err := s.authorize(ctx, s.Store.Config.Account, request.Password); err != nil {
		return "", err
	}
	job, err := s.JobManager.Create(jobs.KindDeploy, s.Store.Identity.Fingerprint(), id)
	if err != nil {
		return "", err
	}
	go s.runJob(job.ID, func(ctx context.Context) error {
		if s.DeployFunc == nil {
			return errors.New("service: deployment engine is not configured")
		}
		return s.DeployFunc(ctx, job.ID, id, request)
	})
	return job.ID, nil
}

func (s *Service) Update(ctx context.Context, id string, request httpapi.UpdateRequest) (string, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return "", err
	}
	if request.AdminKey == "" {
		if key, err := s.Store.AdminAuthorizedKey(); err == nil {
			request.AdminKey = key
		}
	}
	if id != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(id)
		if err != nil {
			return "", err
		}
		jobID, err := client.Update(ctx, id, request)
		if err != nil {
			return "", err
		}
		s.rememberPeerTrust(id)
		s.rememberRemoteJob(id, jobID)
		s.startAutoConfirm(id, jobID, client, request.Mode, request.Timeout, request.MagicRollback)
		return jobID, nil
	}
	if err := s.authorize(ctx, s.Store.Config.Account, request.Password); err != nil {
		return "", err
	}
	job, err := s.JobManager.Create(jobs.KindUpdate, s.Store.Identity.Fingerprint(), id)
	if err != nil {
		return "", err
	}
	go s.runJob(job.ID, func(ctx context.Context) error {
		if s.UpdateFunc == nil {
			return errors.New("service: update engine is not configured")
		}
		return s.UpdateFunc(ctx, job.ID, id, request)
	})
	return job.ID, nil
}

func (s *Service) SSH(ctx context.Context, id string) (httpapi.SSHInfo, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return httpapi.SSHInfo{}, err
	}
	if id == s.Store.Identity.Fingerprint() {
		return httpapi.SSHInfo{
			Host:        "127.0.0.1",
			User:        s.Store.Config.SSHUser,
			Port:        s.Store.Config.SSHPort,
			AgentSocket: s.AgentSocket,
		}, nil
	}
	peer, ok := s.Registry.Get(id)
	if !ok {
		return httpapi.SSHInfo{}, fmt.Errorf("service: unknown device %q", id)
	}
	port := peer.Payload.SSHPort
	if port == 0 {
		port = s.Store.Config.SSHPort
	}
	return httpapi.SSHInfo{
		Host:        peer.Address,
		User:        s.Store.Config.SSHUser,
		Port:        port,
		AgentSocket: s.AgentSocket,
	}, nil
}

func (s *Service) Jobs(ctx context.Context) []jobs.Job {
	if err := s.requireTrusted(ctx); err != nil {
		return nil
	}
	result := s.JobManager.List()
	for _, remote := range s.remoteJobList() {
		client, err := s.remoteClient(remote.TargetID)
		if err != nil {
			continue
		}
		job, err := client.Job(ctx, remote.JobID)
		if err == nil {
			result = append(result, job)
		}
	}
	return result
}

func (s *Service) Job(ctx context.Context, id string) (jobs.Job, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return jobs.Job{}, err
	}
	if job, ok := s.JobManager.Get(id); ok {
		return job, nil
	}
	remote, ok := s.remoteJob(id)
	if !ok {
		return jobs.Job{}, fmt.Errorf("service: unknown job %q", id)
	}
	client, err := s.remoteClient(remote.TargetID)
	if err != nil {
		return jobs.Job{}, err
	}
	return client.Job(ctx, remote.JobID)
}

func (s *Service) Confirm(ctx context.Context, id, confirmer string) error {
	if err := s.requireTrusted(ctx); err != nil {
		return err
	}
	if remote, ok := s.remoteJob(id); ok {
		client, err := s.remoteClient(remote.TargetID)
		if err != nil {
			return err
		}
		return client.Confirm(ctx, remote.JobID)
	}
	if confirmer == "" {
		return errors.New("service: confirmation requires a paired administration device")
	}
	job, ok := s.JobManager.Get(id)
	if !ok {
		return fmt.Errorf("service: unknown job %q", id)
	}
	if job.State != jobs.StateAwaitingConfirmation {
		return errors.New("service: job is not awaiting confirmation")
	}
	if !job.ConfirmDeadline.IsZero() && time.Now().After(job.ConfirmDeadline) {
		return errors.New("service: confirmation deadline has passed")
	}
	if err := deploy.ConfirmCanary(job.CanaryPath); err != nil {
		return err
	}
	if _, err := s.JobManager.Confirm(id, confirmer); err != nil {
		return err
	}
	if s.ConfirmFunc != nil {
		return s.ConfirmFunc(ctx, job)
	}
	_, err := s.JobManager.SetState(id, jobs.StateSucceeded)
	return err
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	if remote, ok := s.remoteJob(id); ok {
		client, err := s.remoteClient(remote.TargetID)
		if err != nil {
			return err
		}
		return client.Cancel(ctx, remote.JobID)
	}
	_, err := s.JobManager.Cancel(id)
	return err
}

func (s *Service) PairPending(ctx context.Context) []pairing.Request {
	if err := s.requireLocal(ctx); err != nil {
		return nil
	}
	return s.Pairing.Pending()
}

func (s *Service) PairStart(ctx context.Context, request httpapi.PairRequest) (httpapi.PairResponse, error) {
	peerID := request.PeerID
	if peerID == "" {
		return httpapi.PairResponse{}, errors.New("service: peer id is required")
	}
	caller := httpapi.Peer(ctx)
	remoteCaller := caller != "" && caller != "local"
	if remoteCaller {
		if caller != peerID {
			return httpapi.PairResponse{}, errors.New("service: pairing identity does not match the request")
		}
	} else if peerID != s.Store.Identity.Fingerprint() {
		client, err := s.remoteClient(peerID)
		if err != nil {
			return httpapi.PairResponse{}, err
		}
		response, err := client.PairStart(ctx, httpapi.PairRequest{
			PeerID:  s.Store.Identity.Fingerprint(),
			PeerKey: identity.PublicKeyText(s.Store.Identity.Public),
		})
		if err != nil {
			return httpapi.PairResponse{}, err
		}
		s.rememberRemotePair(response.ID, peerID, response.Code, response.Expires)
		return response, nil
	}
	pending, err := s.Pairing.Start(peerID, request.PeerKey)
	if err != nil {
		return httpapi.PairResponse{}, err
	}
	return httpapi.PairResponse{ID: pending.ID, Code: pending.Code, Expires: pending.Expires}, nil
}

func (s *Service) PairConfirm(ctx context.Context, request httpapi.PairConfirmRequest) error {
	if err := s.requireLocal(ctx); err != nil {
		return err
	}
	if remote, ok := s.remotePair(request.ID); ok {
		if request.Code != remote.Code {
			return errors.New("service: pairing code mismatch")
		}
		peer, ok := s.Registry.Get(remote.TargetID)
		if !ok {
			return fmt.Errorf("service: unknown device %q", remote.TargetID)
		}
		s.Store.Trust.Add(trust.Record{
			Fingerprint: peer.Payload.ID,
			PublicKey:   identity.PublicKeyText(peer.Payload.PublicKey),
			PairedAt:    time.Now().UTC(),
		})
		if err := s.Store.SaveTrust(); err != nil {
			return err
		}
		s.forgetRemotePair(request.ID)
		return nil
	}
	pending, err := s.Pairing.Confirm(request.ID, request.Code)
	if err != nil {
		return err
	}
	s.Store.Trust.Add(trust.Record{
		Fingerprint: pending.PeerID,
		PublicKey:   pending.PeerKey,
		PairedAt:    time.Now().UTC(),
	})
	return s.Store.SaveTrust()
}

func (s *Service) rememberPeerTrust(targetID string) {
	if targetID == "" || targetID == s.Store.Identity.Fingerprint() || s.Store.Trust.Has(targetID) {
		return
	}
	peer, ok := s.Registry.Get(targetID)
	if !ok {
		return
	}
	s.Store.Trust.Add(trust.Record{
		Fingerprint: peer.Payload.ID,
		PublicKey:   identity.PublicKeyText(peer.Payload.PublicKey),
		Hostname:    peer.Payload.Hostname,
		Address:     peer.Address,
	})
	if err := s.Store.SaveTrust(); err != nil && s.Logger != nil {
		s.Logger.Warn("could not persist peer trust", "peer", targetID, "error", err)
	}
}

func (s *Service) rememberRemotePair(id, targetID, code string, expires time.Time) {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	if s.remotePairs == nil {
		s.remotePairs = make(map[string]remotePair)
	}
	s.remotePairs[id] = remotePair{TargetID: targetID, Code: code, Expires: expires}
}

func (s *Service) remotePair(id string) (remotePair, bool) {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	pair, ok := s.remotePairs[id]
	if !ok {
		return remotePair{}, false
	}
	if !pair.Expires.IsZero() && time.Now().After(pair.Expires) {
		delete(s.remotePairs, id)
		return remotePair{}, false
	}
	return pair, true
}

func (s *Service) forgetRemotePair(id string) {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	delete(s.remotePairs, id)
}

func (s *Service) ShareKey(ctx context.Context, request httpapi.KeyShareRequest) (pairing.Envelope, error) {
	if err := s.requireTrusted(ctx); err != nil {
		return pairing.Envelope{}, err
	}
	if request.PeerID == "" || request.PeerPublicKey == "" {
		return pairing.Envelope{}, errors.New("service: peer id and public key are required")
	}
	publicKey, err := base64.StdEncoding.DecodeString(request.PeerPublicKey)
	if err != nil {
		return pairing.Envelope{}, err
	}
	pem, err := s.Store.AdminKeyPEM()
	if err != nil {
		return pairing.Envelope{}, err
	}
	code := pairing.Code(s.Store.Identity.Fingerprint(), request.PeerID)
	return pairing.Seal(publicKey, pem, code)
}

func (s *Service) BuildISO(ctx context.Context, request httpapi.ISORequest) (string, error) {
	if err := s.requireLocal(ctx); err != nil {
		return "", err
	}
	request.OwnerUID, request.OwnerGID, request.OwnerKnown = httpapi.Owner(ctx)
	job, err := s.JobManager.Create(jobs.KindISO, s.Store.Identity.Fingerprint(), s.Store.Identity.Fingerprint())
	if err != nil {
		return "", err
	}
	go s.runJob(job.ID, func(ctx context.Context) error {
		if s.ISOFunc == nil {
			return errors.New("service: deploytarget ISO builder is not configured")
		}
		return s.ISOFunc(ctx, job.ID, request)
	})
	return job.ID, nil
}

func (s *Service) Publish(ctx context.Context, request httpapi.PublishRequest) (string, error) {
	if err := s.requireLocal(ctx); err != nil {
		return "", err
	}
	if err := s.authorize(ctx, s.Store.Config.Account, request.Password); err != nil {
		return "", err
	}
	job, err := s.JobManager.Create(jobs.KindPublish, s.Store.Identity.Fingerprint(), s.Store.Identity.Fingerprint())
	if err != nil {
		return "", err
	}
	go s.runJob(job.ID, func(ctx context.Context) error {
		if s.PublishFunc == nil {
			return errors.New("service: publish is not configured")
		}
		return s.PublishFunc(ctx, job.ID, request)
	})
	return job.ID, nil
}

func (s *Service) runJob(id string, function func(context.Context) error) {
	ctx := context.Background()
	if _, err := s.JobManager.SetState(id, jobs.StatePreparing); err != nil {
		return
	}
	if err := function(ctx); err != nil {
		_, _ = s.JobManager.Fail(id, err.Error())
		return
	}
	_, _ = s.JobManager.SetState(id, jobs.StateSucceeded)
}

func (s *Service) runner() deploy.Runner {
	if s.EngineRunner != nil {
		return s.EngineRunner
	}
	return deploy.ExecRunner{}
}

func (s *Service) remoteClient(targetID string) (*httpapi.Client, error) {
	peer, ok := s.Registry.Get(targetID)
	if !ok {
		return nil, fmt.Errorf("service: unknown device %q", targetID)
	}
	certificate, err := s.Store.Identity.TLSCertificate()
	if err != nil {
		return nil, err
	}
	expected := peer.Payload.ID
	config := &tls.Config{
		Certificates:       []tls.Certificate{certificate},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("service: target presented no certificate")
			}
			targetCertificate, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			public, ok := targetCertificate.PublicKey.(ed25519.PublicKey)
			if !ok {
				return errors.New("service: target certificate is not Ed25519")
			}
			if identity.Fingerprint(public) != expected {
				return errors.New("service: target identity does not match discovery")
			}
			return nil
		},
	}
	port := peer.Payload.ControlPort
	if port == 0 {
		port = 5061
	}
	return httpapi.NewRemote(net.JoinHostPort(peer.Address, strconv.Itoa(port)), config), nil
}

func (s *Service) remoteJobsPath() string {
	return filepath.Join(s.Store.Dir, "remote_jobs.json")
}

func (s *Service) initRemoteJobsLocked() {
	if s.remoteJobs != nil {
		return
	}
	s.remoteJobs = make(map[string]remoteJob)
	data, err := os.ReadFile(s.remoteJobsPath())
	if err == nil {
		_ = json.Unmarshal(data, &s.remoteJobs)
	}
}

func (s *Service) saveRemoteJobsLocked() {
	data, err := json.MarshalIndent(s.remoteJobs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.remoteJobsPath(), append(data, '\n'), 0o600)
}

func (s *Service) rememberRemoteJob(targetID, jobID string) {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	s.initRemoteJobsLocked()
	s.remoteJobs[jobID] = remoteJob{TargetID: targetID, JobID: jobID}
	s.saveRemoteJobsLocked()
}

func (s *Service) remoteJob(jobID string) (remoteJob, bool) {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	s.initRemoteJobsLocked()
	job, ok := s.remoteJobs[jobID]
	return job, ok
}

func (s *Service) remoteJobList() []remoteJob {
	s.remoteMutex.Lock()
	defer s.remoteMutex.Unlock()
	s.initRemoteJobsLocked()
	result := make([]remoteJob, 0, len(s.remoteJobs))
	for _, job := range s.remoteJobs {
		result = append(result, job)
	}
	return result
}

func (s *Service) startAutoConfirm(targetID, jobID string, client *httpapi.Client, mode string, timeout int, magic *bool) {
	if magic != nil && !*magic {
		return
	}
	go s.autoConfirm(targetID, jobID, client, mode, timeout)
}

func (s *Service) autoConfirm(targetID, jobID string, client *httpapi.Client, mode string, timeout int) {
	ctx := context.Background()
	wait := 5 * time.Minute
	if timeout > 0 {
		wait = time.Duration(timeout)*time.Second + 30*time.Second
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		job, err := client.Job(ctx, jobID)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		switch job.State {
		case jobs.StateAwaitingConfirmation:
			status, err := client.Status(ctx)
			if err != nil {
				time.Sleep(time.Second)
				continue
			}
			configured := mode
			if configured == "" {
				configured = status.MagicRollback.Mode
			}
			if configured != "automatic" {
				return
			}
			if err := s.checkTargetSSH(targetID, status); err != nil {
				time.Sleep(time.Second)
				continue
			}
			_ = client.Confirm(ctx, jobID)
			return
		case jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled, jobs.StateRolledBack:
			return
		}
		time.Sleep(time.Second)
	}
}

func (s *Service) checkTargetSSH(targetID string, status httpapi.Status) error {
	peer, ok := s.Registry.Get(targetID)
	if !ok {
		return fmt.Errorf("service: unknown device %q", targetID)
	}
	account := status.Registration.Account
	if account == "" {
		account = status.Registration.SSHUser
	}
	if account == "" {
		account = s.Store.Config.SSHUser
	}
	port := status.Registration.SSHPort
	if port == 0 {
		port = s.Store.Config.SSHPort
	}
	signer, err := s.Store.AdminSigner()
	if err != nil {
		return err
	}
	config := &ssh.ClientConfig{
		User:            account,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	connection, err := ssh.Dial("tcp", net.JoinHostPort(peer.Address, strconv.Itoa(port)), config)
	if err != nil {
		return err
	}
	defer connection.Close()
	session, err := connection.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	return session.Run("true")
}

func (s *Service) requireTrusted(ctx context.Context) error {
	peer := httpapi.Peer(ctx)
	if peer == "local" {
		return nil
	}
	if peer == "" {
		return errors.New("service: authenticated peer is required")
	}
	if !s.Store.Trust.Has(peer) {
		return errors.New("service: peer is not paired")
	}
	return nil
}

func (s *Service) requireLocal(ctx context.Context) error {
	if httpapi.Peer(ctx) != "local" {
		return errors.New("service: this action is local only")
	}
	return nil
}

func (s *Service) authorize(ctx context.Context, account, password string) error {
	if s.Gate == nil {
		return nil
	}
	ok, err := s.Gate.Authorize(ctx, account, password)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("service: consent denied")
	}
	return nil
}

func (s *Service) hostname() string {
	if s.Hostname != "" {
		return s.Hostname
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}

func shortID(fingerprint string) string {
	if len(fingerprint) <= 8 {
		return fingerprint
	}
	return fingerprint[:8]
}

func stateRegistration(repository, branch, host, sshUser string, sshPort int, account string) registration.Record {
	return registration.Record{
		Repository:     repository,
		Branch:         branch,
		Host:           host,
		InstallMode:    registration.ModePushed,
		UpdatesAllowed: true,
		SSHUser:        sshUser,
		SSHPort:        sshPort,
		Account:        account,
		UpdatedAt:      time.Now().UTC(),
	}
}
