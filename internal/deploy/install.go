package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Install struct {
	Runner  Runner
	Git     string
	Nix     string
	NixCopy string
	Disko   string
}

func (i *Install) Clone(ctx context.Context, url, branch, dir string) error {
	git := i.Git
	if git == "" {
		git = "git"
	}
	_, err := i.Runner.Run(ctx, "", nil, git, "clone", "--branch", branch, "--single-branch", url, dir)
	return err
}

func (i *Install) Build(ctx context.Context, dir, host string) (string, error) {
	nix := i.Nix
	if nix == "" {
		nix = "nix"
	}
	flake := fmt.Sprintf("path:%s#nixosConfigurations.%s.config.system.build.toplevel", dir, host)
	output, err := i.Runner.Run(ctx, dir, nil, nix, "build", "--no-link", "--print-out-paths", flake)
	if err != nil {
		return "", fmt.Errorf("deploy: build: %w: %s", err, output)
	}
	return firstLine(output), nil
}

func (i *Install) Copy(ctx context.Context, target, closure string) error {
	nixCopy := i.NixCopy
	if nixCopy == "" {
		nixCopy = "nix"
	}
	output, err := i.Runner.Run(ctx, "", nil, nixCopy, "copy", "--to", "ssh-ng://"+target, closure)
	if err != nil {
		return fmt.Errorf("deploy: copy: %w: %s", err, output)
	}
	return nil
}

func (i *Install) Install(ctx context.Context, dir, host string) error {
	disko := i.Disko
	if disko == "" {
		disko = "disko"
	}
	flake := fmt.Sprintf("path:%s#%s", dir, host)
	if output, err := i.Runner.Run(ctx, dir, nil, disko, "--mode", "disko", flake); err != nil {
		return fmt.Errorf("deploy: disko: %w: %s", err, output)
	}
	if output, err := i.Runner.Run(ctx, dir, nil, "nixos-install", "--flake", flake, "--no-root-passwd"); err != nil {
		return fmt.Errorf("deploy: nixos-install: %w: %s", err, output)
	}
	return nil
}

func (i *Install) AuthorizeKey(root, account, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("deploy: administration key is empty")
	}
	directory := filepath.Join(root, "etc", "ssh", "authorized_keys.d")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	path := filepath.Join(directory, account)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == key {
			return nil
		}
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += key + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func StageTree(sourceDir, destinationDir string) error {
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destinationDir, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		}
		return copyFile(path, target, info.Mode().Perm()|0o600)
	})
}

func StageState(sourceDir, destinationDir, jobID string) error {
	files := []string{
		filepath.Join("identity", "identity.key"),
		filepath.Join("identity", "identity.pub"),
		"registration.json",
		"trust.json",
		"config.json",
		"admin_ssh.pub",
	}
	for _, relative := range files {
		source := filepath.Join(sourceDir, relative)
		destination := filepath.Join(destinationDir, "var", "lib", "nix-connect", relative)
		if err := copyFile(source, destination, 0o600); err != nil {
			required := relative == filepath.Join("identity", "identity.key") || relative == filepath.Join("identity", "identity.pub")
			if !required && os.IsNotExist(err) {
				continue
			}
			return err
		}
	}
	if jobID != "" {
		source := filepath.Join(sourceDir, "jobs", jobID+".json")
		destination := filepath.Join(destinationDir, "var", "lib", "nix-connect", "jobs", jobID+".json")
		if err := copyFile(source, destination, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Sync()
}

func firstLine(value string) string {
	for index, char := range value {
		if char == '\n' {
			return value[:index]
		}
	}
	return value
}
