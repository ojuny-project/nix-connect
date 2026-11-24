package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type recordingService struct {
	status       Status
	devices      []Device
	jobs         []jobs.Job
	register     RegisterRequest
	unregister   string
	deploy       DeployRequest
	update       UpdateRequest
	confirmID    string
	confirmer    string
	cancelID     string
	pairStart    PairRequest
	pairConfirm  PairConfirmRequest
	keyShare     KeyShareRequest
	iso          ISORequest
	publish      PublishRequest
	failRegister bool
}

func (s *recordingService) Status(context.Context) Status {
	return s.status
}

func (s *recordingService) Devices(context.Context) []Device {
	return s.devices
}

func (s *recordingService) Register(_ context.Context, _ string, request RegisterRequest) error {
	s.register = request
	if s.failRegister {
		return errors.New("register failed")
	}
	return nil
}

func (s *recordingService) Unregister(_ context.Context, _ string, password string) error {
	s.unregister = password
	return nil
}

func (s *recordingService) Deploy(_ context.Context, _ string, request DeployRequest) (string, error) {
	s.deploy = request
	return "deploy-job", nil
}

func (s *recordingService) Update(_ context.Context, _ string, request UpdateRequest) (string, error) {
	s.update = request
	return "update-job", nil
}

func (s *recordingService) SSH(context.Context, string) (SSHInfo, error) {
	return SSHInfo{Host: "10.0.0.2", User: "harness", Port: 22, AgentSocket: "/tmp/agent.sock"}, nil
}

func (s *recordingService) Jobs(context.Context) []jobs.Job {
	return s.jobs
}

func (s *recordingService) Job(_ context.Context, id string) (jobs.Job, error) {
	for _, job := range s.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return jobs.Job{}, errors.New("unknown job")
}

func (s *recordingService) Confirm(_ context.Context, id, confirmer string) error {
	s.confirmID = id
	s.confirmer = confirmer
	return nil
}

func (s *recordingService) Cancel(_ context.Context, id string) error {
	s.cancelID = id
	return nil
}

func (s *recordingService) PairStart(_ context.Context, request PairRequest) (PairResponse, error) {
	s.pairStart = request
	return PairResponse{ID: "pair", Code: "123456", Expires: time.Unix(100, 0)}, nil
}

func (s *recordingService) PairConfirm(_ context.Context, request PairConfirmRequest) error {
	s.pairConfirm = request
	return nil
}

func (s *recordingService) PairPending(context.Context) []pairing.Request {
	return []pairing.Request{{ID: "pair", PeerID: "peer", Code: "123456"}}
}

func (s *recordingService) ShareKey(_ context.Context, request KeyShareRequest) (pairing.Envelope, error) {
	s.keyShare = request
	return pairing.Envelope{Version: 1, Ephemeral: "e", Nonce: "n", Ciphertext: "c"}, nil
}

func (s *recordingService) ImportSharedKey(context.Context, string) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (s *recordingService) ResetRegistration(context.Context) error {
	return nil
}

func (s *recordingService) TrustRecords(context.Context) []trust.Record {
	return []trust.Record{{Fingerprint: "peer"}}
}

func (s *recordingService) RevokeTrust(context.Context, string) error {
	return nil
}

func (s *recordingService) RotateAdminKey(context.Context) (string, error) {
	return "ssh-ed25519 AAAA", nil
}

func (s *recordingService) BuildISO(_ context.Context, request ISORequest) (string, error) {
	s.iso = request
	return "iso-job", nil
}

func (s *recordingService) Publish(_ context.Context, request PublishRequest) (string, error) {
	s.publish = request
	return "publish-job", nil
}

func newClientForTest(t *testing.T, service *recordingService) *Client {
	t.Helper()
	server := httptest.NewServer(NewServer(service, nil).Handler())
	t.Cleanup(server.Close)
	return &Client{base: server.URL, http: server.Client()}
}

