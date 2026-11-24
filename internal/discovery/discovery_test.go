package discovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/identity"
	"github.com/ojuny-project/nix-connect/internal/info"
	"github.com/ojuny-project/nix-connect/internal/registry"
)

func TestParseRanges(t *testing.T) {
	ranges, err := ParseRanges([]string{"192.168.1.0/24", "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 2 || ranges[1].Bits() != 32 {
		t.Fatalf("unexpected ranges %v", ranges)
	}
	if _, err := ParseRanges([]string{"not-a-range"}); err == nil {
		t.Fatal("invalid range was accepted")
	}
}

func TestProberFindsSignedPeer(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := info.Payload{
		ID:        identity.Fingerprint(public),
		Version:   info.Version,
		Hostname:  "peer",
		Kind:      "installed",
		SSHPort:   22,
		Daemon:    "0.1.0",
		Timestamp: time.Now().Unix(),
		PublicKey: public,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, encodeErr := payload.Encode(private)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	peers := registry.New()
	prober := &Prober{
		Ranges:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Port:     port,
		Timeout:  time.Second,
		Workers:  1,
		SelfID:   "someone-else",
		Registry: peers,
	}
	if found := prober.Scan(context.Background()); found != 1 {
		t.Fatalf("expected one peer, got %d", found)
	}
	if _, ok := peers.Get(payload.ID); !ok {
		t.Fatal("peer was not registered")
	}
}

func TestProberExcludesSelf(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := identity.Fingerprint(public)
	payload := info.Payload{
		ID:        id,
		Version:   info.Version,
		Hostname:  "self",
		Kind:      "installed",
		Daemon:    "0.1.0",
		Timestamp: time.Now().Unix(),
		PublicKey: public,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := payload.Encode(private)
		_, _ = w.Write(data)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port

	peers := registry.New()
	prober := &Prober{
		Ranges:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Port:     port,
		Timeout:  time.Second,
		Workers:  1,
		SelfID:   id,
		Registry: peers,
	}
	if found := prober.Scan(context.Background()); found != 0 {
		t.Fatalf("expected zero peers, got %d", found)
	}
}

func TestScanSkipsWhenGateIsClosed(t *testing.T) {
	peers := registry.New()
	prober := &Prober{
		Gate:     func() bool { return false },
		Registry: peers,
	}
	if found := prober.Scan(context.Background()); found != 0 {
		t.Fatalf("expected zero peers, got %d", found)
	}
	if peers.Count() != 0 {
		t.Fatal("registry changed while the gate was closed")
	}
}

func TestProbeRejectsUnsignedPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not a signed payload"))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	peers := registry.New()
	prober := &Prober{
		Ranges:   []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		Port:     port,
		Timeout:  time.Second,
		Workers:  1,
		Registry: peers,
	}
	if found := prober.Scan(context.Background()); found != 0 {
		t.Fatalf("expected zero peers, got %d", found)
	}
	if peers.Count() != 0 {
		t.Fatal("unsigned payload was registered")
	}
}

func TestTargetsRespectsTheAddressCap(t *testing.T) {
	prober := &Prober{Ranges: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")}}
	targets := prober.targets()
	if len(targets) != MaxAddresses {
		t.Fatalf("expected %d targets, got %d", MaxAddresses, len(targets))
	}
}
