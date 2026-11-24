# nix-connect design

Status: design complete. Not implemented.

This document is the governing design. Code, tests and user documentation follow
it. A change that contradicts a limitation in section 24 is rejected unless this
document is changed first.

## 1. Purpose

nix-connect is a LAN management utility for NixOS devices. Every device runs a
daemon that advertises itself, pairs with administration devices, and exposes
privileged operations: SSH access, device information, full installation of a
flake, deploy-rs updates, and flake registration. A CLI with a TUI and a
graphical client offer the same operations.

It works with generic flakes and with Ojuny templates. Ojuny templates get the
generated hardware, disko and user modules that the Ojuny installer flow
produces.

## 2. Goals

1. Discover devices on the LAN without configuration.
2. Show the same device list and the same actions in the CLI/TUI and the GUI.
3. Require no manual SSH setup on a target provisioned by nix-connect.
4. Use one administration SSH keypair across administration devices, shared
   securely over the LAN.
5. Support headless devices, including a server with only the daemon.
6. Install a flake to a live-boot target and carry the device identity and
   registration through the install.
7. Update an installed target with deploy-rs from a registered remote flake.
8. Let a target own a deployment job so the admin device does not need to hold a
   session for the install.
9. Support generic flakes and Ojuny templates with the same action surface.
10. Generate a deploytarget ISO that appears in nix-connect with no interaction
    on wired networking.
11. Publish a device's prepared flake to a new remote so a pre-installed device
    can be linked to its owner's repository.

## 3. Non-goals

1. Fleet management, a controller, users, groups or policy.
2. Public internet, WAN, VPN or cloud operation.
3. Windows and macOS targets.
4. Private repositories, GitHub authentication or a credential store.
5. The handover feature for system integrators.
6. kexec installs and destructive reinstall of an installed device.
7. Secure Boot signing or provisioning of the deploytarget ISO.
8. Per-device administration SSH keys.
9. A binary cache or remote build offload. nix-cache-discovery and
   nix-builder-discovery keep their roles.
10. Editing or extending nix-settings-gui to fit a module shape.
11. Multi-tenant use, browser access or a web interface.
12. Editing the user's flake outside the preparation and publish flows.
13. Changes to the existing Ojuny `remote-install` and `remote-setup` scripts.
    They stay as they are until nix-connect is validated.

## 4. Requirements

### 4.1 Runtime

- NixOS with flakes and a running nix-daemon on every participating device.
- A LAN where the discovery and control ports are reachable between devices.
- One TCP port pair per mesh. All devices in a mesh use the same ports.
- systemd on every device.
- x86_64-linux for the deploytarget ISO.
- Go for the daemon and both frontends.
- GTK4 for the graphical client.
- A live target needs enough memory for the live environment and a target disk
  larger than 8 GiB.

### 4.2 Git

- Every deployed flake is a git URL and a branch. Local paths are not a deploy
  source.
- The admin device must have push access to the repository when the Ojuny
  preparation path commits and pushes.
- Public repositories only.
- git runs as the invoking user with that user's normal environment. nix-connect
  does not manage, store or inspect credentials.

### 4.3 Target provisioning

- A target installed from the deploytarget ISO needs no interaction on the target
  itself when wired.
- A target on Wi-Fi needs `nmtui` to connect. Nothing else is required.
- The target disk is selected during the deployment flow from the block devices
  the live target reports. The administrator applies the Ojuny disk filters:
  reject removable or USB disks, read-only disks, disks smaller than 8 GiB, the
  live medium, mounted disks or disks with mounted partitions, and disks that
  contain an existing vfat partition. An explicit device bypasses the filters.
- Firmware defaults to automatic detection from `/sys/firmware/efi`, with an
  explicit override.
- The installation settings mirror the Ojuny installer settings: disk,
  firmware, hostname, account, password or SSH keys, locale, keymap, timezone,
  swap, encryption and encryption passphrase.

### 4.4 Secure Boot

- The deploytarget ISO is unsigned and requires Secure Boot to be disabled.
- Secure Boot is a post-install flow, matching Ojuny.
- The physical access boundary for an ISO install is documented as: Secure Boot
  plus a UEFI setup password on a correctly configured machine.

### 4.5 Magic rollback

- Magic rollback is enabled by default for activation of an installed device.
- It requires an administration device to remain reachable for the confirmation
  window.
- The automatic health check validates the new control plane and an SSH login as
  the configured account.
- The module sets the default mode and timeouts. A job can override them.
- Magic rollback is automatically disabled when the installed configuration does
  not include the daemon.

## 5. What to avoid

These are binding. Do not implement around them.

1. Do not hold an SSH session for the duration of an install or activation. SSH
   is used for the closure copy and the handoff, not for the job.
2. Do not assume passwordless sudo. The consent gate decides per device.
3. Do not run a mutation as a privileged process without the consent gate.
4. Do not store passwords, passphrases, private keys or git credentials in the
   Nix store, the job record, the event stream or any log.
5. Do not put the registered flake URL, account names, logs or certificate
   identities on the public plane.
6. Do not implement git credential handling, private repository access or
   GitHub authentication.
7. Do not run kexec. A destructive reinstall requires booting the ISO.
8. Do not move a registration on an installed device until the new generation is
   active and verified. An install stages it before the wipe so it survives.
9. Do not clear the non-updatable flag except through nix-connect's own publish
   or an explicit re-registration that verifies the remote.
