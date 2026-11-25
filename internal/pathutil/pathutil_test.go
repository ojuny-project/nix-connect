package pathutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	expanded, err := Expand("~/temp/deploy-target-iso")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(home, "temp", "deploy-target-iso")
	if expanded != expected {
		t.Fatalf("unexpected expanded path %q", expanded)
	}
	expanded, err = Expand("~")
	if err != nil {
		t.Fatal(err)
	}
	if expanded != home {
		t.Fatalf("unexpected home path %q", expanded)
	}
}

func TestExpandAbsoluteAndRelative(t *testing.T) {
	expanded, err := Expand("/var/tmp/deploy-target-iso")
	if err != nil {
		t.Fatal(err)
	}
	if expanded != "/var/tmp/deploy-target-iso" {
		t.Fatalf("absolute path changed to %q", expanded)
	}
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	expanded, err = Expand("deploy-target-iso")
	if err != nil {
		t.Fatal(err)
	}
	if expanded != filepath.Join(working, "deploy-target-iso") {
		t.Fatalf("relative path changed to %q", expanded)
	}
}

func TestExpandRejectsEmpty(t *testing.T) {
	if _, err := Expand("  "); err == nil {
		t.Fatal("empty path was accepted")
	}
}
