package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ojuny-project/nix-connect/internal/deploy"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/registration"
)

type engineFakeRunner struct {
	calls      []string
	closure    string
	activation string
	warning    string
}

func (f *engineFakeRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) (string, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "nix" {
		for index, argument := range args {
			if argument != "-o" || index+1 >= len(args) {
				continue
			}
			if err := os.Symlink(f.closure, args[index+1]); err != nil {
				return "", err
			}
		}
		for _, argument := range args {
			if strings.Contains(argument, "isoImage") {
				return f.warning + f.closure + "\n", nil
			}
			if strings.Contains(argument, "deploy.nodes.") {
				return f.activation + "\n", nil
			}
			if strings.Contains(argument, "config.system.build.toplevel") {
				return f.closure + "\n", nil
			}
		}
		return "", nil
	}
	if name == "readlink" {
		return f.closure + "\n", nil
	}
	if name == "systemctl" {
		return "active\n", nil
	}
	return "", nil
}

func TestRunLiveDeployStagesInstalledState(t *testing.T) {
	service, store := newTestService(t)
	store.Config.Kind = "live"
	service.InstallRoot = t.TempDir()
	runner := &engineFakeRunner{closure: "/nix/store/abc-system"}
	service.EngineRunner = runner
	job, err := service.JobManager.Create(jobs.KindDeploy, store.Identity.Fingerprint(), store.Identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RunDeploy(context.Background(), job.ID, store.Identity.Fingerprint(), httpapi.DeployRequest{
		Repository: "https://example.invalid/flake",
		Branch:     "main",
		Host:       "host",
		Disk:       "/dev/sda",
		Account:    "alice",
	}); err != nil {
		t.Fatal(err)
	}
	if store.Config.Kind != "installed" || store.Config.Account != "alice" {
		t.Fatalf("config was not switched: %+v", store.Config)
	}
	if store.Registration.Repository != "https://example.invalid/flake" || !store.Registration.UpdatesAllowed {
		t.Fatalf("registration was not written: %+v", store.Registration)
	}
	staged := filepath.Join(service.InstallRoot, "var", "lib", "nix-connect", "identity", "identity.key")
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("identity was not staged: %v", err)
	}
	finished, _ := service.JobManager.Get(job.ID)
	if finished.State != jobs.StateSucceeded {
		t.Fatalf("unexpected job state %q", finished.State)
	}
	for _, expected := range []string{"disko --mode disko", "nixos-install", "systemctl reboot"} {
		found := false
		for _, call := range runner.calls {
			if strings.Contains(call, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing call %q in %v", expected, runner.calls)
		}
	}
}

func TestRunUpdateActivatesAndKeepsRegistration(t *testing.T) {
	service, store := newTestService(t)
	store.Config.Kind = "installed"
	store.Registration.Apply("https://example.invalid/flake", "main", "host", true, "", "")
	runner := &engineFakeRunner{closure: "/nix/store/abc-system", activation: "/nix/store/abc-activation"}
	service.EngineRunner = runner
	job, err := service.JobManager.Create(jobs.KindUpdate, store.Identity.Fingerprint(), store.Identity.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	magic := false
	if err := service.RunUpdate(context.Background(), job.ID, store.Identity.Fingerprint(), httpapi.UpdateRequest{MagicRollback: &magic}); err != nil {
		t.Fatal(err)
	}
	if store.Registration.Repository != "https://example.invalid/flake" || !store.Registration.UpdatesAllowed {
		t.Fatalf("registration changed unexpectedly: %+v", store.Registration)
	}
	finished, _ := service.JobManager.Get(job.ID)
	if finished.State != jobs.StateSucceeded || finished.Revision != "/nix/store/abc-system" {
		t.Fatalf("unexpected job: %+v", finished)
	}
	found := false
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "systemd-run ") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("activation was not started through systemd-run: %v", runner.calls)
	}
}

func TestRunUpdateRequiresRegistration(t *testing.T) {
	service, _ := newTestService(t)
	job, _ := service.JobManager.Create(jobs.KindUpdate, service.Store.Identity.Fingerprint(), service.Store.Identity.Fingerprint())
	if err := service.RunUpdate(context.Background(), job.ID, service.Store.Identity.Fingerprint(), httpapi.UpdateRequest{}); err == nil {
		t.Fatal("update without a registration was accepted")
	}
}

func TestPrepareSourceUsesTemplateCommand(t *testing.T) {
	service, _ := newTestService(t)
	jobDirectory := t.TempDir()
	source := filepath.Join(jobDirectory, "source")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "flake.nix"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &engineFakeRunner{closure: "/nix/store/abc-system"}
	service.EngineRunner = runner
	prepared := service.prepareSource(context.Background(), jobDirectory, "https://example.invalid/flake", "main", "host", "/dev/sda", "alice", "ssh-ed25519 AAAA", "efi")
	if prepared != "path:"+source {
		t.Fatalf("unexpected prepared source %q", prepared)
	}
	found := false
	for _, call := range runner.calls {
		if strings.Contains(call, "prepare-remote-repository") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("template preparation was not invoked: %v", runner.calls)
	}
}