10. Do not force push, rewrite history, or resolve git conflicts automatically.
11. Do not modify the user's repository in no-commit mode.
12. Do not leave the target's identity or registration to be regenerated after
    an install.
13. Do not add a daemon-less mode. The frontends require the local daemon.
14. Do not implement handover in nix-connect.
15. Do not add a boot-time consent code to the deploytarget ISO.
16. Do not sanitize or police disk identifiers in the user's repository.
17. Do not modify `nix-settings-gui/src/scan.rs` to accept a module shape. Change
    the module shape instead.
18. Do not attach `@option` annotations to values inside a list, `lib.mkMerge`,
    `map`, `forEach` or `lib.optionalAttrs`. Use a plain attribute set or a
    direct `lib.mkIf`.
19. Do not remove the magic rollback canary before the new control plane and an
    SSH login as the configured account both succeed.
20. Do not enable magic rollback when the new configuration does not include the
    daemon.

## 6. Components

Three independently installable packages:

- `nixconnect-daemon`: the local service. Owns device identity, the
  administration SSH key, discovery, trust, registration, jobs and the
  deploytarget ISO build.
- `nixconnect`: the CLI. Running it with no arguments opens the TUI. A second
  symlink named `ncon` points at the same binary.
- `nixconnect-gui`: the graphical client, Go with GTK4.

The frontends are clients of the daemon and require it at runtime. A server may
install only the daemon. An administration laptop installs the daemon plus one
or both frontends. There is no daemon-less mode.

The daemon runs as a system service with state under `/var/lib/nix-connect`.
Target-side operations execute as root. Administration-side preparation, clone,
commit and push execute as the calling user. The local control socket identifies
the caller by UID. No credentials are stored by any component.

The TUI and GUI share one client package and one control API, so parity is a
property of the API rather than a maintenance promise. The only intentional
asymmetry is interactive SSH: the TUI suspends and execs the system ssh, and the
GUI launches a terminal emulator with the same parameters.

### 6.1 CLI surface

`ncon` with no arguments opens the TUI. The headless commands are:

- `ncon status`
- `ncon devices`
- `ncon info <id>`
- `ncon ssh <id>`
- `ncon deploy <id>`
- `ncon update <id>`
- `ncon register <id>`
- `ncon unregister <id>`
- `ncon publish`
- `ncon build-iso`
- `ncon pair`
- `ncon key share`
- `ncon jobs`

Every command has a GUI equivalent. Mutating commands require the consent gate,
and the non-interactive form requires `--yes` where a confirmation would
otherwise be shown.

### 6.2 GUI surface

The GUI is a GTK4 client over the same control API. The window has a header bar
with the local identity and daemon state, a device table with HOST, ID, ADDRESS,
KIND and STATE columns, and a status panel that shows either the local device or
the selected device. The actions are grouped as Manage, Connect and System.
Every action has a tooltip and a keyboard accelerator. Jobs, trust and the
deploytarget ISO use dedicated dialogs. The jobs dialog refreshes while it is
open and shows the phase, error and confirmation deadline of the selected job.
The trust dialog shows pending pairings and trusted devices with confirm and
revoke actions. The ISO dialog accepts an output link and explains that the link
points to `iso/<image>.iso`.

## 7. Planes and ports

| Plane | Default | Transport | Authentication | Contents |
| --- | --- | --- | --- | --- |
| Public info | TCP 5060 | Plain HTTP | None, signed payload | Hostname, addresses, SSH port, device kind, pairing boolean, capabilities, daemon version, job summary |
| Control | TCP 5061 | HTTPS | mTLS pinned to device identity | Device details, account, registration, actions, jobs, events, pairing, key sharing |
| Local control | Unix socket | HTTP over Unix socket | Peer UID via `SO_PEERCRED`, socket group | Same API, plus local-only actions |

The ports are module options: `services.nix-connect.infoPort` and
`services.nix-connect.controlPort`.

Firewall rules follow nix-builder-discovery: the ports are opened to private,
link-local and unique-local prefixes only. A public-IP gate disables discovery
on networks the user has not explicitly trusted. The info endpoint is public by
design. The control plane is gated by mTLS and trust.

The public payload is signed with the device identity key. The signature covers
every byte before the signature field. The timestamp is a replay hint with a
tolerance, never an authorization decision.

## 8. Identity, pairing and key sharing

### 8.1 Device identity

Each device generates an Ed25519 keypair at first start. The private key lives in
the state directory, owned by the service user, mode 0400. The public key is the
device identity. The fingerprint is SHA-256 of the raw public key, base32 without
padding, and the short ID is its first eight characters. The TLS certificate is
self-signed from the same key and is compared by key, not by name.

### 8.2 Administration SSH key

One Ed25519 keypair is shared by administration devices. It is generated on the
first administration device and distributed by pairing. It is stored at mode
0400 and exposed only through an ssh-agent socket served by the daemon. The
private key never appears on a command line, in an environment variable, or in
the Nix store.

The authorized public key is written to `<stateDir>/admin_ssh.pub`. The NixOS
module option `adminAuthorizedKeyFile` defaults to that path and authorizes it
for the initial account when the file exists. The daemon writes the file when it
generates, rotates or imports the key, and `SaveConfig` rewrites it from
`Config.AdminAuthorizedKey`. The file lives outside every flake, so a template
can import it without embedding the key or generating a user module.

