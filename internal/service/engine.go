package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ojuny-project/nix-connect/internal/deploy"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/identity"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/ojuny"
	"github.com/ojuny-project/nix-connect/internal/registration"
)

func (s *Service) RunDeploy(ctx context.Context, jobID, targetID string, request httpapi.DeployRequest) error {
	if s.Store.Config.Kind == "installed" {
		return s.runInstalled(ctx, jobID, request.Repository, request.Branch, request.Host, request.AdminKey, request.Mode, request.Timeout, request.MagicRollback)
	}
	return s.runLiveDeploy(ctx, jobID, request)
}

func (s *Service) RunUpdate(ctx context.Context, jobID, targetID string, request httpapi.UpdateRequest) error {
	record := s.Store.Registration
	if !record.Registered() {
		return errors.New("service: device is not registered")
	}
	return s.runInstalled(ctx, jobID, record.Repository, record.Branch, record.Host, request.AdminKey, request.Mode, request.Timeout, request.MagicRollback)
}

func (s *Service) runLiveDeploy(ctx context.Context, jobID string, request httpapi.DeployRequest) error {
	if request.Repository == "" || request.Branch == "" || request.Host == "" {
		return errors.New("service: repository, branch and host are required")
	}
	if request.Disk == "" {
		return errors.New("service: the target disk is required")
	}
	account := request.Account
	if account == "" {
		account = s.Store.Config.Account
	}
	adminKey := request.AdminKey
	if adminKey == "" {
		key, err := s.Store.AdminAuthorizedKey()
		if err != nil {
			return err
		}
		adminKey = key
	}
	jobDirectory := s.Store.JobDir(jobID)
	if err := os.MkdirAll(jobDirectory, 0o700); err != nil {
		return err
	}
	targetURL := s.prepareSource(ctx, jobDirectory, request.Repository, request.Branch, request.Host, request.Disk, account, adminKey, request.Firmware)
	if targetURL == "" {
		targetURL = request.Repository
	}
	spec := deploy.WrapperSpec{
		TargetURL:    targetURL,
		Host:         request.Host,
		System:       "x86_64-linux",
		Account:      account,
		AdminKey:     adminKey,
		DiskDevice:   request.Disk,
		FirmwareEFI:  request.Firmware != "bios",
		IncludeDisko: true,
	}
	if err := deploy.WriteWrapper(jobDirectory, spec, true); err != nil {
		return err
	}
	install := &deploy.Install{Runner: s.runner()}
	closure, err := install.Build(ctx, jobDirectory, request.Host)
	if err != nil {
		return err
	}
	if _, err := s.JobManager.SetState(jobID, jobs.StateStaged); err != nil {
		return err
	}
	s.JobManager.AppendLog(jobID, "built "+closure)
	if _, err := s.JobManager.SetState(jobID, jobs.StateInstalling); err != nil {
		return err
	}
	if err := install.Install(ctx, jobDirectory, request.Host); err != nil {
		return err
	}
	s.Store.Config.Kind = "installed"
	s.Store.Config.Account = account
	s.Store.Config.SSHUser = account
	s.Store.Config.AdminAuthorizedKey = adminKey
	if s.Store.Config.SSHPort == 0 {
		s.Store.Config.SSHPort = 22
	}
	if err := s.Store.SaveConfig(); err != nil {
		return err
	}
	s.Store.Registration.Apply(request.Repository, request.Branch, request.Host, true, "", closure)
	if err := s.Store.SaveRegistration(); err != nil {
		return err
	}
	root := s.InstallRoot
	if root == "" {
		root = "/mnt"
	}
	if targetURL != request.Repository {
		home := filepath.Join(root, "home", account, "repos", "nix-flake")
		if err := deploy.StageTree(filepath.Join(jobDirectory, "source"), home); err != nil {
			return err
		}
	}
	if _, err := s.JobManager.SetState(jobID, jobs.StateSucceeded); err != nil {
		return err
	}
	if err := deploy.StageState(s.Store.Dir, root, jobID); err != nil {
		return err
	}
	if output, err := s.runner().Run(ctx, "", nil, "systemctl", "reboot"); err != nil {
		return fmt.Errorf("service: reboot: %w: %s", err, output)
	}
	return nil
}

func (s *Service) prepareSource(ctx context.Context, jobDirectory, repository, branch, host, disk, account, adminKey, firmware string) string {
	source := filepath.Join(jobDirectory, "source")
	preparer := &ojuny.Preparer{Runner: s.runner(), Git: "git"}
	if err := preparer.Clone(ctx, repository, branch, source); err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(source, "flake.nix")); err != nil {
		return ""
	}
	firmwareValue := "efi"
	if firmware == "bios" {
		firmwareValue = "bios"
	}
	arguments := []string{
		"run", "path:" + source + "#prepare-remote-repository", "--",
		"--root", source,
		"--hostname", host,
		"--username", account,
		"--disk", disk,
		"--firmware", firmwareValue,
	}
	if adminKey != "" {
		arguments = append(arguments, "--ssh-key", adminKey)
	}
	if _, err := s.runner().Run(ctx, source, nil, "nix", arguments...); err != nil {
		return ""
	}
	if _, err := os.Stat(filepath.Join(source, "flake.nix")); err != nil {
		return ""
	}
	return "path:" + source
}

