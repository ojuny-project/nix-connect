package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

func sampleModel() model {
	return model{
		devices: []httpapi.Device{
			{ID: "self-fingerprint", ShortID: "self-fin", Hostname: "self", Self: true, Paired: true},
			{ID: "peer-fingerprint", ShortID: "peer-fin", Hostname: "peer", Address: "10.0.0.2", Kind: "live", Paired: false},
		},
		status: httpapi.Status{Hostname: "self", ShortID: "self-fin"},
	}
}

func TestViewEmpty(t *testing.T) {
	view := model{}.View()
	if !strings.Contains(view, "nix-connect") {
		t.Fatal("title missing")
	}
	if !strings.Contains(view, "No devices discovered") {
		t.Fatal("empty state missing")
	}
	if !strings.Contains(view, "r refresh") {
		t.Fatal("key hints missing")
	}
}

func TestViewListsDevices(t *testing.T) {
	view := sampleModel().View()
	for _, expected := range []string{"self", "peer", "10.0.0.2", "live", "paired"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q:\n%s", expected, view)
		}
	}
}

func TestViewShowsDetails(t *testing.T) {
	value := sampleModel()
	value.detail = true
	value.cursor = 1
	view := value.View()
	if !strings.Contains(view, "Device") || !strings.Contains(view, "peer-fingerprint") || !strings.Contains(view, "registered:") {
		t.Fatalf("details missing:\n%s", view)
	}
}

func TestUpdateMovesCursor(t *testing.T) {
	value := sampleModel()
	updated, _ := value.Update(tea.KeyMsg{Type: tea.KeyDown})
	value = updated.(model)
	if value.cursor != 1 {
		t.Fatalf("unexpected cursor %d", value.cursor)
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyDown})
	value = updated.(model)
	if value.cursor != 1 {
		t.Fatalf("cursor moved past the end: %d", value.cursor)
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyUp})
	value = updated.(model)
	if value.cursor != 0 {
		t.Fatalf("unexpected cursor %d", value.cursor)
	}
}

func TestUpdateTogglesDetailsAndQuits(t *testing.T) {
	value := sampleModel()
	updated, _ := value.Update(tea.KeyMsg{Type: tea.KeyEnter})
	value = updated.(model)
	if !value.detail {
		t.Fatal("details were not enabled")
	}
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	value = updated.(model)
	if command == nil {
		t.Fatal("quit command is nil")
	}
}

func TestLoadedMessageClampsCursor(t *testing.T) {
	value := sampleModel()
	value.cursor = 10
	updated, _ := value.Update(loadedMsg{devices: []httpapi.Device{{ID: "one", Hostname: "one"}}, status: httpapi.Status{Hostname: "one"}})
	value = updated.(model)
	if value.cursor != 0 {
		t.Fatalf("unexpected cursor %d", value.cursor)
	}
	if len(value.devices) != 1 || value.status.Hostname != "one" {
		t.Fatalf("loaded state was not applied: %+v", value)
	}
}

func TestLoadedErrorRenders(t *testing.T) {
	updated, _ := (model{}).Update(loadedMsg{err: contextError("boom")})
	view := updated.(model).View()
	if !strings.Contains(view, "boom") {
		t.Fatalf("error missing:\n%s", view)
	}
}

func TestFormEditingAndCancel(t *testing.T) {
	value := sampleModel()
	updated, _ := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	value = updated.(model)
	if value.form == nil || value.form.kind != "deploy" {
		t.Fatalf("deploy form was not opened: %+v", value.form)
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
	value = updated.(model)
	if value.form.values[0] != "abc" {
		t.Fatalf("unexpected field value %q", value.form.values[0])
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	value = updated.(model)
	if value.form.values[0] != "ab" {
		t.Fatalf("backspace did not edit the field: %q", value.form.values[0])
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyTab})
	value = updated.(model)
	if value.form.index != 1 {
		t.Fatalf("tab did not move the field index: %d", value.form.index)
	}
	view := value.View()
	if !strings.Contains(view, "Deploy") || !strings.Contains(view, "branch") {
		t.Fatalf("form view is missing fields:\n%s", view)
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyEsc})
	value = updated.(model)
	if value.form != nil {
		t.Fatal("escape did not close the form")
	}
}

func TestFormSubmitWithoutDevice(t *testing.T) {
	value := model{form: newForm("update")}
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	value = updated.(model)
	if value.err == nil || value.err.Error() != "select a device first" {
		t.Fatalf("unexpected submit error: %v", value.err)
	}
	if command != nil {
		t.Fatal("submit without a device returned a command")
	}
}

