package service

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/consent"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/info"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/registration"
	"github.com/ojuny-project/nix-connect/internal/registry"
	"github.com/ojuny-project/nix-connect/internal/state"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type allowRunner struct{}

func (allowRunner) Run(context.Context, string, []string, io.Reader) (consent.Result, error) {
	return consent.Result{ExitCode: 0}, nil
}

type denyRunner struct{}

func (denyRunner) Run(context.Context, string, []string, io.Reader) (consent.Result, error) {
	return consent.Result{ExitCode: 1}, nil
}

type errorRunner struct{}

func (errorRunner) Run(_ context.Context, name string, args []string, _ io.Reader) (consent.Result, error) {
	if name == "id" {
		return consent.Result{ExitCode: 0, Output: "harness wheel\n"}, nil
	}
	if name == "pamtester" {
		return consent.Result{}, errors.New("pam broken")
	}
	return consent.Result{ExitCode: 1}, nil
}

func newTestService(t *testing.T) (*Service, *state.Store) {
	t.Helper()
	directory := t.TempDir()
	store, err := state.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	store.Config.Account = "harness"
	store.Config.SSHUser = "harness"
	store.Config.SSHPort = 2222
	store.Config.InfoPort = 15060
	store.Config.ControlPort = 15061
	store.Config.Kind = "installed"
	store.Config.MagicRollback = state.MagicRollbackConfig{
		Enable:           true,
		Mode:             "automatic",
		AutomaticTimeout: 60,
		ManualTimeout:    600,
	}
	manager, err := jobs.New(filepath.Join(directory, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		Store:       store,
		JobManager:  manager,
		Registry:    registryForTest(),
		Pairing:     pairing.New(store.Identity.Fingerprint()),
		Gate:        &consent.Gate{Runner: allowRunner{}},
		AgentSocket: filepath.Join(directory, "agent.sock"),
		Hostname:    "test-host",
		Version:     "0.1.0",
	}
	return service, store
}

func registryForTest() *registry.Registry {
	return registry.New()
}

func localContext() context.Context {
	return httpapi.WithPeer(context.Background(), "local")
}

func peerContext(fingerprint string) context.Context {
	return httpapi.WithPeer(context.Background(), fingerprint)
}

func trustPeer(store *state.Store, fingerprint string) {
	store.Trust.Add(trust.Record{Fingerprint: fingerprint})
}

func waitForJobState(t *testing.T, manager *jobs.Manager, id string, state jobs.State) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := manager.Get(id)
		if ok && job.State == state {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := manager.Get(id)
	t.Fatalf("job %s did not reach %s: %+v", id, state, job)
	return jobs.Job{}
}

func canaryFile(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "canary")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStatusAndRegistrationRedaction(t *testing.T) {
	service, store := newTestService(t)
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "rev")
	service.Registry.Upsert(info.Payload{ID: "peer", Hostname: "peer"}, "10.0.0.2")

	status := service.Status(localContext())
	if status.ID != store.Identity.Fingerprint() {
		t.Fatalf("unexpected id %q", status.ID)
	}
	if status.ShortID != store.Identity.ShortID() {
		t.Fatalf("unexpected short id %q", status.ShortID)
	}
	if status.Hostname != "test-host" {
		t.Fatalf("unexpected hostname %q", status.Hostname)
	}
	if status.InfoPort != 15060 || status.ControlPort != 15061 {
		t.Fatalf("unexpected ports %+v", status)
	}
	if status.Peers != 1 {
		t.Fatalf("unexpected peer count %d", status.Peers)
	}
	if status.Registration.Repository != "https://example.invalid/flake" {
		t.Fatalf("registration missing: %+v", status.Registration)
	}
	if !status.MagicRollback.Enable || status.MagicRollback.Mode != "automatic" {
		t.Fatalf("unexpected magic rollback: %+v", status.MagicRollback)
	}

	unpaired := service.Status(peerContext("unpaired"))
	if unpaired.Registration.Registered() {
		t.Fatal("unpaired caller received the registration")
	}
	empty := service.Status(context.Background())
	if empty.Registration.Registered() {
		t.Fatal("empty caller received the registration")
	}

	trustPeer(store, "peer")
	paired := service.Status(peerContext("peer"))
	if paired.Registration.Repository != "https://example.invalid/flake" {
		t.Fatal("paired caller did not receive the registration")
	}

	service.Hostname = ""
	if service.Status(localContext()).Hostname == "" {
		t.Fatal("hostname fallback was empty")
	}
}

