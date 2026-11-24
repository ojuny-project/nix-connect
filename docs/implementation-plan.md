# nix-connect implementation plan

Status: every phase is implemented except the local-UID/service-user model and
the user-execution item. The VM tests, the package builds and the deploytarget
ISO boot check pass.

This plan turns `docs/design.md` into the implementation. It is ordered so every
phase leaves a compiling repository. Each phase is one unit of work with its own
verification.

## Scope

Deliver:

- a Go module with the daemon, CLI/TUI and the GTK4 GUI
- a Nix flake with a devshell containing Go, gcc, pkg-config, GTK4, libadwaita,
  git, openssh, pamtester, deploy-rs, disko and nixos-anywhere
- the device identity, trust, registration and job state
- the public info plane, discovery and the local control API
- the control plane with mTLS and client-certificate pinning
- pairing and administration key sharing
- the Ojuny preparation path, no-commit mode and publish
- admin-to-target forwarding and remote job aggregation
- target-side deploy with disko and nixos-install
- target-side update with deploy-rs activation
- magic rollback with admin confirmation, SSH health check and the reboot
  fallback
- registration transitions on successful deploy and update
- a NixOS module and a deploytarget ISO expression
- unit, forwarding and NixOS tests

Non-goals:

- a complete Calamares-quality installer UI
- a signed ISO
- private repositories
- handover
- multi-host Ojuny repositories

## Acceptance criteria

1. `nix develop -c go build ./...` succeeds.
2. `nix develop -c go test ./...` succeeds.
3. `nix develop -c go vet ./...` succeeds.
4. `nix develop -c gofmt -l .` prints nothing.
5. `nix flake check` evaluates the flake and builds the packages.
6. The NixOS module evaluates for a fixture host.
7. The deploytarget ISO expression evaluates. Building the full image is left to
   CI because of its size.

## Phase 0: Toolchain and skeleton

1. Add `flake.nix` with the devshell and package outputs.
2. Add `go.mod`, `.gitignore` and the directory layout from the design.
3. Add `nix/packages.nix`, `nix/module.nix` and `nix/iso.nix` as compiling
   skeletons.
4. Run `nix develop -c go version` and record the toolchain version.

Verification: the devshell opens and Go is on PATH.

## Phase 1: Identity and state

Files:

- `internal/identity/identity.go`
- `internal/state/state.go`
- `internal/registration/registration.go`
- `internal/trust/trust.go`
- tests for each

Work:

1. Generate and load Ed25519 device identities and the administration SSH key.
2. Derive the fingerprint and short ID.
3. Generate the self-signed TLS certificate from the identity.
4. Implement atomic JSON state writes under `/var/lib/nix-connect`.
5. Implement trust records and registration transitions, including `installMode`,
   `updatesAllowed`, branch and host.

Verification: unit tests for key permissions, fingerprints, atomic writes and
registration transitions.

## Phase 2: Info plane and discovery

Files:

- `internal/info/info.go`
- `internal/discovery/discovery.go`
- `internal/registry/registry.go`

Work:

1. Sign and verify the public info payload.
2. Serve `GET /.nix-connect/v1/info` on port 5060.
3. Implement CIDR and automatic subnet discovery with workers, timeouts,
   self-exclusion and a public-IP gate interface.
4. Keep the last known address and job summary per fingerprint.

Verification: `httptest` tests for signing, tampering, discovery and
self-exclusion.

## Phase 3: Control API, pairing and key sharing

Files:

- `internal/httpapi/server.go`
- `internal/httpapi/client.go`
- `internal/pairing/pairing.go`
- `internal/agent/agent.go`
- `internal/consent/consent.go`

Work:

1. Serve the local API on `/run/nix-connect/control.sock` and the mTLS API on
   port 5061.
2. Pin client certificates to trusted fingerprints.
3. Implement status, devices, register, unregister, deploy, update, ssh, jobs,
   confirm, iso and publish endpoints.
4. Implement the two-sided pairing code and the sealed administration key
   transfer.
5. Serve the administration key through an ssh-agent socket.
6. Implement the consent gate: NOPASSWD detection and PAM through `pamtester`.

Verification: unit tests for the pairing code, certificate pinning, consent
decisions and the API client.

## Phase 4: Jobs and deployment engines

Files:

- `internal/jobs/jobs.go`
- `internal/deploy/install.go`
- `internal/deploy/activation.go`
- `internal/deploy/wrapper.go`
- `internal/userexec/userexec.go`

Work:

1. Implement job records, phases, states, logs, SSE events and interruption on
   restart.
2. Implement the install prepare phase: clone, build, copy and stage identity,
   trust, registration and job state.
3. Implement the target-side install command.
4. Implement the activation unit, the deploy-rs canary, the confirmation
   endpoint, the SSH health check and the rollback fallback.