func TestClientRequestsAndResponses(t *testing.T) {
	service := &recordingService{
		status:  Status{ID: "id", Hostname: "host"},
		devices: []Device{{ID: "peer", Hostname: "peer"}},
		jobs:    []jobs.Job{{ID: "job", State: jobs.StateSucceeded}},
	}
	client := newClientForTest(t, service)
	ctx := context.Background()

	status, err := client.Status(ctx)
	if err != nil || status.Hostname != "host" {
		t.Fatalf("status: %+v %v", status, err)
	}
	devices, err := client.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].ID != "peer" {
		t.Fatalf("devices: %+v %v", devices, err)
	}
	if err := client.Register(ctx, "peer", RegisterRequest{Repository: "repo", Branch: "main", Host: "host", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	if service.register.Repository != "repo" || service.register.Password != "pw" {
		t.Fatalf("register not forwarded: %+v", service.register)
	}
	if err := client.Unregister(ctx, "peer", "pw"); err != nil {
		t.Fatal(err)
	}
	if service.unregister != "pw" {
		t.Fatalf("unregister not forwarded: %q", service.unregister)
	}
	deploy, err := client.Deploy(ctx, "peer", DeployRequest{Repository: "repo", Host: "host"})
	if err != nil || deploy != "deploy-job" {
		t.Fatalf("deploy: %q %v", deploy, err)
	}
	if service.deploy.Host != "host" {
		t.Fatalf("deploy not forwarded: %+v", service.deploy)
	}
	update, err := client.Update(ctx, "peer", UpdateRequest{Mode: "manual", Timeout: 30})
	if err != nil || update != "update-job" {
		t.Fatalf("update: %q %v", update, err)
	}
	if service.update.Mode != "manual" || service.update.Timeout != 30 {
		t.Fatalf("update not forwarded: %+v", service.update)
	}
	ssh, err := client.SSH(ctx, "peer")
	if err != nil || ssh.Host != "10.0.0.2" || ssh.Port != 22 {
		t.Fatalf("ssh: %+v %v", ssh, err)
	}
	list, err := client.Jobs(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "job" {
		t.Fatalf("jobs: %+v %v", list, err)
	}
	job, err := client.Job(ctx, "job")
	if err != nil || job.ID != "job" {
		t.Fatalf("job: %+v %v", job, err)
	}
	if err := client.Confirm(ctx, "job"); err != nil {
		t.Fatal(err)
	}
	if service.confirmID != "job" || service.confirmer != "local" {
		t.Fatalf("confirm not forwarded: %+v %+v", service.confirmID, service.confirmer)
	}
	if err := client.Cancel(ctx, "job"); err != nil {
		t.Fatal(err)
	}
	if service.cancelID != "job" {
		t.Fatalf("cancel not forwarded: %+v", service.cancelID)
	}
	pair, err := client.PairStart(ctx, PairRequest{PeerID: "peer", PeerKey: "key"})
	if err != nil || pair.ID != "pair" || pair.Code != "123456" {
		t.Fatalf("pair start: %+v %v", pair, err)
	}
	if service.pairStart.PeerID != "peer" {
		t.Fatalf("pair start not forwarded: %+v", service.pairStart)
	}
	if err := client.PairConfirm(ctx, PairConfirmRequest{ID: "pair", Code: "123456"}); err != nil {
		t.Fatal(err)
	}
	if service.pairConfirm.ID != "pair" || service.pairConfirm.Code != "123456" {
		t.Fatalf("pair confirm not forwarded: %+v", service.pairConfirm)
	}
	pending, err := client.PairPending(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != "pair" {
		t.Fatalf("pair pending: %+v %v", pending, err)
	}
	envelope, err := client.ShareKey(ctx, KeyShareRequest{PeerID: "peer", PeerPublicKey: "pub"})
	if err != nil || envelope.Version != 1 {
		t.Fatalf("share key: %+v %v", envelope, err)
	}
	if service.keyShare.PeerID != "peer" || service.keyShare.PeerPublicKey != "pub" {
		t.Fatalf("share key not forwarded: %+v", service.keyShare)
	}
	imported, err := client.ImportSharedKey(ctx, "peer")
	if err != nil || imported != "ssh-ed25519 AAAA" {
		t.Fatalf("import key: %q %v", imported, err)
	}
	if err := client.ResetRegistration(ctx); err != nil {
		t.Fatal(err)
	}
	records, err := client.TrustRecords(ctx)
	if err != nil || len(records) != 1 || records[0].Fingerprint != "peer" {
		t.Fatalf("trust records: %+v %v", records, err)
	}
	if err := client.RevokeTrust(ctx, "peer"); err != nil {
		t.Fatal(err)
	}
	rotated, err := client.RotateAdminKey(ctx)
	if err != nil || rotated != "ssh-ed25519 AAAA" {
		t.Fatalf("rotate key: %q %v", rotated, err)
	}
	iso, err := client.BuildISO(ctx, ISORequest{Output: "/tmp/x.iso"})
	if err != nil || iso != "iso-job" {
		t.Fatalf("iso: %q %v", iso, err)
	}
	if service.iso.Output != "/tmp/x.iso" {
		t.Fatalf("iso not forwarded: %+v", service.iso)
	}
	publish, err := client.Publish(ctx, PublishRequest{RemoteURL: "https://example.invalid/new", Branch: "main"})
	if err != nil || publish != "publish-job" {
		t.Fatalf("publish: %q %v", publish, err)
	}
	if service.publish.RemoteURL != "https://example.invalid/new" {
		t.Fatalf("publish not forwarded: %+v", service.publish)
	}
}

func TestClientErrors(t *testing.T) {
	service := &recordingService{failRegister: true}
	client := newClientForTest(t, service)
	if err := client.Register(context.Background(), "peer", RegisterRequest{}); err == nil || err.Error() != "register failed" {
		t.Fatalf("unexpected register error: %v", err)
	}
	if _, err := client.Job(context.Background(), "missing"); err == nil {
		t.Fatal("unknown job was accepted")
	}
}