func (s *Service) RunISO(ctx context.Context, jobID string, request httpapi.ISORequest) error {
	if s.ISOSource == "" {
		return errors.New("service: deploytarget ISO source is not configured")
	}
	jobDirectory := s.Store.JobDir(jobID)
	if err := os.MkdirAll(jobDirectory, 0o700); err != nil {
		return err
	}
	source := filepath.Join(jobDirectory, "source")
	if err := deploy.StageTree(s.ISOSource, source); err != nil {
		return err
	}
	adminKey, err := s.Store.AdminAuthorizedKey()
	if err != nil {
		return err
	}
	prepaired := map[string]string{
		"fingerprint":        s.Store.Identity.Fingerprint(),
		"publicKey":          identity.PublicKeyText(s.Store.Identity.Public),
		"adminAuthorizedKey": adminKey,
	}
	data, err := json.MarshalIndent(prepaired, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "prepaired.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.JobManager.AppendLog(jobID, "building deploytarget ISO")
	flake := "path:" + source + "#nixosConfigurations.deploytarget.config.system.build.isoImage"
	destination := request.Output
	if destination == "" {
		destination = filepath.Join(s.Store.ISODir(), "deploy-target-iso")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := s.runner().Run(ctx, source, nil, "nix", "build", "-o", destination, "--print-out-paths", flake)
	if err != nil {
		return fmt.Errorf("service: build deploytarget ISO: %w: %s", err, output)
	}
	built := filepath.Join(firstLine(output), "iso")
	entries, err := os.ReadDir(built)
	if err != nil {
		return err
	}
	var image string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".iso") {
			image = filepath.Join(built, entry.Name())
			break
		}
	}
	if image == "" {
		return fmt.Errorf("service: no ISO image in %s", built)
	}
	if parent, err := os.Stat(filepath.Dir(destination)); err == nil {
		if stat, ok := parent.Sys().(*syscall.Stat_t); ok {
			_ = os.Lchown(destination, int(stat.Uid), int(stat.Gid))
		}
	}
	s.JobManager.AppendLog(jobID, "linked "+destination)
	s.JobManager.AppendLog(jobID, "ISO "+image)
	return nil
}

func (s *Service) RunPublish(ctx context.Context, jobID string, request httpapi.PublishRequest) error {
	record := s.Store.Registration
	if !record.Registered() {
		return errors.New("service: device is not registered")
	}
	if record.InstallMode != registration.ModeNoCommit {
		return errors.New("service: publish requires a no-commit registration")
	}
	if request.RemoteURL == "" {
		return errors.New("service: a new remote URL is required")
	}
	account := record.Account
	if account == "" {
		account = s.Store.Config.Account
	}
	directory := s.PublishRoot
	if directory == "" {
		home := filepath.Join("/home", account)
		if entry, err := user.Lookup(account); err == nil && entry.HomeDir != "" {
			home = entry.HomeDir
		}
		directory = filepath.Join(home, "repos", "nix-flake")
	}
	if _, err := os.Stat(filepath.Join(directory, ".git")); err != nil {
		return fmt.Errorf("service: publish checkout: %w", err)
	}
	branch := request.Branch
	if branch == "" {
		branch = record.Branch
	}
	if branch == "" {
		branch = "main"
	}
	preparer := &ojuny.Preparer{Runner: s.runner(), Git: "git"}
	if err := preparer.Publish(ctx, directory, request.RemoteURL, branch); err != nil {
		return err
	}
	record.Repository = request.RemoteURL
	record.Branch = branch
	record.EnableUpdates()
	s.Store.Registration = record
	if err := s.Store.SaveRegistration(); err != nil {
		return err
	}
	s.JobManager.AppendLog(jobID, "published to "+request.RemoteURL)
	return nil
}