A target provisioned by nix-connect receives the public key after install in
`/etc/ssh/authorized_keys.d/<account>` and in `<stateDir>/admin_ssh.pub`. A
template that imports the file keeps the key across rebuilds. A device installed
outside nix-connect must already authorize the public key; nix-connect does not
add it to a foreign device.

The shared key has one consequence: revocation is per key, not per administration
device. Rotating it is a manual operation that must update every target. This is
a binding limitation, not a bug.

### 8.3 Control pairing

Control pairing establishes mutual mTLS trust between an administration device
and a target. Both screens show a short code derived from both identity keys.
The administrator confirms, the target confirms, and each side stores a trust
record with the peer fingerprint, public key, last known hostname and address,
state, source and timestamps.

The public plane is unauthenticated, so first contact is recognition by short ID
and hostname. A relay on the LAN can present different keys to each side, which
the code comparison detects.

A headless target approves pairing through a local frontend on that device. A
target provisioned by nix-connect does not need a new pairing after install,
because the trust store is carried with the identity. A device installed outside
nix-connect and running only the daemon needs the CLI available for the initial
pairing, or must be provisioned by the deploytarget ISO. There is no safe
UI-less remote pairing against a LAN relay.

### 8.4 Key sharing between administration devices

The new administration device asks an existing one for the administration SSH
key. Both show a short code derived from both identity keys. The existing device
seals the private key to the new device's identity key and sends it over the
paired mTLS channel. The transfer never touches the public plane.

Pairing and key sharing use the same code and two-sided commit shape as
nix-builder-discovery. They carry different material: control pairing carries
trust records, key sharing carries the sealed private key.

### 8.5 Deploytarget ISO pre-pairing

The ISO embeds the administration device identity public key and the
administration SSH public key. It carries no private material. The target daemon
accepts the embedded identity as trusted on first boot, and the administration
device pins the target key on first contact.

The ISO device identity is generated at boot. It is never baked into the image.

## 9. Device model and registration

### 9.1 Device kinds

A device is `live` while running the deploytarget ISO and `installed` after a
successful install. The public info plane advertises the kind. The kind decides
which actions are available.

### 9.2 Registration record

Registration lives on the target daemon and is the single source of truth for
what the device is managed as. Fields:

- repository URL
- branch
- `nixosConfigurations` host attribute
- `installMode`: `pushed` or `no-commit`
- `updatesAllowed`: boolean
- reason when updates are disabled: `no-commit` or `push-failed`
- SSH user and SSH port
- configured account, used by default for SSH and consent
- last deployed revision
- timestamps

The public plane exposes only a boolean "registered". The full record is served
on the mTLS control plane to paired administration devices. The local daemon is
the only writer.

### 9.3 Registration transitions

A successful deploy or update moves the registration to the flake that was
deployed. On an installed device the move happens after activation and
verification, never before.

An install is the exception. The registration is staged into the new root before
the destructive phase so it survives the wipe, and the installed daemon adopts it
on first boot. A failure before the wipe discards the staged record with the live
environment.

A successful commit-mode deploy writes `installMode = pushed` and
`updatesAllowed = true`. A no-commit deploy, or a commit-mode deploy whose push
failed, writes `installMode = no-commit` and `updatesAllowed = false`.

A failed deploy or update leaves the previous record unchanged.

### 9.4 Identity and state carry-over

Before the destructive phase, the target's state directory is written into the
new root:

- device identity keypair
- trust records for paired administration devices
- registration record
- job record
- daemon configuration, including the configured account and ports

The installed system comes up as the same device with the same registration and
the same paired administrators. No re-pairing and no re-registration are needed
after an install.

## 10. Actions

| Device kind | Action | Implementation | Effect | Consent |
| --- | --- | --- | --- | --- |
| live | info | control plane | read-only details | pairing |
| live | ssh | system ssh with daemon agent socket | interactive session | SSH key |
| live | deploy | target-side disko and nixos-install | wipe, install, carry identity and registration | live account is NOPASSWD, so none |
| installed | info | control plane | read-only details | pairing |
| installed | ssh | system ssh with daemon agent socket | interactive session | SSH key, password for sudo |
| installed | deploy a different flake | deploy-rs | replace system configuration, keep user files, move registration on success | sudo gate |
| installed | update | deploy-rs from the registered flake | replace system configuration from the registered branch | sudo gate |
| installed | register, change, unregister | daemon state | change the flake association | sudo gate |
| installed, no-commit | publish | local git push | link the device to a new remote and enable updates | sudo gate |

A live device has no update and no registration actions. An installed device has
no destructive reinstall action. To wipe it, boot the deploytarget ISO.

Update and deploy-to-installed activations run with magic rollback by default
(section 15.1). Live installs do not use it, because there is no previous
generation to return to.

## 11. Consent gate

Every mutation must reproduce sudo's authorization decision for an account on the
target:

1. The target determines whether the configured account has NOPASSWD sudo. It
   checks by running a trivial sudo command as that account. Success means no
   password is required.
2. Otherwise the frontend prompts for a username and password. The username
   defaults to the advertised account. The target checks that the user is a
   member of `wheel` and authenticates the password through PAM using a
   dedicated service name.
3. Failures are rate-limited by the daemon and by the platform's PAM lockout.
4. The password is sent over mTLS, used once, then zeroed. It is never logged,
   persisted, or included in a job record.

