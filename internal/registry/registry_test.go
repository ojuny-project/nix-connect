package registry

import (
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/info"
)

func TestUpsertGetListAndCount(t *testing.T) {
	registry := New()
	if registry.Count() != 0 {
		t.Fatal("new registry is not empty")
	}
	registry.Upsert(info.Payload{ID: "bravo", Hostname: "zeta"}, "10.0.0.2")
	registry.Upsert(info.Payload{ID: "alpha", Hostname: "alpha"}, "10.0.0.1")
	if registry.Count() != 2 {
		t.Fatalf("unexpected count %d", registry.Count())
	}
	peers := registry.List()
	if len(peers) != 2 {
		t.Fatalf("unexpected list length %d", len(peers))
	}
	if peers[0].Payload.ID != "alpha" || peers[0].Address != "10.0.0.1" {
		t.Fatalf("unexpected sort order: %+v", peers)
	}
	peer, ok := registry.Get("bravo")
	if !ok || peer.Address != "10.0.0.2" {
		t.Fatalf("unexpected peer %+v", peer)
	}
}

func TestUpsertReplacesSameIdentity(t *testing.T) {
	registry := New()
	registry.Upsert(info.Payload{ID: "same", Hostname: "old"}, "10.0.0.1")
	registry.Upsert(info.Payload{ID: "same", Hostname: "new"}, "10.0.0.2")
	if registry.Count() != 1 {
		t.Fatalf("unexpected count %d", registry.Count())
	}
	peer, _ := registry.Get("same")
	if peer.Payload.Hostname != "new" || peer.Address != "10.0.0.2" {
		t.Fatalf("peer was not replaced: %+v", peer)
	}
}

func TestPruneDropsOldPeers(t *testing.T) {
	registry := New()
	registry.Upsert(info.Payload{ID: "old"}, "10.0.0.1")
	registry.Upsert(info.Payload{ID: "new"}, "10.0.0.2")
	registry.mutex.Lock()
	peer := registry.peers["old"]
	peer.LastSeen = time.Now().Add(-time.Hour)
	registry.peers["old"] = peer
	registry.mutex.Unlock()
	registry.Prune(time.Minute)
	if _, ok := registry.Get("old"); ok {
		t.Fatal("old peer was not pruned")
	}
	if _, ok := registry.Get("new"); !ok {
		t.Fatal("fresh peer was pruned")
	}
}
