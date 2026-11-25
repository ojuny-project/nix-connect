package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
)

func TestDeviceStateAndShortID(t *testing.T) {
	self := httpapi.Device{Self: true}
	if deviceState(self) != "self" {
		t.Fatalf("unexpected self state %q", deviceState(self))
	}
	paired := httpapi.Device{Paired: true}
	if deviceState(paired) != "paired" {
		t.Fatalf("unexpected paired state %q", deviceState(paired))
	}
	unpaired := httpapi.Device{}
	if deviceState(unpaired) != "unpaired" {
		t.Fatalf("unexpected unpaired state %q", deviceState(unpaired))
	}
	if deviceShortID(httpapi.Device{ID: "ABCDEFGHIJ"}) != "ABCDEFGH" {
		t.Fatal("short ID was not derived from the identity")
	}
	if deviceShortID(httpapi.Device{ID: "ABCDEFGHIJ", ShortID: "SHORT"}) != "SHORT" {
		t.Fatal("short ID was not taken from the device")
	}
}

func TestModelActionAvailability(t *testing.T) {
	value := NewModel(nil)
	if value.actionAvailable("deploy") {
		t.Fatal("deploy was enabled without a selection")
	}
	value.Devices = []httpapi.Device{
		{ID: "self", Self: true, Paired: true, Registered: true},
		{ID: "peer", Paired: true, Registered: true},
		{ID: "live", Kind: "live"},
	}
	value.Select(0)
	if !value.actionAvailable("ssh") || value.actionAvailable("register") || value.actionAvailable("pair") {
		t.Fatalf("unexpected self action availability")
	}
	value.Select(1)
	if !value.actionAvailable("share") || !value.actionAvailable("ssh") || !value.actionAvailable("update") {
		t.Fatalf("unexpected paired action availability")
	}
	value.Select(2)
	if value.actionAvailable("ssh") || value.actionAvailable("share") || value.actionAvailable("update") {
		t.Fatalf("unexpected live action availability")
	}
	if !value.actionAvailable("deploy") || !value.actionAvailable("register") {
		t.Fatalf("live deploy actions are unavailable")
	}
}

func TestActiveJobs(t *testing.T) {
	now := time.Now()
	value := NewModel(nil)
	value.Jobs = []jobs.Job{
		{ID: "one", State: jobs.StateActivating, Updated: now.Add(-time.Minute)},
		{ID: "two", State: jobs.StateSucceeded, Updated: now},
		{ID: "three", State: jobs.StateAwaitingConfirmation, Updated: now},
	}
	active, latest := value.ActiveJobs()
	if active != 2 {
		t.Fatalf("unexpected active count %d", active)
	}
	if latest == nil || latest.ID != "three" {
		t.Fatalf("unexpected latest active job %+v", latest)
	}
	if !jobTerminal(jobs.StateSucceeded) || jobTerminal(jobs.StateActivating) {
		t.Fatal("job terminal detection is wrong")
	}
}

func TestFormattingHelpers(t *testing.T) {
	if fallback("", "value") != "value" || fallback("set", "value") != "set" {
		t.Fatal("fallback is wrong")
	}
	if formatTimestamp(time.Time{}) != "-" {
		t.Fatal("zero timestamp was formatted")
	}
	if deviceAddress(httpapi.Device{}) != "-" || deviceKind(httpapi.Device{}) != "unknown" {
		t.Fatal("device fallbacks are wrong")
	}
}

func TestSelectClearsSelection(t *testing.T) {
	value := NewModel(nil)
	value.Devices = []httpapi.Device{{ID: "one"}}
	value.Select(0)
	if value.Selected != 0 || !value.Detail {
		t.Fatalf("selection was not stored: %+v", value)
	}
	value.Select(-1)
	if value.Selected != -1 || value.Detail {
		t.Fatalf("selection was not cleared: %+v", value)
	}
	value.Select(5)
	if value.Selected != -1 || value.Detail {
		t.Fatalf("out of range selection was accepted: %+v", value)
	}
}

func TestISOOutputPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expanded, err := isoOutputPath("~/temp/deploy-target-iso")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, "temp", "deploy-target-iso")
	if expanded != expected {
		t.Fatalf("unexpected ISO output %q", expanded)
	}
	expanded, err = isoOutputPath("")
	if err != nil {
		t.Fatal(err)
	}
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if expanded != filepath.Join(working, "deploy-target-iso") {
		t.Fatalf("unexpected default ISO output %q", expanded)
	}
}