The gate applies to deploy, update, register, change, unregister and publish.

The deploytarget ISO's live account has NOPASSWD sudo, so an ISO install requires
no password. This is not a special case; it is the same rule.

A device with no wheel account and no passwordless sudo cannot be mutated
remotely. That is intentional.

## 12. Discovery

Discovery reuses the nix-cache-discovery prober shape: configured CIDR ranges or
automatic subnet detection, a probe interval, a per-probe timeout, a worker
count, self-exclusion by identity, and a public-IP gate.

Each probe requests `GET /.nix-connect/v1/info` on the info port. A device is
listed if it returns a signed payload with a valid signature. An unpaired device
is still listed, because visibility is independent of trust.

Addresses are not assumed to be stable. The registry records the last known
address per fingerprint, and the control plane reconnects by fingerprint.

Discovery is event-driven for local state changes. A device starting or stopping
an install updates its advertised job summary without waiting for the next scan.

## 13. Control API and jobs

### 13.1 Transport

The local API is HTTP over a Unix socket at
`/run/nix-connect/control.sock`, group `nix-connect`, mode 0660. The remote API
is HTTPS on the control port with client certificates required. Both serve the
same `/v1` schema. Some endpoints are local-only, such as publish and ISO build.

### 13.2 Endpoints

- `GET /v1/status`
- `GET /v1/devices`
- `POST /v1/pair/start`
- `POST /v1/pair/confirm`
- `POST /v1/key/share`
- `POST /v1/devices/{id}/register`
- `POST /v1/devices/{id}/unregister`
- `POST /v1/devices/{id}/deploy`
- `POST /v1/devices/{id}/update`
- `POST /v1/devices/{id}/ssh`
- `GET /v1/jobs`
- `GET /v1/jobs/{id}`
- `GET /v1/jobs/{id}/events`
- `POST /v1/jobs/{id}/cancel`
- `POST /v1/jobs/{id}/confirm`
- `POST /v1/iso/deploytarget`
- `POST /v1/publish`

`POST /v1/devices/{id}/ssh` returns connection parameters and a short-lived agent
socket path. It does not proxy the session.

### 13.3 Job model

A job has an ID, kind, owner fingerprint, target fingerprint, phase, state,
revision, timestamps, a log ring buffer, a redacted error field, the previous
system path, the canary path, the confirmation deadline, the confirmer
fingerprint and the rollback outcome.

Kinds: `install`, `deploy`, `update`, `iso`, `publish`.

States: `queued`, `preparing`, `staged`, `copying`, `handing_off`, `installing`,
`activating`, `awaiting_confirmation`, `confirming`, `verifying`, `succeeded`,
`failed`, `rolled_back`, `cancelled`, `interrupted`.

An activation job for an installed device runs in a transient systemd unit so it
survives the daemon restart caused by the activation. The daemon records the
unit name, the canary path and the deadline before starting it.

Any paired administration device can confirm a job in `awaiting_confirmation`.
The target validates the job, the canary and the deadline, records the confirmer
and removes the canary. Confirmation does not repeat the consent gate, because
the activation was already authorized.

An install job is owned by the administration daemon during preparation. At
handoff it becomes owned by the target, and its record is written into the new
root with the identity. The new system marks it `succeeded` after verifying the
active system profile, or `failed` when verification does not match.

A deploy or update job is owned by the target from the start. The administration
device is only a viewer.

A daemon that starts and finds a job in a running state marks it `interrupted`,
unless it can verify that the active generation matches the job's staged
closure.

Logs are persisted per job, with passwords, passphrases and tokens redacted
before writing. The event stream is server-sent events with a sequence number so
a frontend can resume.

Cancel is offered before the destructive phase. Once disko starts, the UI does
not offer cancel, because a partially written disk is worse than a failed
install.

## 14. Install flow

This is the live-target path.

1. The administrator selects a discovered live device and a flake URL, branch
   and host.
2. The administrator answers the install prompts: disk, firmware, hostname,
   account, password or SSH keys, locale, keymap, timezone, swap, encryption and
   encryption passphrase. If the selected flake does not include the nix-connect
   daemon, the warning from section 15.3 applies before the destructive phase.
3. The administration daemon clones the flake, prepares it if needed, and builds
   the system closure.
4. The daemon copies the closure to the target over one SSH connection, then
   stages the identity, trust records, registration, job record and prepared
   tree.
5. The target acknowledges the handoff and takes ownership of the job.
6. The target runs disko and nixos-install locally as root. No SSH session is
   held during this phase.
7. The target reboots. The installed daemon reads the carried state, verifies
   the active generation against the staged closure, and marks the job
   succeeded.
8. The registration is written as part of step 4, so it is already present when
   the new system boots.

The install uses the nixos-anywhere phase sequence: closure copy, disko
partitioning, nixos-install and verification. It does not run the nixos-anywhere
orchestrator over SSH, because that would hold the administration session for
the destructive phase and defeat the target-owned job. The target runs the same
steps locally.

The administration device is required for steps 3 and 4. After handoff it can be
shut down. A failure before handoff leaves the target untouched and the job on
the administration device.

The encryption passphrase exists only in memory on the administration device and
the target during step 6. It is sent over mTLS, used by disko, and never written
to a file, the store or the log.

## 15. Deploy and update flows

### 15.1 Update an installed device

