package trust

import (
	"errors"
	"sort"
	"sync"
	"time"
)

type Record struct {
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey"`
	Hostname    string    `json:"hostname,omitempty"`
	Address     string    `json:"address,omitempty"`
	PairedAt    time.Time `json:"pairedAt"`
}

var ErrNotTrusted = errors.New("peer is not trusted")

type Store struct {
	mutex   sync.RWMutex
	records map[string]Record
}

func New() *Store {
	return &Store{records: make(map[string]Record)}
}

func (s *Store) Add(record Record) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if record.PairedAt.IsZero() {
		record.PairedAt = time.Now().UTC()
	}
	s.records[record.Fingerprint] = record
}

func (s *Store) Remove(fingerprint string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.records, fingerprint)
}

func (s *Store) Get(fingerprint string) (Record, bool) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	record, ok := s.records[fingerprint]
	return record, ok
}

func (s *Store) Has(fingerprint string) bool {
	_, ok := s.Get(fingerprint)
	return ok
}

func (s *Store) List() []Record {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	result := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Fingerprint < result[j].Fingerprint
	})
	return result
}

func (s *Store) Snapshot() map[string]Record {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	result := make(map[string]Record, len(s.records))
	for key, value := range s.records {
		result[key] = value
	}
	return result
}

func (s *Store) Replace(records map[string]Record) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records = make(map[string]Record, len(records))
	for key, value := range records {
		s.records[key] = value
	}
}
