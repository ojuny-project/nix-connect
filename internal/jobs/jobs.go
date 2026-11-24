package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindInstall Kind = "install"
	KindDeploy  Kind = "deploy"
	KindUpdate  Kind = "update"
	KindISO     Kind = "iso"
	KindPublish Kind = "publish"
)

type State string

const (
	StateQueued               State = "queued"
	StatePreparing            State = "preparing"
	StateStaged               State = "staged"
	StateCopying              State = "copying"
	StateHandingOff           State = "handing_off"
	StateInstalling           State = "installing"
	StateActivating           State = "activating"
	StateAwaitingConfirmation State = "awaiting_confirmation"
	StateConfirming           State = "confirming"
	StateVerifying            State = "verifying"
	StateSucceeded            State = "succeeded"
	StateFailed               State = "failed"
	StateRolledBack           State = "rolled_back"
	StateCancelled            State = "cancelled"
	StateInterrupted          State = "interrupted"
)

type Job struct {
	ID              string    `json:"id"`
	Kind            Kind      `json:"kind"`
	Owner           string    `json:"owner"`
	Target          string    `json:"target"`
	Phase           string    `json:"phase"`
	State           State     `json:"state"`
	Revision        string    `json:"revision,omitempty"`
	CanaryPath      string    `json:"canaryPath,omitempty"`
	PreviousSystem  string    `json:"previousSystem,omitempty"`
	ConfirmDeadline time.Time `json:"confirmDeadline,omitempty"`
	Confirmer       string    `json:"confirmer,omitempty"`
	Rollback        string    `json:"rollback,omitempty"`
	Error           string    `json:"error,omitempty"`
	Created         time.Time `json:"created"`
	Updated         time.Time `json:"updated"`
}

type Event struct {
	Type string `json:"type"`
	Job  Job    `json:"job"`
	Line string `json:"line,omitempty"`
}

type Manager struct {
	dir     string
	mutex   sync.RWMutex
	jobs    map[string]*Job
	subs    map[string]map[chan Event]struct{}
	persist bool
}

func New(dir string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	manager := &Manager{
		dir:     dir,
		jobs:    make(map[string]*Job),
		subs:    make(map[string]map[chan Event]struct{}),
		persist: true,
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func NewMemory() *Manager {
	return &Manager{
		jobs: make(map[string]*Job),
		subs: make(map[string]map[chan Event]struct{}),
	}
}

func (m *Manager) load() error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, entry.Name()))
		if err != nil {
			return err
		}
		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}
		if isActive(job.State) {
			job.State = StateInterrupted
			job.Error = "daemon restarted while the job was running"
			job.Updated = time.Now().UTC()
			m.persistJob(&job)
		}
		m.jobs[job.ID] = &job
	}
	return nil
}

func (m *Manager) Create(kind Kind, owner, target string) (*Job, error) {
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	job := &Job{
		ID:      id,
		Kind:    kind,
		Owner:   owner,
		Target:  target,
		State:   StateQueued,
		Created: now,
		Updated: now,
	}
	m.mutex.Lock()
	m.jobs[id] = job
	m.persistJob(job)
	m.mutex.Unlock()
	m.publish(Event{Type: "job", Job: *job})
	return cloneJob(job), nil
}

func (m *Manager) Get(id string) (Job, bool) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *cloneJob(job), true
}

func (m *Manager) List() []Job {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	result := make([]Job, 0, len(m.jobs))
	for _, job := range m.jobs {
		result = append(result, *cloneJob(job))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Created.After(result[j].Created)
	})
	return result
}

func (m *Manager) Update(id string, function func(*Job)) (Job, error) {
	m.mutex.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mutex.Unlock()
		return Job{}, fmt.Errorf("jobs: unknown job %q", id)
	}
	function(job)
	job.Updated = time.Now().UTC()
	m.persistJob(job)
	result := *cloneJob(job)
	m.mutex.Unlock()
	m.publish(Event{Type: "job", Job: result})
	return result, nil
}

func (m *Manager) SetState(id string, state State) (Job, error) {
	return m.Update(id, func(job *Job) {
		job.State = state
	})
}