5. Implement the generic wrapper flake and the disko module.
6. Run git as the calling user and target commands as root.

Verification: unit tests for job transitions, canary handling, confirmation
deadlines and the rollback wrapper, using a fake runner.

## Phase 5: Ojuny preparation and publish

Files:

- `internal/ojuny/prepare.go`
- `internal/ojuny/publish.go`
- `internal/ojuny/backup.go`

Work:

1. Clone the remote branch into a job directory.
2. Create the backup ref at the pre-edit HEAD.
3. Run the template preparation or the built-in module generator.
4. Stage only the touched files and commit with the user's identity.
5. Push the current branch, or skip commit and push in no-commit mode.
6. Implement publish: verify no-commit, commit pending changes, verify an empty
   remote, push and update the registration.

Verification: tests against temporary git repositories, including push failure,
no-commit and publish.

## Phase 6: CLI and TUI

Files:

- `cmd/nixconnect/main.go`
- `internal/cli/cli.go`
- `internal/tui/tui.go`

Work:

1. Implement the commands from section 6.1 of the design.
2. Make no arguments open the TUI.
3. Implement the device list, action menu, job progress and confirmation prompts.
4. Add the `ncon` symlink in the Nix package.

Verification: `go build` and a scripted client test against a fake API.

## Phase 7: GUI

Files:

- `cmd/nixconnect-gui/main.go`
- `internal/gui/gui.go`

Work:

1. Open a GTK4 window with the device list.
2. Add actions for info, SSH, deploy, update, register and unregister.
3. Show job state and the manual magic rollback confirmation.
4. Reuse the same client package as the TUI.

Verification: cgo build succeeds in the devshell.

## Phase 8: NixOS module and ISO

Files:

- `nix/module.nix`
- `nix/iso.nix`
- `nix/packages.nix`
- `flake.nix`

Work:

1. Declare the module options from section 23.
2. Create the service user, group, state directory, socket and systemd unit.
3. Add the PAM service and firewall rules.
4. Build the deploytarget ISO expression with the daemon, SSH, DHCP,
   NetworkManager, auto-login and the embedded administration keys.
5. Wire the package outputs.

Verification: `nix flake check` and a module evaluation for a fixture host.

## Phase 9: Final verification

1. Run `nix develop -c gofmt -w .`.
2. Run `nix develop -c go vet ./...`.
3. Run `nix develop -c go test ./...`.
4. Run `nix develop -c go build ./...`.
5. Run `nix flake check`.
6. Update `README.md` with the build, run and test commands.

## Implementation results

Completed:

- `flake.nix` with the devshell and the three package outputs.
- Go module and the directory layout.
- Device identity, administration key, TLS certificate and fingerprints.
- Atomic config, registration and trust persistence.
- Signed public info payload and the info server.
- CIDR and automatic subnet discovery with self-exclusion.
- Local control socket and the mTLS control plane with peer fingerprinting.
- Pairing codes, the two-sided request manager and the sealed administration
  key envelope.
- Consent gate with the NOPASSWD shortcut, wheel membership and PAM through
  `pamtester`.
- Job records, phases, logs, persistence and interruption on restart.
- Ojuny module generation, backup refs, commit, push and publish.
- Deploy-rs canary helpers, generic wrapper generation, install command helpers,
  activation helper and rollback script.
- CLI commands, the bubbletea TUI and the GTK4 client source.
- NixOS module with service, state and runtime directories, group membership,
  PAM service, firewall and pre-pairing support.
- Deploytarget ISO expression.
- Nix package build with runtime tool wrappers, and a vendor hash.
- Unit tests for identity, info, discovery, pairing, registration, state,
  consent, jobs, deploy helpers and the Ojuny adapter.
- A local integration run that starts the daemon, queries status and devices,
  and shuts down.

Remaining:

- Identify the local caller by UID and run the daemon as a service user. The
  daemon currently runs as root and treats every local socket caller as
  `local`; the module creates the `nix-connect` group but no service user. A
  privileged helper is required to keep disko, nixos-install and target writes
  working as root.
- Run git as the calling user. Git currently runs as the daemon user; the
  design's public-repository-only scope means no credentials are involved, but
  the `internal/userexec` ownership model is not implemented.

Completed after the first pass:

- The GTK4 client builds with cgo in the devshell and carries the full action
  set; the TUI and GUI match the CLI for deploy, update, register, unregister,
  publish, SSH, pairing, jobs, trust, reset, rotation and key sharing.
- The deploytarget ISO expression and the runtime ISO job build.
- The two-node VM test passes, including two-sided pairing, mutual trust, key
  sharing, rotation and bidirectional trust revocation.
- Key rotation and the trust-record UI flows.
- The handover-adjacent registration reset interface.
- Remote registration and unregistration forwarding.
- Publish is wired to the job engine: it verifies no-commit, commits pending
  changes, checks that the new remote is empty, pushes, verifies the remote head
  and updates the registration.