1. The administrator selects update and the confirmation mode, automatic or
   manual, or accepts the module default.
2. The target applies the consent gate.
3. The target fetches the latest commit on the registered branch, evaluates
   `nixosConfigurations.<host>` and stops if the host is absent.
4. The target captures the previous system path and builds the new system.
5. The target starts the deploy-rs activation in a transient systemd unit. With
   magic rollback on, the unit runs with `--magic-rollback --auto-rollback`, the
   job's confirm timeout and a job-scoped canary path.
6. After the profile switch the job becomes `awaiting_confirmation`.
7. Confirmation:
   - Automatic mode: an administration daemon polls the target's new control
     plane, verifies the identity and active generation, opens a one-shot SSH
     connection as the configured account and port, and calls the confirm
     endpoint when both checks pass.
   - Manual mode: the UI shows the control-plane and SSH check results and a
     countdown. The confirm action is enabled only after both checks pass. Any
     paired administration device can confirm, and the administrator still
     decides whether to keep the new generation.
8. Removing the canary completes the activation. The job verifies the new
   generation and succeeds.
9. If the canary is not removed before the deadline, the activation rolls back
   to the previous generation and the job fails.
10. On success the registration stays on the same repository, branch and host,
    and `last deployed revision` advances.

With magic rollback off, the administration device can leave immediately after
the request. With it on, the device is needed only for the confirmation window,
not for the build or activation.

Automatic confirmation is attempted by the initiating daemon and can be taken
over by any other paired administration daemon that sees the job. That is what
keeps the window survivable if the initiating device goes offline.

Magic rollback is on by default. Automatic mode uses a short timeout, proposed
at 60 seconds. Manual mode uses a much longer timeout, proposed at 10 minutes.
The module sets the default mode and both timeouts; the UI can override them per
job.

Magic rollback is automatically disabled when the new configuration does not
include the daemon, because there is no new daemon to accept the confirmation.
The UI states that it was disabled for that reason.

The SSH check uses the account and port the target advertises after activation.
It proves the administrator's normal path to the machine still works. If SSH does
not come back, the canary stays and the activation rolls back.

If reactivating the previous generation fails, the rollback wrapper sets the
previous generation as the boot generation, runs the bootloader update and
reboots. The job records that the fallback was used. The device returns on the
previous generation. A second failure requires physical access.

A reboot during the confirmation window ends the canary process. The job is
interrupted and the next boot may use the new generation, because the bootloader
was already updated. Magic rollback cannot cover that case.

### 15.2 Deploy a different flake to an installed device

This replaces the system configuration with a different flake while keeping user
files and the disk layout.

1. The administrator selects deploy and a flake URL, branch and host.
2. If a registration exists and differs, the UI warns that the system
   configuration will be replaced and names the current registration.
3. The target applies the consent gate.
4. If the selected flake does not include the nix-connect daemon, the UI warns
   that the device will leave the network after activation and requires an
   explicit confirmation.
5. The target fetches the flake, evaluates `nixosConfigurations.<host>`, builds
   and runs the deploy-rs activation profile locally.
6. The magic rollback rules in section 15.1 apply to this activation, including
   automatic disable when the new configuration lacks the daemon.
7. On success, and when the new configuration includes the daemon, the
   registration moves to the new flake with `installMode = pushed`.

If the new configuration omits the daemon, the job cannot record the move after
activation. The device leaves the network and the last record is stale. The
confirmation in step 4 states this.

There is no kexec and no disk wipe. A destructive reinstall is only available by
booting the deploytarget ISO.

### 15.3 Daemon presence

A deployed configuration that lacks the daemon drops off the network after
activation. The job record remains on the target's state directory, but no daemon
reports it. The device disappears from the list and cannot be updated. The
confirmation in step 4 states this.

## 16. Ojuny preparation, no-commit and publish

### 16.1 Adapter selection

The adapter is chosen by capability and by operation:

1. For an install, the Ojuny preparation path wins when the flake exposes the
   preparation package and template modules.
2. Otherwise an install uses the generic wrapper.
3. For an update or a deploy to an installed device, `deploy.nodes.<host>` is
   used directly when present.
4. Otherwise the target builds a local wrapper that supplies the deploy-rs
   activation profile.

### 16.2 Ojuny preparation

The Ojuny template has no disko by design, and the Ojuny installer uses disko for
both ISO paths. The prepared hardware module is therefore required for an
install and for later updates.

The flow:

1. Clone the registered URL and branch into a job directory.
2. Create a git backup ref at the pre-edit HEAD.
3. Run the template's preparation with the install prompt values.
4. The preparation writes the target hardware and disko module, the bootloader,
   the user module with the administrator's public key, the host imports, and
   `ojuny.network.deploy-rs.enable = true`.
5. In commit mode, stage only the files the preparation touched, commit with the
   user's git identity, and push the current branch.
6. Deploy from the pushed revision in commit mode, or from the local prepared
   tree in no-commit mode.
7. The target receives the prepared tree, including its git history, in the home
   directory at the Ojuny repository path.

The target's local checkout matches what was installed, so local rebuilds and the
settings GUI work.

### 16.3 No-commit mode

No-commit is for testing, for deploying a repository that must not be modified,
and for pre-installed devices that will be linked to an owner's remote later.

1. The UI shows an "are you sure?" confirmation before anything starts. It states
   that the remote will not contain the device configuration and that updates
   will be disabled.
