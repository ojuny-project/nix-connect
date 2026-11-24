package registry

import (
	"sort"
	"sync"
	"time"

	"github.com/ojuny-project/nix-connect/internal/info"
)

type Peer struct {
	Payload  info.Payload
	Address  string
	LastSeen time.Time
}

type Registry struct {
	mutex sync.RWMutex
	peers map[string]Peer
}

func New() *Registry {
	return &Registry{peers: make(map[string]Peer)}
}

func (r *Registry) Upsert(payload info.Payload, address string) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.peers[payload.ID] = Peer{
		Payload:  payload,
		Address:  address,
		LastSeen: time.Now().UTC(),
	}
}

func (r *Registry) Get(id string) (Peer, bool) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	peer, ok := r.peers[id]
	return peer, ok
}

func (r *Registry) List() []Peer {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	result := make([]Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		result = append(result, peer)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Payload.Hostname == result[j].Payload.Hostname {
			return result[i].Payload.ID < result[j].Payload.ID
		}
		return result[i].Payload.Hostname < result[j].Payload.Hostname
	})
	return result
}

func (r *Registry) Count() int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return len(r.peers)
}

func (r *Registry) Prune(ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	r.mutex.Lock()
	defer r.mutex.Unlock()
	for id, peer := range r.peers {
		if peer.LastSeen.Before(cutoff) {
			delete(r.peers, id)
		}
	}
}
