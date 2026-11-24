package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJobLifecycle(t *testing.T) {
	manager := NewMemory()
	job, err := manager.Create(KindUpdate, "admin", "target")
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateQueued {
		t.Fatalf("unexpected state %q", job.State)
	}
	if _, err := manager.SetState(job.ID, StatePreparing); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetCanary(job.ID, "/tmp/canary", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	confirmed, err := manager.Confirm(job.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.State != StateConfirming || confirmed.Confirmer != "admin" {
		t.Fatalf("unexpected confirmation: %+v", confirmed)
	}
}

func TestJobRejectsLateConfirmation(t *testing.T) {
	manager := NewMemory()
	job, _ := manager.Create(KindUpdate, "admin", "target")
	if _, err := manager.SetCanary(job.ID, "/tmp/canary", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Confirm(job.ID, "admin"); err == nil {
		t.Fatal("late confirmation was accepted")
	}
}

func TestManagerPersistsAndInterrupts(t *testing.T) {
	dir := t.TempDir()
	manager, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Create(KindUpdate, "admin", "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetState(job.ID, StateActivating); err != nil {
		t.Fatal(err)
	}
	manager.AppendLog(job.ID, "activation started")

	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok := reopened.Get(job.ID)
	if !ok {
		t.Fatal("job was not persisted")
	}
	if reloaded.State != StateInterrupted {
		t.Fatalf("expected interrupted, got %q", reloaded.State)
	}
	logs, err := reopened.Log(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0] != "activation started" {
		t.Fatalf("unexpected logs %v", logs)
	}
	if _, err := os.Stat(filepath.Join(dir, job.ID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestListNewestFirst(t *testing.T) {
	manager := NewMemory()
	first, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	list := manager.List()
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("unexpected order: %+v", list)
	}
}

func TestPhaseFailAndLog(t *testing.T) {
	directory := t.TempDir()
	manager, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetPhase(job.ID, "activating"); err != nil {
		t.Fatal(err)
	}
	phase, _ := manager.Get(job.ID)
	if phase.Phase != "activating" {
		t.Fatalf("unexpected phase %q", phase.Phase)
	}
	if _, err := manager.Fail(job.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	failed, _ := manager.Get(job.ID)
	if failed.State != StateFailed || failed.Error != "boom" {
		t.Fatalf("unexpected failure: %+v", failed)
	}
	manager.AppendLog(job.ID, "first line")
	manager.AppendLog(job.ID, "second line")
	logs, err := manager.Log(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || logs[0] != "first line" || logs[1] != "second line" {
		t.Fatalf("unexpected logs %v", logs)
	}
}

func TestSubscribe(t *testing.T) {
	manager := NewMemory()
	job, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := manager.Subscribe(job.ID)
	defer cancel()
	if _, err := manager.SetState(job.ID, StatePreparing); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "job" || event.Job.State != StatePreparing {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("no event was published")
	}
}

func TestCancelUnknown(t *testing.T) {
	manager := NewMemory()
	if _, err := manager.Cancel("missing"); err == nil || err.Error() != `jobs: unknown job "missing"` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadKeepsAwaitingConfirmation(t *testing.T) {
	directory := t.TempDir()
	manager, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetCanary(job.ID, filepath.Join(directory, "canary"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get(job.ID)
	if !ok || got.State != StateAwaitingConfirmation {
		t.Fatalf("awaiting job changed state: %+v", got)
	}
}

func TestLoadKeepsQueued(t *testing.T) {
	directory := t.TempDir()
	manager, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Create(KindUpdate, "owner", "target")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := New(directory)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get(job.ID)
	if !ok || got.State != StateQueued {
		t.Fatalf("queued job changed state: %+v", got)
	}
}