func TestRunISOStagesTheImage(t *testing.T) {
	service, store := newTestService(t)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "flake.nix"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.ISOSource = source
	built := t.TempDir()
	if err := os.MkdirAll(filepath.Join(built, "iso"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(built, "iso", "nixos.iso"), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &engineFakeRunner{closure: built, warning: "warning: evaluation warning\n"}
	service.EngineRunner = runner
	output := filepath.Join(t.TempDir(), "deploy-target-iso")
	if err := service.RunISO(context.Background(), "iso-job", httpapi.ISORequest{Output: output}); err != nil {
		t.Fatal(err)
	}
	link, err := os.Lstat(output)
	if err != nil {
		t.Fatal(err)
	}
	if link.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output is not an out-link: %s", link.Mode())
	}
	target, err := os.Readlink(output)
	if err != nil {
		t.Fatal(err)
	}
	if target != built {
		t.Fatalf("unexpected out-link target %q", target)
	}
	image, err := os.ReadFile(filepath.Join(output, "iso", "nixos.iso"))
	if err != nil || string(image) != "image" {
		t.Fatalf("unexpected image: %q %v", image, err)
	}
	jobDirectory := store.JobDir("iso-job")
	prepairedData, err := os.ReadFile(filepath.Join(jobDirectory, "source", "prepaired.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prepaired map[string]string
	if err := json.Unmarshal(prepairedData, &prepaired); err != nil {
		t.Fatal(err)
	}
	if prepaired["fingerprint"] != store.Identity.Fingerprint() || prepaired["adminAuthorizedKey"] == "" {
		t.Fatalf("unexpected prepaired data: %+v", prepaired)
	}
	found := false
	for _, call := range runner.calls {
		if strings.Contains(call, "isoImage") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ISO build was not invoked: %v", runner.calls)
	}
}

func TestRunPublishUpdatesTheRegistration(t *testing.T) {
	service, store := newTestService(t)
	store.Registration.Apply("https://example.invalid/old", "main", "host", false, "no-commit", "")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	service.PublishRoot = root
	runner := &engineFakeRunner{}
	service.EngineRunner = runner
	if err := service.RunPublish(context.Background(), "publish-job", httpapi.PublishRequest{
		RemoteURL: "https://example.invalid/new",
	}); err != nil {
		t.Fatal(err)
	}
	if store.Registration.Repository != "https://example.invalid/new" ||
		store.Registration.InstallMode != registration.ModePushed ||
		!store.Registration.UpdatesAllowed {
		t.Fatalf("publish did not update the registration: %+v", store.Registration)
	}
	found := false
	for _, call := range runner.calls {
		if strings.Contains(call, "push --set-upstream nixconnect main") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("publish did not push: %v", runner.calls)
	}
}

func TestRunPublishRequiresNoCommitAndCheckout(t *testing.T) {
	service, store := newTestService(t)
	if err := service.RunPublish(context.Background(), "publish-job", httpapi.PublishRequest{RemoteURL: "https://example.invalid/new"}); err == nil || err.Error() != "service: device is not registered" {
		t.Fatalf("unexpected empty publish error: %v", err)
	}
	store.Registration.Apply("https://example.invalid/old", "main", "host", true, "", "")
	if err := service.RunPublish(context.Background(), "publish-job", httpapi.PublishRequest{RemoteURL: "https://example.invalid/new"}); err == nil || err.Error() != "service: publish requires a no-commit registration" {
		t.Fatalf("unexpected pushed publish error: %v", err)
	}
	store.Registration.Apply("https://example.invalid/old", "main", "host", false, "no-commit", "")
	service.PublishRoot = t.TempDir()
	if err := service.RunPublish(context.Background(), "publish-job", httpapi.PublishRequest{RemoteURL: "https://example.invalid/new"}); err == nil || !strings.Contains(err.Error(), "publish checkout") {
		t.Fatalf("unexpected checkout error: %v", err)
	}
}

func TestWaitForCanaryTracksTheUnit(t *testing.T) {
	service, _ := newTestService(t)
	service.EngineRunner = &engineFakeRunner{}
	directory := t.TempDir()
	canary := deploy.CanaryPath(directory, "/nix/store/abc-system")
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = deploy.ArmCanary(canary)
	}()
	if err := service.waitForCanary(context.Background(), "job", canary); err != nil {
		t.Fatal(err)
	}
}

func TestRunISORequiresSource(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.RunISO(context.Background(), "iso-job", httpapi.ISORequest{}); err == nil || err.Error() != "service: deploytarget ISO source is not configured" {
		t.Fatalf("unexpected source error: %v", err)
	}
}

func TestRunLiveDeployRequiresDisk(t *testing.T) {
	service, store := newTestService(t)
	store.Config.Kind = "live"
	service.EngineRunner = &engineFakeRunner{closure: "/nix/store/abc-system"}
	job, _ := service.JobManager.Create(jobs.KindDeploy, store.Identity.Fingerprint(), store.Identity.Fingerprint())
	if err := service.RunDeploy(context.Background(), job.ID, store.Identity.Fingerprint(), httpapi.DeployRequest{
		Repository: "https://example.invalid/flake",
		Branch:     "main",
		Host:       "host",
	}); err == nil {
		t.Fatal("deploy without a disk was accepted")
	}
}