2. The preparation still runs and the prepared tree still reaches the target.
3. The preparation changes remain uncommitted in the local tree. Nothing is
   pushed to the registered remote.
4. The registration records `installMode = no-commit` and
   `updatesAllowed = false`.
5. A later push done outside nix-connect does not clear the flag.

The target's checkout carries the preparation as uncommitted changes. Publish
commits them when the device is linked to a new remote.

A commit-mode push failure produces the same result after a second confirmation.
The administrator can continue with a non-updatable device or abort.

No-commit is install-only. An installed target's update pulls from the registered
remote, so a revision that was never pushed cannot be an update.

### 16.4 Publish

Publish is the supported way to link a no-commit device to its owner's remote. It
runs locally on the device, as the invoking user, using that user's normal git
authentication.

1. Verify the registration is in no-commit mode.
2. If the checkout has uncommitted preparation changes, commit them with the
   invoking user's git identity.
3. Instruct the user to create an empty repository on a git host, with no
   README, license or initial commit.
4. Prompt for the URL and verify with `git ls-remote` that the repository has no
   refs.
5. Add the URL as a remote, push the current branch, and set its upstream.
6. Verify the remote ref equals the local commit.
7. Update the registration to the new URL and branch, set
   `installMode = pushed` and `updatesAllowed = true`.

The existing upstream remote is kept, so the original template remains available
for diffs. Any failure leaves the registration unchanged. The consent gate
applies because the registration changes.

## 17. Generic flakes

A generic flake has no Ojuny preparation. Its install uses an ephemeral wrapper
flake:

- The remote flake is an input.
- The wrapper exposes `nixosConfigurations.<name>` by extending the target
  configuration with a generated disko and bootloader module.
- The wrapper also exposes `deploy.nodes.<name>` for deploy-rs.
- The wrapper is never pushed.
- The wrapper does not generate a user module and does not set
  `users.users.<account>`.

The installed system is system-only. There is no Ojuny flake in the home and no
generated user module. A generic flake that wants to stay managed must include
the nix-connect NixOS module as an input and enable it. nix-connect does not
inject the daemon into the wrapper.

nix-connect pins its own deploy-rs and disko inputs, so the user's flake does not
need them.

Updates use an ephemeral local wrapper on the target. The wrapper imports the
remote flake and supplies the deploy-rs activation profile when the flake does
not declare `deploy.nodes`. It does not invent missing filesystem or boot
settings, so a remote configuration that cannot evaluate on its own cannot be
updated.

A generic install registers the remote flake as `pushed`, because the remote is
the source of the configuration and the installation wrapper is not part of it.
Updates are allowed subject to the preflight.

The administrator public key is written after `nixos-install` to
`/etc/ssh/authorized_keys.d/<account>` and to
`<root>/var/lib/nix-connect/admin_ssh.pub`. A template that imports the file
keeps the key across rebuilds. If the user's flake does not import the file, a
later rebuild can replace the authorized keys file and remove SSH access. The
design recommends that the flake import the key file, and documents the
consequence.

## 18. Deploytarget ISO

A minimal NixOS live image:

- the nix-connect daemon enabled, discovery auto, public and control planes up;
- SSH enabled with the embedded administration public key;
- DHCP on wired interfaces;
- NetworkManager for `nmtui` on Wi-Fi;
- auto-login on tty1 with a message explaining that `nmtui` connects Wi-Fi;
- passwordless sudo for the live account, so the consent gate permits installs;
- no desktop, no installer, no Ojuny Calamares;
- disko and nixos-install available for the target-side install;
- block devices reported through the control plane;
- the embedded administration identity public key and SSH public key;
- identity generated at boot, never baked into the image.

Wired behavior is zero interaction: boot the target and it appears in
nix-connect. Wi-Fi behavior requires `nmtui`, nothing else.

There is no boot-time consent code. Pre-pairing is the consent. Physical boot plus
Secure Boot disabled is the security boundary, and a correct Secure Boot setup
with a UEFI password is what prevents an unauthorized person from booting the
image on a machine they do not own.

The ISO is built by an administration daemon as a job, so both frontends show
progress. It is per administration device because of the embedded keys. Rotating
the administration key invalidates existing images.

## 19. Handover boundary

Handover is a separate feature for system integrators and is not implemented in
nix-connect. The integrator installs with nix-connect, tests and modifies the
device, then enables handover mode before shipping. Handover takes the
recipient's details and regenerates the user module.

Handover must leave the device in a state publish can use:

- reset the registration to `installMode = no-commit` with
  `updatesAllowed = false`, so the recipient does not inherit the integrator's
  remote;
- leave the checkout committed or clean, with the regenerated user module
  included.

The recipient then runs publish to link their own remote.

## 20. Security and threat model

- The public plane is unauthenticated by design. It is signed, which proves key
  possession and binds the fields to the key, not the hostname.
- Trust is per device, explicit and revocable. A lost administration device
  requires rotating the shared key on every target.
- The control plane authenticates both sides with certificates pinned to the
  device identity.
- The shared administration key means one compromised administration device
  compromises every target that trusts that key.
- The consent gate is proof of an account on the target. On a device with
  passwordless sudo it reduces to pairing, which is the chosen behavior.
- The ISO's physical boot is the consent. Secure Boot off is required, and the
  documented boundary assumes a correctly configured UEFI password and Secure
  Boot enabled after install.
