package registration

import "testing"

func TestTransitions(t *testing.T) {
	var record Record
	if record.Registered() {
		t.Fatal("empty record is registered")
	}
	record.Apply("https://example.invalid/flake", "main", "host", true, "", "abc")
	if record.InstallMode != ModePushed || !record.UpdatesAllowed || record.Reason != "" {
		t.Fatalf("unexpected pushed record: %+v", record)
	}
	record.Apply("https://example.invalid/flake", "main", "host", false, "no-commit", "")
	if record.InstallMode != ModeNoCommit || record.UpdatesAllowed || record.Reason != "no-commit" {
		t.Fatalf("unexpected no-commit record: %+v", record)
	}
	record.EnableUpdates()
	if !record.UpdatesAllowed || record.InstallMode != ModePushed {
		t.Fatalf("updates were not enabled: %+v", record)
	}
	record.DisableUpdates("push-failed")
	if record.UpdatesAllowed || record.Reason != "push-failed" {
		t.Fatalf("updates were not disabled: %+v", record)
	}
}

func TestValidate(t *testing.T) {
	var record Record
	if err := record.Validate(); err == nil {
		t.Fatal("empty record validated")
	}
	record.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.SSHPort = 70000
	if err := record.Validate(); err == nil {
		t.Fatal("invalid SSH port validated")
	}
}

func TestNoCommitReason(t *testing.T) {
	var record Record
	record.Apply("https://example.invalid/flake", "main", "host", false, "push-failed", "")
	if record.InstallMode != ModeNoCommit || record.UpdatesAllowed || record.Reason != "push-failed" {
		t.Fatalf("unexpected no-commit record: %+v", record)
	}
	record.EnableUpdates()
	if record.InstallMode != ModePushed || !record.UpdatesAllowed || record.Reason != "" {
		t.Fatalf("updates were not enabled: %+v", record)
	}
}