func TestDevices(t *testing.T) {
	service, store := newTestService(t)
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	service.Registry.Upsert(info.Payload{
		ID:         "peer-fingerprint",
		Hostname:   "peer",
		Kind:       "live",
		Job:        "installing",
		SSHPort:    2200,
		Registered: true,
	}, "10.0.0.2")
	trustPeer(store, "peer-fingerprint")

	devices := service.Devices(localContext())
	if len(devices) != 2 {
		t.Fatalf("unexpected device count %d", len(devices))
	}
	self := devices[0]
	if !self.Self || !self.Paired || self.Kind != "installed" || self.SSHPort != 2222 || !self.Registered {
		t.Fatalf("unexpected self device: %+v", self)
	}
	peer := devices[1]
	if peer.ID != "peer-fingerprint" || peer.ShortID != "peer-fin" {
		t.Fatalf("unexpected peer id: %+v", peer)
	}
	if peer.Address != "10.0.0.2" || peer.Kind != "live" || peer.Job != "installing" || peer.SSHPort != 2200 || !peer.Paired {
		t.Fatalf("unexpected peer: %+v", peer)
	}

	untrusted := service.Devices(peerContext("unpaired"))
	if len(untrusted) != 2 {
		t.Fatalf("device list was hidden from an untrusted caller: %d", len(untrusted))
	}
	empty := service.Devices(context.Background())
	if len(empty) != 2 {
		t.Fatalf("device list was hidden from an empty caller: %d", len(empty))
	}
}

func TestRegister(t *testing.T) {
	service, store := newTestService(t)
	request := httpapi.RegisterRequest{
		Repository: "https://example.invalid/flake",
		Branch:     "main",
		Host:       "host",
	}
	if err := service.Register(localContext(), store.Identity.Fingerprint(), request); err != nil {
		t.Fatal(err)
	}
	record := store.Registration
	if record.Repository != request.Repository || record.Branch != "main" || record.Host != "host" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if record.Account != "harness" || record.SSHUser != "harness" || record.SSHPort != 2222 {
		t.Fatalf("defaults were not applied: %+v", record)
	}
	if record.InstallMode != registration.ModePushed || !record.UpdatesAllowed {
		t.Fatalf("register did not enable updates: %+v", record)
	}
	if record.UpdatedAt.IsZero() || record.LastRevision != "" {
		t.Fatalf("unexpected timestamps: %+v", record)
	}
	if record.Reason != "" {
		t.Fatalf("unexpected reason %q", record.Reason)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, state.RegistrationFile)); err != nil {
		t.Fatal(err)
	}

	if err := service.Register(localContext(), "unknown", request); err == nil || err.Error() != `service: unknown device "unknown"` {
		t.Fatalf("unknown registration target was accepted: %v", err)
	}

	untrusted := service.Register(peerContext("unpaired"), store.Identity.Fingerprint(), request)
	if untrusted == nil || untrusted.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected untrusted error: %v", untrusted)
	}

	service.Gate = &consent.Gate{Runner: denyRunner{}}
	if err := service.Register(localContext(), store.Identity.Fingerprint(), request); err == nil || err.Error() != "service: consent denied" {
		t.Fatalf("unexpected consent error: %v", err)
	}

	service.Gate = &consent.Gate{Runner: allowRunner{}}
	for _, missing := range []httpapi.RegisterRequest{
		{Branch: "main", Host: "host"},
		{Repository: "repo", Host: "host"},
		{Repository: "repo", Branch: "main"},
	} {
		if err := service.Register(localContext(), store.Identity.Fingerprint(), missing); err == nil || err.Error() != "service: repository, branch and host are required" {
			t.Fatalf("unexpected validation error: %v", err)
		}
	}

	if err := service.Register(localContext(), store.Identity.Fingerprint(), httpapi.RegisterRequest{
		Repository: "https://example.invalid/other",
		Branch:     "dev",
		Host:       "other",
		Account:    "alice",
		SSHUser:    "alice",
		SSHPort:    2200,
	}); err != nil {
		t.Fatal(err)
	}
	if store.Registration.Repository != "https://example.invalid/other" || store.Registration.Account != "alice" {
		t.Fatalf("second registration did not replace: %+v", store.Registration)
	}
}