- The encryption passphrase transits to the target because the target runs disko.
  It is never persisted, but a compromised target during install sees it.
- Deploy logs can contain store paths and build output. They are never public and
  are redacted before persistence.
- nix-connect does not read, store or manage git credentials. git runs as the
  user with the user's environment.
- A first-contact relay on the LAN is detectable by comparing the short ID on
  both screens where the target has one. A daemon-only target must be provisioned
  or paired with a local frontend.
- A paired administration device can request a mutation but cannot bypass the
  consent gate on a device without NOPASSWD sudo.
- Any paired administration device can confirm a magic rollback job.
  Confirmation does not repeat the consent gate because the activation was
  already authorized.
- The SSH health check proves the administrator's access path, not that every
  service is healthy.
- A compromised administration device can confirm a broken configuration, but it
  already passed the gate.
- A reboot during the confirmation window bypasses magic rollback.

## 21. Failure modes

| Condition | Result |
| --- | --- |
| No daemon on the administration device | Frontends refuse to start and name the missing service |
| No peers discovered | Empty list, discovery continues |
| Pairing not confirmed | Nothing is stored; the request expires |
| Key sharing not confirmed | No key is transferred |
| Push rejected | Warning, optional no-commit install, updates disabled |
| Remote branch lacks the host | Update and deploy stop before building |
| Build fails | Job fails, target untouched |
| Activation fails before confirmation | Activation rolls back, job fails, registration unchanged |
| Confirmation timeout | Activation rolls back, job fails |
| SSH health check fails | No confirmation, activation rolls back |
| Confirmation after the deadline | Rejected, rollback proceeds |
| No administration device during confirmation | Activation rolls back |
| Daemon-less configuration | Magic rollback automatically disabled, warning shown |
| Rollback reactivation fails | Boot generation set to previous, reboot, job records the fallback |
| Reboot during the confirmation window | Job interrupted, next boot may use the new generation |
| Copy fails | Job fails before handoff, target untouched |
| Install fails before disko | Target untouched, retry is safe |
| Install fails during disko | Target disk is partially written, physical access is required |
| Target reboots mid-install | Job record is lost with the live environment, device does not appear |
| New system fails to boot | Device does not return, admin sees the job unresolved |
| Identity mismatch after boot | Daemon refuses to adopt the carried state and reports it |
| Wrong account | Consent gate fails, mutation refused |
| No wheel account and no NOPASSWD | Remote mutation is impossible, local fix required |
| Password lockout | Consent gate refuses until the lockout clears |
| Deployed flake lacks the daemon | Device leaves the network after activation |
| No-commit registration | Updates are disabled until publish or re-registration |
| Secure Boot enabled | The deploytarget ISO does not boot |
| Wi-Fi without nmtui | Device does not appear |
| Administration device powers off before handoff | Job is lost before the install starts |

## 22. Testing and CI gates

### 22.1 Unit tests

- identity generation, fingerprint stability, key permissions
- info payload signing and verification, including tampering
- discovery scanning, self-exclusion and the public-IP gate
- trust records, pairing state machine, timeouts and two-sided commit
- key sharing and sealed transfer
- registration transitions, including no-commit and publish
- consent gate: NOPASSWD detection, wheel check, PAM failure and rate limiting
- job state machine, interruption on restart and log redaction
- confirmation endpoint validation: paired admin, canary, deadline and replay
- magic rollback auto-disable when the configuration lacks the daemon
- rollback fallback wrapper and boot generation selection
- adapter selection and wrapper generation
- update preflight when the remote lacks the host

### 22.2 VM tests

Three-node and ISO tests:

- pair an admin and a target, fetch information and open SSH
- boot the deploytarget ISO, install a flake, verify identity and registration
  survive the reboot
- install with no-commit, verify updates are disabled, run publish to a new
  repository, verify updates are enabled
- commit-mode push failure produces a no-commit device with a warning
- update an installed device and keep user files
- magic rollback with automatic confirmation
- magic rollback timeout and rollback to the previous generation
- magic rollback with a failing SSH check
- manual confirmation from a second administration device
- rollback fallback sets the previous boot generation and reboots
- daemon-less deploy disables magic rollback with a warning
- deploy a different flake and move the registration on success
- deploy a flake without the daemon and verify the device disappears
- wrong password, absent wheel account and NOPASSWD account
- admin device shuts down after handoff and the install completes
- target reboots mid-install and the job does not claim success
- public info contains no flake URL and no account name

### 22.3 CI gates

- `go test ./...`, `go vet ./...`, gofmt
- `nix flake check` and the package builds
- the deploytarget ISO build and a QEMU boot test
- no private key material in the repository or in the ISO
- the public payload schema rejects flake URLs and account names
- a static check that `@option` annotations are attached only to plain
  assignments or direct `lib.mkIf` attribute sets, using the rules in
  `ojuny-template/docs/05-settings-gui-limitations.md`,
  `nix-settings-gui/docs/comment-syntax.md` and section 5 of this document
- a test that no install job opens an SSH session for the duration of disko or
  nixos-install

## 23. Repository layout and module surface

Proposed layout:

```text
nix-connect/
  cmd/nixconnect-daemon/
  cmd/nixconnect/
  cmd/nixconnect-gui/
  internal/
    discovery/
    identity/
    pairing/
    trust/
    registry/
    jobs/
    deploy/
    ojuny/
    httpapi/
    userexec/
    iso/
  nix/
    module.nix
    iso.nix
    packages.nix
  docs/
    design.md
  tests/
    unit/
    vm/
```