func TestTrustViewAndRevokeConfirmation(t *testing.T) {
	value := sampleModel()
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	value = updated.(model)
	if !value.trustView || command == nil {
		t.Fatal("trust view was not opened")
	}
	value.trust = []trust.Record{{Fingerprint: "peer-fingerprint", Hostname: "peer", Address: "10.0.0.2"}}
	view := value.View()
	if !strings.Contains(view, "Trusted devices") || !strings.Contains(view, "peer-fingerprint") {
		t.Fatalf("trust view is missing records:\n%s", view)
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	value = updated.(model)
	if value.confirm != "revoke:peer-fingerprint" {
		t.Fatalf("revoke was not armed: %q", value.confirm)
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	value = updated.(model)
	if value.confirm != "" || command == nil {
		t.Fatal("revoke was not submitted")
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyEsc})
	value = updated.(model)
	if value.trustView {
		t.Fatal("escape did not close the trust view")
	}
}

func TestResetAndRotateConfirmation(t *testing.T) {
	value := sampleModel()
	value.cursor = 1
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	value = updated.(model)
	if value.confirm != "reset" || command != nil {
		t.Fatal("reset was not armed")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	value = updated.(model)
	if value.confirm != "" || command == nil {
		t.Fatal("reset was not submitted")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	value = updated.(model)
	if value.confirm != "rotate" || command != nil {
		t.Fatal("key rotation was not armed")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("K")})
	value = updated.(model)
	if value.confirm != "" || command == nil {
		t.Fatal("key rotation was not submitted")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	value = updated.(model)
	if value.confirm != "share:peer-fingerprint" || command != nil {
		t.Fatalf("key sharing was not armed: %q", value.confirm)
	}
}

func TestTrustViewListsPendingPairings(t *testing.T) {
	value := sampleModel()
	value.trustView = true
	value.pending = []pairing.Request{{
		ID:      "pair-id",
		PeerID:  "admin-fingerprint",
		Code:    "123456",
		Expires: time.Now().Add(time.Minute),
	}}
	value.trust = []trust.Record{{Fingerprint: "peer-fingerprint", Hostname: "peer"}}
	view := value.View()
	if !strings.Contains(view, "pair-id") || !strings.Contains(view, "123456") || !strings.Contains(view, "confirm pending") {
		t.Fatalf("pending pairing is missing:\n%s", view)
	}
	updated, _ := value.Update(tea.KeyMsg{Type: tea.KeyDown})
	value = updated.(model)
	if value.trustCursor != 1 {
		t.Fatalf("cursor did not reach the trust record: %d", value.trustCursor)
	}
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	value = updated.(model)
	if command != nil {
		t.Fatal("confirm on a trust record returned a command")
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyUp})
	value = updated.(model)
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	value = updated.(model)
	if command == nil {
		t.Fatal("pending pairing was not confirmed")
	}
}

func TestJobsViewAndConfirmation(t *testing.T) {
	value := sampleModel()
	value.jobsView = true
	value.jobList = []jobs.Job{{
		ID:      "job-1",
		Kind:    jobs.KindUpdate,
		Target:  "peer-fingerprint",
		Phase:   "activate",
		State:   jobs.StateAwaitingConfirmation,
		Updated: time.Now(),
	}}
	view := value.View()
	if !strings.Contains(view, "Jobs") || !strings.Contains(view, "awaiting_confirmation") || !strings.Contains(view, "confirm") {
		t.Fatalf("jobs view is missing state:\n%s", view)
	}
	updated, command := value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	value = updated.(model)
	if command == nil {
		t.Fatal("confirmation was not sent")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	value = updated.(model)
	if value.confirm != "cancel:job-1" || command != nil {
		t.Fatal("cancel was not armed")
	}
	updated, command = value.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	value = updated.(model)
	if command == nil {
		t.Fatal("cancel was not sent")
	}
	updated, _ = value.Update(tea.KeyMsg{Type: tea.KeyEsc})
	value = updated.(model)
	if value.jobsView {
		t.Fatal("escape did not close the jobs view")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Fatal("short value changed")
	}
	value := truncate("abcdef", 4)
	if value != "abc…" {
		t.Fatalf("unexpected truncation %q", value)
	}
	if truncate("abcdef", 1) != "a" {
		t.Fatal("single character truncation failed")
	}
}

type contextError string

func (e contextError) Error() string {
	return string(e)
}