func TestUnregister(t *testing.T) {
	service, store := newTestService(t)
	if err := service.Register(localContext(), store.Identity.Fingerprint(), httpapi.RegisterRequest{
		Repository: "https://example.invalid/flake",
		Branch:     "main",
		Host:       "host",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Unregister(localContext(), store.Identity.Fingerprint(), ""); err != nil {
		t.Fatal(err)
	}
	if store.Registration.Registered() {
		t.Fatalf("registration was not cleared: %+v", store.Registration)
	}
	if err := service.Unregister(localContext(), store.Identity.Fingerprint(), ""); err != nil {
		t.Fatalf("second unregister failed: %v", err)
	}

	service.Gate = &consent.Gate{Runner: denyRunner{}}
	if err := service.Unregister(localContext(), store.Identity.Fingerprint(), ""); err == nil || err.Error() != "service: consent denied" {
		t.Fatalf("unexpected consent error: %v", err)
	}

	service.Gate = &consent.Gate{Runner: allowRunner{}}
	if err := service.Unregister(peerContext("unpaired"), store.Identity.Fingerprint(), ""); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
}

func TestDeployJob(t *testing.T) {
	service, _ := newTestService(t)
	target := service.Store.Identity.Fingerprint()
	jobID, err := service.Deploy(localContext(), target, httpapi.DeployRequest{})
	if err != nil {
		t.Fatal(err)
	}
	job := waitForJobState(t, service.JobManager, jobID, jobs.StateFailed)
	if job.Kind != jobs.KindDeploy || job.Owner != target || job.Target != target {
		t.Fatalf("unexpected job: %+v", job)
	}
	if job.Error != "service: deployment engine is not configured" {
		t.Fatalf("unexpected error %q", job.Error)
	}

	service.Gate = &consent.Gate{Runner: denyRunner{}}
	if _, err := service.Deploy(localContext(), target, httpapi.DeployRequest{}); err == nil {
		t.Fatal("consent failure was accepted")
	}
	if len(service.JobManager.List()) != 1 {
		t.Fatal("consent failure created a job")
	}

	service.Gate = &consent.Gate{Runner: allowRunner{}}
	if _, err := service.Deploy(peerContext("unpaired"), target, httpapi.DeployRequest{}); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
}

func TestUpdateJobAndCallback(t *testing.T) {
	service, _ := newTestService(t)
	target := service.Store.Identity.Fingerprint()
	requests := make(chan httpapi.UpdateRequest, 1)
	service.UpdateFunc = func(ctx context.Context, jobID, targetID string, request httpapi.UpdateRequest) error {
		requests <- request
		return nil
	}
	magic := true
	jobID, err := service.Update(localContext(), target, httpapi.UpdateRequest{
		Mode:          "manual",
		MagicRollback: &magic,
		Timeout:       123,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if request.Mode != "manual" || request.MagicRollback == nil || !*request.MagicRollback || request.Timeout != 123 {
			t.Fatalf("unexpected update request: %+v", request)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("update callback was not called")
	}
	job := waitForJobState(t, service.JobManager, jobID, jobs.StateSucceeded)
	if job.Kind != jobs.KindUpdate || job.Target != target {
		t.Fatalf("unexpected job: %+v", job)
	}

	service.UpdateFunc = func(context.Context, string, string, httpapi.UpdateRequest) error {
		return errors.New("update failed")
	}
	failedID, err := service.Update(localContext(), target, httpapi.UpdateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForJobState(t, service.JobManager, failedID, jobs.StateFailed)
	if failed.Error != "update failed" {
		t.Fatalf("unexpected error %q", failed.Error)
	}

	if len(service.JobManager.List()) != 2 {
		t.Fatalf("unexpected job count %d", len(service.JobManager.List()))
	}
}

func TestSSH(t *testing.T) {
	service, store := newTestService(t)
	local, err := service.SSH(localContext(), store.Identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if local.Host != "127.0.0.1" || local.User != "harness" || local.Port != 2222 || local.AgentSocket == "" {
		t.Fatalf("unexpected local SSH info: %+v", local)
	}

	service.Registry.Upsert(info.Payload{ID: "peer", Hostname: "peer", SSHPort: 2200}, "10.0.0.2")
	peer, err := service.SSH(localContext(), "peer")
	if err != nil {
		t.Fatal(err)
	}
	if peer.Host != "10.0.0.2" || peer.Port != 2200 || peer.User != "harness" {
		t.Fatalf("unexpected peer SSH info: %+v", peer)
	}

	service.Registry.Upsert(info.Payload{ID: "default-port", Hostname: "peer"}, "10.0.0.3")
	defaultPort, err := service.SSH(localContext(), "default-port")
	if err != nil {
		t.Fatal(err)
	}
	if defaultPort.Port != 2222 {
		t.Fatalf("unexpected fallback port %d", defaultPort.Port)
	}

	if _, err := service.SSH(localContext(), "missing"); err == nil || err.Error() != `service: unknown device "missing"` {
		t.Fatalf("unexpected unknown error: %v", err)
	}
	if _, err := service.SSH(peerContext("unpaired"), "peer"); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
}

func TestJobsAndJobTrust(t *testing.T) {
	service, _ := newTestService(t)
	job, err := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(service.Jobs(peerContext("unpaired"))) != 0 {
		t.Fatal("untrusted caller received jobs")
	}
	if len(service.Jobs(context.Background())) != 0 {
		t.Fatal("empty caller received jobs")
	}
	if len(service.Jobs(localContext())) != 1 {
		t.Fatal("local caller did not receive jobs")
	}
	got, err := service.Job(localContext(), job.ID)
	if err != nil || got.ID != job.ID {
		t.Fatalf("unexpected job: %+v %v", got, err)
	}
	if _, err := service.Job(localContext(), "missing"); err == nil || err.Error() != `service: unknown job "missing"` {
		t.Fatalf("unexpected unknown error: %v", err)
	}
	if _, err := service.Job(peerContext("unpaired"), job.ID); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
}

func TestConfirm(t *testing.T) {
	service, _ := newTestService(t)
	job, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	if err := service.Confirm(localContext(), job.ID, ""); err == nil || err.Error() != "service: confirmation requires a paired administration device" {
		t.Fatalf("unexpected empty confirmer error: %v", err)
	}
	if err := service.Confirm(peerContext("unpaired"), job.ID, "admin"); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
	if err := service.Confirm(localContext(), job.ID, "admin"); err == nil || err.Error() != "service: job is not awaiting confirmation" {
		t.Fatalf("unexpected state error: %v", err)
	}

	pastCanary := canaryFile(t, t.TempDir())
	if _, err := service.JobManager.SetCanary(job.ID, pastCanary, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := service.Confirm(localContext(), job.ID, "admin"); err == nil || err.Error() != "service: confirmation deadline has passed" {
		t.Fatalf("unexpected deadline error: %v", err)
	}
	if _, err := os.Stat(pastCanary); err != nil {
		t.Fatal("deadline failure removed the canary")
	}

	futureCanary := canaryFile(t, t.TempDir())
	if _, err := service.JobManager.SetCanary(job.ID, futureCanary, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.Confirm(localContext(), job.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(futureCanary); !os.IsNotExist(err) {
		t.Fatal("canary was not removed")
	}
	confirmed, _ := service.JobManager.Get(job.ID)
	if confirmed.State != jobs.StateSucceeded || confirmed.Confirmer != "admin" {
		t.Fatalf("unexpected confirmed job: %+v", confirmed)
	}

	missingJob, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	if _, err := service.JobManager.SetCanary(missingJob.ID, filepath.Join(t.TempDir(), "missing"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.Confirm(localContext(), missingJob.ID, "admin"); err != nil {
		t.Fatalf("missing canary was not tolerated: %v", err)
	}

	blockedJob, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	blockedPath := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blockedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.JobManager.SetCanary(blockedJob.ID, blockedPath, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := service.Confirm(localContext(), blockedJob.ID, "admin"); err == nil {
		t.Fatal("unremovable canary was confirmed")
	}
	blocked, _ := service.JobManager.Get(blockedJob.ID)
	if blocked.State != jobs.StateAwaitingConfirmation {
		t.Fatalf("blocked job changed state: %+v", blocked)
	}

	callbackJob, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	callbackCanary := canaryFile(t, t.TempDir())
	if _, err := service.JobManager.SetCanary(callbackJob.ID, callbackCanary, time.Time{}); err != nil {
		t.Fatal(err)
	}
	called := make(chan jobs.Job, 1)
	service.ConfirmFunc = func(ctx context.Context, job jobs.Job) error {
		called <- job
		return nil
	}
	if err := service.Confirm(localContext(), callbackJob.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("confirm callback was not called")
	}
	callback, _ := service.JobManager.Get(callbackJob.ID)
	if callback.State != jobs.StateConfirming || callback.Confirmer != "admin" {
		t.Fatalf("unexpected callback job: %+v", callback)
	}
}

func TestCancel(t *testing.T) {
	service, _ := newTestService(t)
	job, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	if err := service.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := service.JobManager.Get(job.ID)
	if cancelled.State != jobs.StateCancelled {
		t.Fatalf("unexpected state %q", cancelled.State)
	}

	succeeded, _ := service.JobManager.Create(jobs.KindUpdate, "owner", "target")
	if _, err := service.JobManager.SetState(succeeded.ID, jobs.StateSucceeded); err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(context.Background(), succeeded.ID); err != nil {
		t.Fatal(err)
	}
	stillSucceeded, _ := service.JobManager.Get(succeeded.ID)
	if stillSucceeded.State != jobs.StateSucceeded {
		t.Fatalf("succeeded job changed state: %q", stillSucceeded.State)
	}

	if err := service.Cancel(context.Background(), "missing"); err == nil || err.Error() != `jobs: unknown job "missing"` {
		t.Fatalf("unexpected unknown error: %v", err)
	}
}

func TestPairing(t *testing.T) {
	service, store := newTestService(t)
	if _, err := service.PairStart(localContext(), httpapi.PairRequest{}); err == nil || err.Error() != "service: peer id is required" {
		t.Fatalf("unexpected empty peer error: %v", err)
	}
	if _, err := service.PairStart(peerContext("other"), httpapi.PairRequest{PeerID: "peer"}); err == nil || err.Error() != "service: pairing identity does not match the request" {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
	if len(service.Pairing.Pending()) != 0 {
		t.Fatal("refused caller created a pending request")
	}
	if pending := service.PairPending(localContext()); len(pending) != 0 {
		t.Fatalf("unexpected pending requests: %+v", pending)
	}
	if _, err := service.PairStart(localContext(), httpapi.PairRequest{PeerID: "peer", PeerKey: "key"}); err == nil || err.Error() != `service: unknown device "peer"` {
		t.Fatalf("local start did not forward to the peer: %v", err)
	}

	response, err := service.PairStart(peerContext("peer"), httpapi.PairRequest{PeerID: "peer", PeerKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Code) != 6 || response.ID == "" || response.Expires.IsZero() {
		t.Fatalf("unexpected pairing response: %+v", response)
	}
	if pending := service.PairPending(localContext()); len(pending) != 1 || pending[0].ID != response.ID {
		t.Fatalf("pending pairing was not listed: %+v", pending)
	}
	if pending := service.PairPending(peerContext("peer")); pending != nil {
		t.Fatalf("remote caller listed pending pairings: %+v", pending)
	}
	if err := service.PairConfirm(peerContext("other"), httpapi.PairConfirmRequest{ID: response.ID, Code: response.Code}); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected confirm mismatch: %v", err)
	}
	if store.Trust.Has("peer") {
		t.Fatal("refused confirm wrote a trust record")
	}

	second, err := service.PairStart(peerContext("peer"), httpapi.PairRequest{PeerID: "peer", PeerKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.PairConfirm(localContext(), httpapi.PairConfirmRequest{ID: second.ID, Code: second.Code}); err != nil {
		t.Fatal(err)
	}
	record, ok := store.Trust.Get("peer")
	if !ok || record.Fingerprint != "peer" || record.PublicKey != "key" || record.PairedAt.IsZero() {
		t.Fatalf("unexpected trust record: %+v", record)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, state.TrustFile)); err != nil {
		t.Fatal(err)
	}
	if err := service.PairConfirm(localContext(), httpapi.PairConfirmRequest{ID: second.ID, Code: second.Code}); err == nil {
		t.Fatal("second confirm was accepted")
	}
}

func TestRemotePairTracking(t *testing.T) {
	service, _ := newTestService(t)
	service.rememberRemotePair("id", "peer", "123456", time.Now().Add(time.Minute))
	pair, ok := service.remotePair("id")
	if !ok || pair.TargetID != "peer" || pair.Code != "123456" {
		t.Fatalf("unexpected remote pair: %+v %t", pair, ok)
	}
	service.forgetRemotePair("id")
	if _, ok := service.remotePair("id"); ok {
		t.Fatal("remote pair was not forgotten")
	}
	service.rememberRemotePair("expired", "peer", "000000", time.Now().Add(-time.Second))
	if _, ok := service.remotePair("expired"); ok {
		t.Fatal("expired remote pair was returned")
	}
	if _, ok := service.remotePair("expired"); ok {
		t.Fatal("expired remote pair was not removed")
	}
}

func TestRotateAdminKey(t *testing.T) {
	service, store := newTestService(t)
	before, err := store.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RotateAdminKey(peerContext("unpaired")); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected remote rotate error: %v", err)
	}
	var rotated ed25519.PrivateKey
	service.RotateAgent = func(key ed25519.PrivateKey) error {
		rotated = key
		return nil
	}
	after, err := service.RotateAdminKey(localContext())
	if err != nil {
		t.Fatal(err)
	}
	if after == before || after == "" || rotated == nil {
		t.Fatalf("key was not rotated: %q %q %v", before, after, rotated)
	}
	stored, err := store.AdminAuthorizedKey()
	if err != nil || stored != after {
		t.Fatalf("stored key does not match the response: %q %v", stored, err)
	}
}

func TestShareKey(t *testing.T) {
	service, store := newTestService(t)
	peerID := "peer"
	private, public, err := pairing.NewEphemeral()
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := service.ShareKey(localContext(), httpapi.KeyShareRequest{
		PeerID:        peerID,
		PeerPublicKey: public,
	})
	if err != nil {
		t.Fatal(err)
	}
	code := pairing.Code(store.Identity.Fingerprint(), peerID)
	opened, err := pairing.Open(private, envelope, code)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := store.AdminKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != string(expected) {
		t.Fatal("sealed key did not open to the administration key")
	}
	if _, err := pairing.Open(private, envelope, "000000"); err == nil {
		t.Fatal("wrong code opened the envelope")
	}
	if _, err := service.ShareKey(localContext(), httpapi.KeyShareRequest{}); err == nil || err.Error() != "service: peer id and public key are required" {
		t.Fatalf("unexpected missing field error: %v", err)
	}
	if _, err := service.ShareKey(localContext(), httpapi.KeyShareRequest{PeerID: "peer", PeerPublicKey: "!!!"}); err == nil {
		t.Fatal("invalid base64 was accepted")
	}
	if _, err := service.ShareKey(peerContext("unpaired"), httpapi.KeyShareRequest{PeerID: "peer", PeerPublicKey: public}); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected trust error: %v", err)
	}
}

func TestBuildISO(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.BuildISO(peerContext("unpaired"), httpapi.ISORequest{}); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected local-only error: %v", err)
	}
	jobID, err := service.BuildISO(localContext(), httpapi.ISORequest{})
	if err != nil {
		t.Fatal(err)
	}
	job := waitForJobState(t, service.JobManager, jobID, jobs.StateFailed)
	if job.Kind != jobs.KindISO || job.Error != "service: deploytarget ISO builder is not configured" {
		t.Fatalf("unexpected ISO job: %+v", job)
	}

	requests := make(chan httpapi.ISORequest, 1)
	service.ISOFunc = func(ctx context.Context, jobID string, request httpapi.ISORequest) error {
		requests <- request
		return nil
	}
	successID, err := service.BuildISO(localContext(), httpapi.ISORequest{Output: "/tmp/deploytarget.iso"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if request.Output != "/tmp/deploytarget.iso" {
			t.Fatalf("unexpected ISO request: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("ISO callback was not called")
	}
	waitForJobState(t, service.JobManager, successID, jobs.StateSucceeded)
}

func TestPublish(t *testing.T) {
	service, store := newTestService(t)
	if _, err := service.Publish(peerContext("unpaired"), httpapi.PublishRequest{}); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected local-only error: %v", err)
	}
	service.Gate = &consent.Gate{Runner: denyRunner{}}
	if _, err := service.Publish(localContext(), httpapi.PublishRequest{}); err == nil || err.Error() != "service: consent denied" {
		t.Fatalf("unexpected consent error: %v", err)
	}
	if len(service.JobManager.List()) != 0 {
		t.Fatal("consent failure created a publish job")
	}

	service.Gate = &consent.Gate{Runner: allowRunner{}}
	jobID, err := service.Publish(localContext(), httpapi.PublishRequest{})
	if err != nil {
		t.Fatal(err)
	}
	job := waitForJobState(t, service.JobManager, jobID, jobs.StateFailed)
	if job.Kind != jobs.KindPublish || job.Error != "service: publish is not configured" {
		t.Fatalf("unexpected publish job: %+v", job)
	}
	if store.Registration.Registered() {
		t.Fatal("failed publish changed the registration")
	}

	requests := make(chan httpapi.PublishRequest, 1)
	service.PublishFunc = func(ctx context.Context, jobID string, request httpapi.PublishRequest) error {
		requests <- request
		return nil
	}
	successID, err := service.Publish(localContext(), httpapi.PublishRequest{RemoteURL: "https://example.invalid/new", Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if request.RemoteURL != "https://example.invalid/new" || request.Branch != "main" {
			t.Fatalf("unexpected publish request: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("publish callback was not called")
	}
	waitForJobState(t, service.JobManager, successID, jobs.StateSucceeded)
}

func TestResetRegistration(t *testing.T) {
	service, store := newTestService(t)
	if err := service.ResetRegistration(localContext()); err == nil || err.Error() != "service: device is not registered" {
		t.Fatalf("unexpected empty reset error: %v", err)
	}
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	if err := service.ResetRegistration(peerContext("unpaired")); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected local-only error: %v", err)
	}
	if err := service.ResetRegistration(localContext()); err != nil {
		t.Fatal(err)
	}
	if store.Registration.InstallMode != registration.ModeNoCommit || store.Registration.UpdatesAllowed || store.Registration.Reason != "no-commit" {
		t.Fatalf("unexpected reset registration: %+v", store.Registration)
	}
}

func TestTrustRecordsAndRevoke(t *testing.T) {
	service, store := newTestService(t)
	store.Trust.Add(trust.Record{Fingerprint: "peer"})
	if len(service.TrustRecords(localContext())) != 1 {
		t.Fatal("local caller did not receive trust records")
	}
	if len(service.TrustRecords(peerContext("unpaired"))) != 0 {
		t.Fatal("unpaired caller received trust records")
	}
	if err := service.RevokeTrust(localContext(), ""); err == nil || err.Error() != "service: fingerprint is required" {
		t.Fatalf("unexpected empty revoke error: %v", err)
	}
	if err := service.RevokeTrust(localContext(), "peer"); err != nil {
		t.Fatal(err)
	}
	if store.Trust.Has("peer") {
		t.Fatal("trust record was not revoked")
	}
}

func TestTrustAndLocalHelpers(t *testing.T) {
	service, store := newTestService(t)
	if err := service.requireTrusted(context.Background()); err == nil || err.Error() != "service: authenticated peer is required" {
		t.Fatalf("unexpected empty peer error: %v", err)
	}
	if err := service.requireTrusted(peerContext("unpaired")); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected unpaired error: %v", err)
	}
	if err := service.requireTrusted(localContext()); err != nil {
		t.Fatalf("local caller was refused: %v", err)
	}
	trustPeer(store, "paired")
	if err := service.requireTrusted(peerContext("paired")); err != nil {
		t.Fatalf("paired caller was refused: %v", err)
	}

	if err := service.requireLocal(context.Background()); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected local error: %v", err)
	}
	if err := service.requireLocal(peerContext("paired")); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected local error: %v", err)
	}
	if err := service.requireLocal(localContext()); err != nil {
		t.Fatalf("local caller was refused: %v", err)
	}

	service.Gate = nil
	if err := service.authorize(localContext(), "harness", ""); err != nil {
		t.Fatalf("nil gate refused the action: %v", err)
	}
	service.Gate = &consent.Gate{Runner: denyRunner{}}
	if err := service.authorize(localContext(), "harness", ""); err == nil || err.Error() != "service: consent denied" {
		t.Fatalf("unexpected consent error: %v", err)
	}
	service.Gate = &consent.Gate{Runner: errorRunner{}}
	if err := service.authorize(localContext(), "harness", "password"); err == nil || err.Error() != "pam broken" {
		t.Fatalf("unexpected PAM error: %v", err)
	}
}