- SSH key propagation on update.
- The deploy-rs canary is created by deploy-rs after activation; the daemon
  waits for it, then opens the confirmation window.

## Deploy and update implementation

The two core actions are wired end to end.

Deploy:

- the admin daemon forwards the request to the target control plane over mTLS
- if the flake exposes `prepare-remote-repository`, the target clones it, runs
  the template preparation, builds from the prepared tree and stages that tree
  into the target home at `repos/nix-flake`; otherwise the flake is deployed
  as-is
- the target daemon builds a wrapper flake with disko, the bootloader and the
  administration SSH key, runs disko and nixos-install locally, stages the
  identity, trust, config, registration and job record into the new root, marks
  the job succeeded and reboots
- the installed system returns as the same device with the same registration

Update:

- the admin daemon forwards the request to the target control plane
- the target daemon builds a wrapper flake, builds the deploy-rs activation and
  the system closure, and starts the activation in a transient systemd unit
- the activation uses the deploy-rs canary with magic rollback and auto-rollback
- the admin daemon performs the control-plane and SSH health checks and confirms
  automatically in automatic mode
- the job survives a daemon restart in the awaiting and confirming states
- the registration remains on the same repository, branch and host

## Pairing, key rotation and trust

Pairing is two-sided. The administration device forwards the request to the
target over mTLS. The target lists the pending request with `nixconnect pair
pending` or in the TUI trust view. Each side confirms locally with the same
short code derived from both identity fingerprints; neither side can confirm on
behalf of the other.

Key rotation and sharing:

- `nixconnect key rotate` generates a new administration key, replaces the
  agent keyring and records the new public key in `config.json`.
- `nixconnect key share <id>` generates an ephemeral X25519 key, obtains the
  administration key sealed by a paired device, decrypts it with the pairing
  code and installs it.
- Existing deploytarget ISO images become invalid after either operation.

Trust records are listed in the TUI and GUI. Revoking a record removes it
locally and notifies the peer over mTLS, so both stores drop the pair. The
registration reset interface sets `installMode = no-commit` with updates
disabled and is available in the CLI, TUI and GUI.

## Deploytarget ISO job

`nixconnect build-iso` runs the ISO build as a job. It copies the installed
`nixconnect-source`, writes `prepaired.json` with the administration identity
fingerprint, identity public key and SSH public key, and builds
`nixosConfigurations.deploytarget.config.system.build.isoImage` from the copy.
The build uses `nix build -o <out-link>`, so the result is a Nix out-link that
points to the result directory containing `iso/deploytarget-<short-id>.iso`. The
default out-link is `deploy-target-iso` in the caller's current directory;
`--output PATH` or `-o PATH` overrides it. The daemon does not copy the image.
The same expression builds the generic image directly from the flake.

Remote jobs are recorded in `remote_jobs.json` on the administration device and
combined with local jobs in the job list.

The CLI deploy command now carries the target disk, account, administration key
and firmware. The administration key is filled from the daemon when omitted.

## Test implementation results

Automated Go tests now cover identity, info, discovery, registry, state,
registration, trust, pairing, consent, jobs, deploy, ojuny, httpapi, service,
cli and tui.

Verified:

- `nix develop --command go test ./...` passes for every package.
- `nix develop --command go vet ./internal/... ./cmd/...` is clean.
- `nix develop --command gofmt -l .` prints nothing.
- `nix develop --command go build ./...` succeeds, including the GTK4 client.
- `nix build .#checks.x86_64-linux.unit-tests` passes.
- `nix build .#checks.x86_64-linux.nixconnect` passes.
- `nix build .#checks.x86_64-linux.single-node` passes.
- `nix build .#checks.x86_64-linux.two-node` passes.
- `nix build .#packages.x86_64-linux.nixconnect-gui` passes.
- `nix flake check` passes, including the deploytarget configuration and both VM
  test derivations.
- The deploytarget ISO builds and boots under QEMU; the forwarded public info
  endpoint returns the signed payload for the live device.

Added:

- `packages.nixconnect-tests`, which runs `go test ./internal/...` inside the
  Nix sandbox.
- `checks.unit-tests`, `checks.single-node` and `checks.two-node`.
- `nix/tests/single-node.nix` and `nix/tests/two-node.nix`.

The single-node and two-node NixOS VM tests pass. The two-node test exercises
discovery, two-sided pairing, mutual trust records, key sharing, rotation and
bidirectional trust revocation.

The CLI now accepts flags after the device id for `register`, `unregister` and
`deploy`.

The nix-connect coverage file was refreshed after test implementation and marks
232 tests as existing. The remaining `existing: no` entries are mostly VM,
install, ISO, GUI and failure-path coverage that needs a quiet machine or a
second node.