func (s *Service) runInstalled(ctx context.Context, jobID, repository, branch, host, adminKey, mode string, timeout int, magicOverride *bool) error {
	if repository == "" || branch == "" || host == "" {
		return errors.New("service: repository, branch and host are required")
	}
	account := s.Store.Config.Account
	if adminKey == "" {
		adminKey = s.Store.Config.AdminAuthorizedKey
	}
	if adminKey == "" {
		var err error
		adminKey, err = s.Store.AdminAuthorizedKey()
		if err != nil {
			return err
		}
	}
	jobDirectory := s.Store.JobDir(jobID)
	if err := os.MkdirAll(jobDirectory, 0o700); err != nil {
		return err
	}
	spec := deploy.WrapperSpec{
		TargetURL:    repository,
		Host:         host,
		System:       "x86_64-linux",
		Account:      account,
		AdminKey:     adminKey,
		IncludeDisko: false,
	}
	if err := deploy.WriteWrapper(jobDirectory, spec, false); err != nil {
		return err
	}
	activationPath, err := s.buildPath(ctx, jobDirectory, "deploy.nodes."+host+".profiles.system.path")
	if err != nil {
		return err
	}
	closure, err := s.buildPath(ctx, jobDirectory, "nixosConfigurations."+host+".config.system.build.toplevel")
	if err != nil {
		return err
	}
	if _, err := s.JobManager.Update(jobID, func(job *jobs.Job) {
		job.Revision = closure
	}); err != nil {
		return err
	}
	activation := &deploy.Activation{Runner: s.runner(), SystemdRun: "systemd-run"}
	timeoutDuration := s.confirmTimeout(mode, timeout)
	magic := true
	if magicOverride != nil {
		magic = *magicOverride
	}
	if _, err := s.JobManager.SetState(jobID, jobs.StateActivating); err != nil {
		return err
	}
	previous, _ := s.runner().Run(ctx, "", nil, "readlink", "-f", "/run/current-system")
	canary, err := activation.Start(ctx, deploy.ActivationSpec{
		Command:        filepath.Join(activationPath, "activate-rs"),
		Args:           []string{"activate", closure, "--profile-path", "/nix/var/nix/profiles/system", "--temp-path", jobDirectory},
		Closure:        closure,
		TempDir:        jobDirectory,
		PreviousSystem: firstLine(previous),
		Timeout:        timeoutDuration,
		MagicRollback:  magic,
		AutoRollback:   magic,
		UnitName:       "nixconnect-activate-" + jobID,
	})
	if err != nil {
		return err
	}
	if magic {
		if err := s.waitForCanary(ctx, jobID, canary); err != nil {
			return err
		}
		if _, err := s.JobManager.SetCanary(jobID, canary, time.Now().Add(timeoutDuration)); err != nil {
			return err
		}
		if err := deploy.WaitForConfirmation(ctx, canary, timeoutDuration); err != nil {
			return fmt.Errorf("service: confirmation: %w", err)
		}
	}
	if err := s.waitForClosure(ctx, closure, 2*time.Minute); err != nil {
		return err
	}
	s.Store.Registration.Apply(repository, branch, host, true, "", closure)
	if err := s.Store.SaveRegistration(); err != nil {
		return err
	}
	if s.Store.Config.AdminAuthorizedKey != adminKey {
		s.Store.Config.AdminAuthorizedKey = adminKey
		if err := s.Store.SaveConfig(); err != nil {
			return err
		}
	}
	job, ok := s.JobManager.Get(jobID)
	if ok {
		return s.VerifyActivation(ctx, job)
	}
	return nil
}

func (s *Service) VerifyActivation(ctx context.Context, job jobs.Job) error {
	if job.Revision == "" {
		_, err := s.JobManager.SetState(job.ID, jobs.StateSucceeded)
		return err
	}
	if err := s.waitForClosure(ctx, job.Revision, 60*time.Second); err != nil {
		_, _ = s.JobManager.Fail(job.ID, err.Error())
		return err
	}
	_, err := s.JobManager.SetState(job.ID, jobs.StateSucceeded)
	return err
}

func (s *Service) waitForCanary(ctx context.Context, jobID, canary string) error {
	unit := "nixconnect-activate-" + jobID
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(canary); err == nil {
			return nil
		}
		output, _ := s.runner().Run(ctx, "", nil, "systemctl", "is-active", unit)
		state := firstLine(output)
		switch state {
		case "active", "activating", "reloading":
		case "":
			return errors.New("service: activation unit state is unknown")
		default:
			return fmt.Errorf("service: activation stopped before the confirmation hook: %s", state)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("service: activation did not reach the confirmation hook")
}

func (s *Service) waitForClosure(ctx context.Context, closure string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		output, err := s.runner().Run(ctx, "", nil, "readlink", "-f", "/run/current-system")
		if err == nil && firstLine(output) == closure {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("service: activation did not reach %s", closure)
}

func (s *Service) buildPath(ctx context.Context, directory, attribute string) (string, error) {
	flake := "path:" + directory + "#" + attribute
	output, err := s.runner().Run(ctx, directory, nil, "nix", "build", "--no-link", "--print-out-paths", flake)
	if err != nil {
		return "", fmt.Errorf("service: build %s: %w: %s", attribute, err, output)
	}
	path := firstLine(output)
	if path == "" {
		return "", fmt.Errorf("service: build %s produced no path", attribute)
	}
	return path, nil
}

func (s *Service) confirmTimeout(mode string, timeout int) time.Duration {
	if timeout > 0 {
		return time.Duration(timeout) * time.Second
	}
	if mode == "" {
		mode = s.Store.Config.MagicRollback.Mode
	}
	if mode == "manual" {
		return time.Duration(s.Store.Config.MagicRollback.ManualTimeout) * time.Second
	}
	return time.Duration(s.Store.Config.MagicRollback.AutomaticTimeout) * time.Second
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}
