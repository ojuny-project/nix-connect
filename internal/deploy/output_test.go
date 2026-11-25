package deploy

import "testing"

func TestOutputPathSkipsWarnings(t *testing.T) {
	output := "warning: evaluation warning\nthese 2 derivations will be built:\n  /nix/store/a.drv\n/nix/store/result\n"
	if got := OutputPath(output); got != "/nix/store/result" {
		t.Fatalf("unexpected output path %q", got)
	}
}

func TestOutputPathFallsBackToFirstLine(t *testing.T) {
	if got := OutputPath("plain output"); got != "plain output" {
		t.Fatalf("unexpected fallback %q", got)
	}
	if got := OutputPath(""); got != "" {
		t.Fatalf("unexpected empty output %q", got)
	}
}