NixOS module options:

- `enable`
- `infoPort`, `controlPort`
- `discovery.ranges`, `discovery.auto`, `discovery.interval`, `discovery.timeout`,
  `discovery.workers`
- `trustedNetworks`, `publicIpUrl`, `trustCheckInterval`
- `stateDir`, default `/var/lib/nix-connect`
- `pamService`
- `initialAccount`, which seeds the runtime configured account on first start
- `magicRollback.enable`, default `true`
- `magicRollback.mode`, default `automatic`
- `magicRollback.automaticTimeout`, default 60 seconds
- `magicRollback.manualTimeout`, default 600 seconds
- `openFirewall`
- `package`
- `gui.enable`, `cli.enable`

If the module is exposed through nix-settings-gui, annotated options follow the
documented shape rules. The CI gate in section 22.3 enforces it.

## 24. Binding limitations

1. The shared administration key cannot be revoked per device. Rotation is
   manual and fleet-wide.
2. There is no kexec. A destructive reinstall requires booting the deploytarget
   ISO.
3. The deploytarget ISO requires Secure Boot to be disabled. Secure Boot is a
   post-install flow.
4. Private repositories are unsupported. Deployed flakes are public git URLs.
5. Handover is out of scope and belongs to a separate feature.
6. A no-commit device is not updatable until published or re-registered.
7. A generic target whose deployed flake lacks the daemon leaves the network and
   cannot be updated.
8. A target reboot during install loses the live job record. The job never
   claims success.
9. Pairing a target requires local approval. A target provisioned by nix-connect
   or booted from the pre-paired ISO skips it. A device installed outside
   nix-connect and running only the daemon needs the CLI for the initial
   pairing.
10. The encryption passphrase transits to the target during install.
11. Update requires the remote branch to contain `nixosConfigurations.<host>`
    and the boot and filesystem settings needed to evaluate.
12. Multi-host module naming in an Ojuny repository is the user's
    responsibility. nix-connect prepares the host it is given.
13. nix-connect does not manage git credentials, repository visibility or
    conflict resolution. It adds a remote during publish and commits and pushes
    during preparation.
14. For an install, an administration device is required until handoff. It can
    leave after the target owns the install job. An update with magic rollback
    requires an administration device for the confirmation window.
15. The ISO is x86_64-linux only.
16. Wi-Fi on a target requires `nmtui`.
17. The administrator SSH key is written after install. A template that does not
    import the key file can lose SSH access on the next rebuild.
18. The ISO is per administration device and is invalidated by key rotation.
19. A device with no wheel account and no passwordless sudo cannot be mutated
    remotely.
20. The public plane reveals hostnames, addresses and SSH ports by design.
21. Magic rollback requires an administration device to remain reachable for the
    confirmation window. Without one, the update rolls back.
22. Magic rollback is automatically disabled when the installed configuration
    does not include the daemon.
23. The SSH health check requires SSH and the configured account to remain
    reachable. A configuration that intentionally removes SSH triggers a
    rollback.
24. If reactivating the previous generation fails, the fallback sets it as the
    boot generation and reboots. A second failure requires physical access.
25. A reboot during the confirmation window ends magic rollback. The next boot
    may use the new generation.

## 25. References

- `nix-builder-discovery/docs/design.md`: public plane, trust plane, mTLS,
  identity, pairing, control socket, security model and testing shape.
- `nix-cache-discovery/README.md`: LAN-only scope, discovery ranges and
  automatic subnet detection, public-IP filter.
- `nix-cache-discovery/internal/discovery/discovery.go`: prober API, workers,
  timeouts, self-exclusion.
- `ojuny-template/docs/00-overview.md` lines 98 to 99: the template has no disko,
  and the Ojuny installer uses disko for both ISO paths.
- `ojuny-template/docs/12-flake-module-contract.md` lines 9 to 19 and 39 to 44:
  generated module names and `ojuny.repoPath`.
- `ojuny-template/scripts/prepare-remote-repository.sh` lines 202 to 278: the
  generated disko hardware module, bootloader and deploy-rs enablement.
- `ojuny-template/modules/backend/01-system/14-deploy-rs.nix` lines 7 to 25:
  SSH, disko, deploy-rs and nixos-anywhere on the target.
- `ojuny-installer/docs/remote-deployment.md`: deploy-rs for installed targets,
  nixos-anywhere for machines without NixOS.
- `ojuny-installer/docs/unattended-install.md` lines 45 to 67, 81 to 93, 104 to
  118 and 149 to 150: install flow, settings, disk selection and LUKS handling.
- `ojuny-installer/docs/attended-install.md` lines 13 to 14 and 35 to 36: the
  attended page sequence and swap choices.
- `nixos-configs/dendrite/modules/hosts/deploy.nix`: ssh user, root activation,
  per-host ports and timeouts.
- `nixos-anywhere` `docs/quickstart.md` lines 26 and 121, and
  `src/nixos-anywhere.sh` lines 606 to 613: non-root installs require
  passwordless sudo.
- deploy-rs `src/bin/activate.rs` and `src/deploy.rs`: canary file,
  confirmation by file removal, confirm timeout and auto-rollback.
