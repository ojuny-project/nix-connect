package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/identity"
	"github.com/ojuny-project/nix-connect/internal/info"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

func startTargetServer(t *testing.T, target *Service) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := target.Store.Identity.TLSCertificate()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	server := httpapi.NewServer(target, nil)
	go func() {
		_ = server.ServeTLSListener(ctx, listener, certificate, nil)
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestRemotePairingEstablishesMutualTrust(t *testing.T) {
	admin, adminStore := newTestService(t)
	target, targetStore := newTestService(t)
	port := startTargetServer(t, target)
	admin.Registry.Upsert(info.Payload{
		ID:          targetStore.Identity.Fingerprint(),
		Hostname:    "target",
		Kind:        "live",
		ControlPort: port,
		PublicKey:   targetStore.Identity.Public,
	}, "127.0.0.1")

	response, err := admin.PairStart(localContext(), httpapi.PairRequest{
		PeerID:  targetStore.Identity.Fingerprint(),
		PeerKey: identity.PublicKeyText(adminStore.Identity.Public),
	})
	if err != nil {
		t.Fatal(err)
	}
	expected := pairing.Code(targetStore.Identity.Fingerprint(), adminStore.Identity.Fingerprint())
	if response.Code != expected {
		t.Fatalf("unexpected pairing code %q, want %q", response.Code, expected)
	}
	if pair, ok := admin.remotePair(response.ID); !ok || pair.TargetID != targetStore.Identity.Fingerprint() {
		t.Fatalf("remote pair was not tracked: %+v %t", pair, ok)
	}
	if err := admin.PairConfirm(localContext(), httpapi.PairConfirmRequest{ID: response.ID, Code: "000000"}); err == nil {
		t.Fatal("wrong pairing code was accepted")
	}
	if adminStore.Trust.Has(targetStore.Identity.Fingerprint()) || targetStore.Trust.Has(adminStore.Identity.Fingerprint()) {
		t.Fatal("wrong pairing code wrote a trust record")
	}
	if err := admin.PairConfirm(localContext(), httpapi.PairConfirmRequest{ID: response.ID, Code: response.Code}); err != nil {
		t.Fatal(err)
	}
	if !adminStore.Trust.Has(targetStore.Identity.Fingerprint()) {
		t.Fatal("the administration device did not trust the target")
	}
	if targetStore.Trust.Has(adminStore.Identity.Fingerprint()) {
		t.Fatal("the target trusted the administration device before its own confirmation")
	}
	if err := target.PairConfirm(localContext(), httpapi.PairConfirmRequest{ID: response.ID, Code: response.Code}); err != nil {
		t.Fatal(err)
	}
	if !targetStore.Trust.Has(adminStore.Identity.Fingerprint()) {
		t.Fatal("the target did not trust the administration device")
	}
	if _, ok := admin.remotePair(response.ID); ok {
		t.Fatal("confirmed remote pair was not forgotten")
	}
	if err := admin.RevokeTrust(localContext(), targetStore.Identity.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	if adminStore.Trust.Has(targetStore.Identity.Fingerprint()) {
		t.Fatal("the administration device still trusts the revoked target")
	}
	if targetStore.Trust.Has(adminStore.Identity.Fingerprint()) {
		t.Fatal("the target still trusts the revoked administration device")
	}
}

func TestRemoteUpdateForwardsTheAdministrationKey(t *testing.T) {
	admin, adminStore := newTestService(t)
	target, targetStore := newTestService(t)
	targetStore.Trust.Add(trust.Record{Fingerprint: adminStore.Identity.Fingerprint()})
	requests := make(chan httpapi.UpdateRequest, 1)
	target.UpdateFunc = func(ctx context.Context, jobID, targetID string, request httpapi.UpdateRequest) error {
		requests <- request
		return nil
	}
	port := startTargetServer(t, target)
	admin.Registry.Upsert(info.Payload{
		ID:          targetStore.Identity.Fingerprint(),
		Hostname:    "target",
		Kind:        "installed",
		ControlPort: port,
		PublicKey:   targetStore.Identity.Public,
	}, "127.0.0.1")

	magic := false
	if _, err := admin.Update(localContext(), targetStore.Identity.Fingerprint(), httpapi.UpdateRequest{Password: "secret", MagicRollback: &magic}); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		expected, err := adminStore.AdminAuthorizedKey()
		if err != nil {
			t.Fatal(err)
		}
		if request.AdminKey != expected {
			t.Fatalf("administration key was not forwarded: %q", request.AdminKey)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("target update callback was not called")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		list := target.JobManager.List()
		if len(list) == 1 && list[0].State == jobs.StateSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("target update job did not finish: %+v", list)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRemoteRegistrationForwardsToTheTarget(t *testing.T) {
	admin, adminStore := newTestService(t)
	target, targetStore := newTestService(t)
	targetStore.Trust.Add(trust.Record{Fingerprint: adminStore.Identity.Fingerprint()})
	port := startTargetServer(t, target)
	admin.Registry.Upsert(info.Payload{
		ID:          targetStore.Identity.Fingerprint(),
		Hostname:    "target",
		Kind:        "installed",
		ControlPort: port,
		PublicKey:   targetStore.Identity.Public,
	}, "127.0.0.1")

	request := httpapi.RegisterRequest{Repository: "https://example.invalid/flake", Branch: "main", Host: "host"}
	if err := admin.Register(localContext(), targetStore.Identity.Fingerprint(), request); err != nil {
		t.Fatal(err)
	}
	if !targetStore.Registration.Registered() || targetStore.Registration.Repository != request.Repository {
		t.Fatalf("registration was not written on the target: %+v", targetStore.Registration)
	}
	if adminStore.Registration.Registered() {
		t.Fatal("registration was written on the administration device")
	}
	if err := admin.Unregister(localContext(), targetStore.Identity.Fingerprint(), ""); err != nil {
		t.Fatal(err)
	}
	if targetStore.Registration.Registered() {
		t.Fatal("unregister did not reach the target")
	}
}

func TestImportSharedKeyInstallsThePeerKey(t *testing.T) {
	admin, adminStore := newTestService(t)
	peer, peerStore := newTestService(t)
	adminStore.Trust.Add(trust.Record{Fingerprint: peerStore.Identity.Fingerprint()})
	peerStore.Trust.Add(trust.Record{Fingerprint: adminStore.Identity.Fingerprint()})
	port := startTargetServer(t, peer)
	admin.Registry.Upsert(info.Payload{
		ID:          peerStore.Identity.Fingerprint(),
		Hostname:    "peer",
		Kind:        "installed",
		ControlPort: port,
		PublicKey:   peerStore.Identity.Public,
	}, "127.0.0.1")

	var agentKey []byte
	admin.RotateAgent = func(key ed25519.PrivateKey) error {
		agentKey = append([]byte(nil), key...)
		return nil
	}
	publicKey, err := admin.ImportSharedKey(localContext(), peerStore.Identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := peerStore.AdminAuthorizedKey()
	if err != nil {
		t.Fatal(err)
	}
	if publicKey != expected {
		t.Fatalf("imported key %q does not match the peer key %q", publicKey, expected)
	}
	stored, err := adminStore.AdminAuthorizedKey()
	if err != nil || stored != expected {
		t.Fatalf("imported key was not stored: %q %v", stored, err)
	}
	if !bytes.Equal(agentKey, adminStore.AdminKeyCopy()) {
		t.Fatal("the agent was not given the imported key")
	}
	if adminStore.Config.AdminAuthorizedKey != expected {
		t.Fatalf("config did not record the imported key: %q", adminStore.Config.AdminAuthorizedKey)
	}
}

func TestImportSharedKeyRequiresTrustAndAPeer(t *testing.T) {
	admin, adminStore := newTestService(t)
	if _, err := admin.ImportSharedKey(localContext(), adminStore.Identity.Fingerprint()); err == nil || err.Error() != "service: a peer id is required" {
		t.Fatalf("unexpected self import error: %v", err)
	}
	if _, err := admin.ImportSharedKey(localContext(), "peer"); err == nil || err.Error() != "service: peer is not paired" {
		t.Fatalf("unexpected untrusted import error: %v", err)
	}
	if _, err := admin.ImportSharedKey(peerContext("peer"), "peer"); err == nil || err.Error() != "service: this action is local only" {
		t.Fatalf("unexpected remote import error: %v", err)
	}
}

func TestRemoteDeployForwardsAndAggregatesJob(t *testing.T) {
	admin, _ := newTestService(t)
	target, targetStore := newTestService(t)
	targetStore.Trust.Add(trust.Record{Fingerprint: admin.Store.Identity.Fingerprint()})
	requests := make(chan httpapi.DeployRequest, 1)
	target.DeployFunc = func(ctx context.Context, jobID, targetID string, request httpapi.DeployRequest) error {
		requests <- request
		return nil
	}
	port := startTargetServer(t, target)
	admin.Registry.Upsert(info.Payload{
		ID:          targetStore.Identity.Fingerprint(),
		Hostname:    "target",
		Kind:        "installed",
		ControlPort: port,
		PublicKey:   targetStore.Identity.Public,
	}, "127.0.0.1")

	magic := false
	jobID, err := admin.Deploy(localContext(), targetStore.Identity.Fingerprint(), httpapi.DeployRequest{
		Repository:    "https://example.invalid/flake",
		Branch:        "main",
		Host:          "host",
		MagicRollback: &magic,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if request.Repository != "https://example.invalid/flake" || request.AdminKey == "" {
			t.Fatalf("unexpected forwarded request: %+v", request)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("target deploy callback was not called")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := admin.Job(localContext(), jobID)
		if err == nil && job.State == jobs.StateSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote job did not succeed: %+v %v", job, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	list := admin.Jobs(localContext())
	if len(list) != 1 || list[0].ID != jobID {
		t.Fatalf("unexpected aggregated jobs: %+v", list)
	}
}
