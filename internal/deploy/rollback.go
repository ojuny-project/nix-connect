package deploy

import "fmt"

func RollbackScript(previousSystem, activation string) string {
	return fmt.Sprintf(`#!/bin/sh
set -u

if %s; then
  exit 0
fi

if [ -n %q ]; then
  nix-env --profile /nix/var/nix/profiles/system --set %q || true
  /nix/var/nix/profiles/system/bin/switch-to-configuration boot || true
  systemctl reboot
fi

exit 1
`, activation, previousSystem, previousSystem)
}
