package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type fakeService struct {
	status     Status
	devices    []Device
	jobs       []jobs.Job
	confirmer  string
	ownerUID   int
	ownerGID   int
	ownerKnown bool
}

func (f *fakeService) Status(ctx context.Context) Status {
	return f.status
}

func (f *fakeService) Devices(ctx context.Context) []Device {
	return f.devices
}

func (f *fakeService) Register(ctx context.Context, id string, request RegisterRequest) error {
	return nil
}

func (f *fakeService) Unregister(ctx context.Context, id string, password string) error {
	return nil
}

func (f *fakeService) Deploy(ctx context.Context, id string, request DeployRequest) (string, error) {
	return "job", nil
}

func (f *fakeService) Update(ctx context.Context, id string, request UpdateRequest) (string, error) {
	return "job", nil
}

func (f *fakeService) SSH(ctx context.Context, id string) (SSHInfo, error) {
	return SSHInfo{Host: "10.0.0.1", User: "nixos", Port: 22}, nil
}

func (f *fakeService) Target(ctx context.Context, id string) (TargetInfo, error) {
	return TargetInfo{Firmware: "efi", Disks: []Disk{{Path: "/dev/nvme0n1", Type: "disk"}}}, nil
}

func (f *fakeService) Jobs(ctx context.Context) []jobs.Job {
	return f.jobs
}

func (f *fakeService) Job(ctx context.Context, id string) (jobs.Job, error) {
	for _, job := range f.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return jobs.Job{}, context.Canceled
}

func (f *fakeService) Confirm(ctx context.Context, id, confirmer string) error {
	f.confirmer = confirmer
	return nil
}

func (f *fakeService) Cancel(ctx context.Context, id string) error {
	return nil
}

func (f *fakeService) PairStart(ctx context.Context, request PairRequest) (PairResponse, error) {
	return PairResponse{ID: "pair", Code: "123456"}, nil
}

func (f *fakeService) PairConfirm(ctx context.Context, request PairConfirmRequest) error {
	return nil
}

func (f *fakeService) PairPending(ctx context.Context) []pairing.Request {
	return []pairing.Request{{ID: "pair", PeerID: "peer", Code: "123456"}}
}

func (f *fakeService) ShareKey(ctx context.Context, request KeyShareRequest) (pairing.Envelope, error) {
	return pairing.Envelope{Version: 1}, nil
}

func (f *fakeService) ImportSharedKey(ctx context.Context, peerID string) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (f *fakeService) ResetRegistration(ctx context.Context) error {
	return nil
}

func (f *fakeService) TrustRecords(ctx context.Context) []trust.Record {
	return nil
}

func (f *fakeService) RevokeTrust(ctx context.Context, fingerprint string) error {
	return nil
}

func (f *fakeService) RotateAdminKey(ctx context.Context) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (f *fakeService) BuildISO(ctx context.Context, request ISORequest) (string, error) {
	f.ownerUID, f.ownerGID, f.ownerKnown = Owner(ctx)
	return "job", nil
}

func (f *fakeService) Publish(ctx context.Context, request PublishRequest) (string, error) {
	return "job", nil
}

func TestStatusEndpoint(t *testing.T) {
	service := &fakeService{
		status: Status{ID: "id", ShortID: "short", Hostname: "host", InfoPort: 5060, ControlPort: 5061},
	}
	server := NewServer(service, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}

func TestConfirmEndpoint(t *testing.T) {
	service := &fakeService{
		jobs: []jobs.Job{{ID: "job", State: jobs.StateAwaitingConfirmation}},
	}
	server := NewServer(service, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/jobs/job/confirm", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.confirmer != "local" {
		t.Fatalf("unexpected confirmer %q", service.confirmer)
	}
}

func TestUnknownJobReturnsNotFound(t *testing.T) {
	server := NewServer(&fakeService{}, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/missing", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}

func TestLocalTransport(t *testing.T) {
	service := &fakeService{
		status: Status{ID: "id", ShortID: "short", Hostname: "host"},
	}
	server := NewServer(service, nil)
	socket := filepath.Join(t.TempDir(), "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = server.ServeLocal(ctx, socket)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not appear")
		}
		time.Sleep(10 * time.Millisecond)
	}
	client := NewLocal(socket)
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Hostname != "host" {
		t.Fatalf("unexpected status %+v", status)
	}
	if _, err := client.BuildISO(context.Background(), ISORequest{Output: "/tmp/deploy-target-iso"}); err != nil {
		t.Fatal(err)
	}
	if !service.ownerKnown {
		t.Fatal("local owner was not recorded")
	}
	if service.ownerUID != os.Getuid() || service.ownerGID != os.Getgid() {
		t.Fatalf("unexpected owner %d:%d", service.ownerUID, service.ownerGID)
	}
}