func (m *Manager) SetPhase(id, phase string) (Job, error) {
	return m.Update(id, func(job *Job) {
		job.Phase = phase
	})
}

func (m *Manager) Fail(id, message string) (Job, error) {
	return m.Update(id, func(job *Job) {
		job.State = StateFailed
		job.Error = message
	})
}

func (m *Manager) SetCanary(id, path string, deadline time.Time) (Job, error) {
	return m.Update(id, func(job *Job) {
		job.CanaryPath = path
		job.ConfirmDeadline = deadline
		job.State = StateAwaitingConfirmation
	})
}

func (m *Manager) Confirm(id, confirmer string) (Job, error) {
	m.mutex.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mutex.Unlock()
		return Job{}, fmt.Errorf("jobs: unknown job %q", id)
	}
	if job.State != StateAwaitingConfirmation {
		m.mutex.Unlock()
		return Job{}, fmt.Errorf("jobs: job is not awaiting confirmation")
	}
	if !job.ConfirmDeadline.IsZero() && time.Now().After(job.ConfirmDeadline) {
		m.mutex.Unlock()
		return Job{}, errors.New("jobs: confirmation deadline has passed")
	}
	job.Confirmer = confirmer
	job.State = StateConfirming
	job.Updated = time.Now().UTC()
	m.persistJob(job)
	result := *cloneJob(job)
	m.mutex.Unlock()
	m.publish(Event{Type: "confirmed", Job: result})
	return result, nil
}

func (m *Manager) Cancel(id string) (Job, error) {
	return m.Update(id, func(job *Job) {
		if job.State == StateSucceeded || job.State == StateFailed || job.State == StateCancelled {
			return
		}
		job.State = StateCancelled
	})
}

func (m *Manager) AppendLog(id, line string) {
	m.mutex.Lock()
	job, ok := m.jobs[id]
	if !ok {
		m.mutex.Unlock()
		return
	}
	snapshot := *cloneJob(job)
	m.mutex.Unlock()

	clean := strings.TrimRight(line, "\n")
	if clean == "" {
		return
	}
	if m.persist {
		file, err := os.OpenFile(filepath.Join(m.dir, id+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			_, _ = file.WriteString(clean + "\n")
			_ = file.Close()
		}
	}
	m.publish(Event{Type: "log", Job: snapshot, Line: clean})
}

func (m *Manager) Log(id string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(m.dir, id+".log"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}

func (m *Manager) Subscribe(id string) (<-chan Event, func()) {
	channel := make(chan Event, 32)
	m.mutex.Lock()
	if m.subs[id] == nil {
		m.subs[id] = make(map[chan Event]struct{})
	}
	m.subs[id][channel] = struct{}{}
	m.mutex.Unlock()

	cancel := func() {
		m.mutex.Lock()
		if subscribers, ok := m.subs[id]; ok {
			delete(subscribers, channel)
			if len(subscribers) == 0 {
				delete(m.subs, id)
			}
		}
		m.mutex.Unlock()
		close(channel)
	}
	return channel, cancel
}

func (m *Manager) publish(event Event) {
	m.mutex.RLock()
	subscribers := make([]chan Event, 0, len(m.subs[event.Job.ID]))
	for channel := range m.subs[event.Job.ID] {
		subscribers = append(subscribers, channel)
	}
	m.mutex.RUnlock()
	for _, channel := range subscribers {
		select {
		case channel <- event:
		default:
		}
	}
}

func (m *Manager) persistJob(job *Job) {
	if !m.persist {
		return
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(m.dir, job.ID+".json")
	temp, err := os.CreateTemp(m.dir, ".job-*")
	if err != nil {
		return
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(name, path)
}

func cloneJob(job *Job) *Job {
	copy := *job
	return &copy
}

func isActive(state State) bool {
	switch state {
	case StatePreparing, StateStaged, StateCopying, StateHandingOff, StateInstalling,
		StateActivating, StateVerifying:
		return true
	default:
		return false
	}
}

func randomID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
