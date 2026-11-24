package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ojuny-project/nix-connect/internal/agent"
	"github.com/ojuny-project/nix-connect/internal/consent"
	"github.com/ojuny-project/nix-connect/internal/discovery"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/info"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/registry"
	"github.com/ojuny-project/nix-connect/internal/service"
	"github.com/ojuny-project/nix-connect/internal/state"
)

const version = "0.1.0"

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("nix-connect failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		stateDir         = flag.String("state-dir", "/var/lib/nix-connect", "state directory")
		infoPort         = flag.Int("info-port", 0, "public info port")
		controlPort      = flag.Int("control-port", 0, "mTLS control port")
		socketPath       = flag.String("socket", "/run/nix-connect/control.sock", "local control socket")
		agentSocket      = flag.String("agent-socket", "/run/nix-connect/agent.sock", "administration ssh-agent socket")
		kind             = flag.String("kind", "", "device kind: installed or live")
		discoveryAuto    = flag.Bool("discovery-auto", true, "discover the local subnet")
		discoveryRanges  stringList
		discoveryEvery   = flag.Int("discovery-interval", 60, "discovery interval in seconds")
		discoveryTimeout = flag.Int("discovery-timeout", 2, "probe timeout in seconds")
		discoveryWorkers = flag.Int("discovery-workers", 64, "discovery worker count")
		pamService       = flag.String("pam-service", "", "PAM service name")
		account          = flag.String("account", "", "default account")
		sshUser          = flag.String("ssh-user", "", "default SSH user")
		sshPort          = flag.Int("ssh-port", 0, "default SSH port")
		magicEnabled     = flag.Bool("magic-rollback", true, "enable magic rollback")
		magicMode        = flag.String("magic-mode", "", "magic rollback mode: automatic or manual")
		magicAuto        = flag.Int("magic-auto-timeout", 0, "automatic confirmation timeout")
		magicManual      = flag.Int("magic-manual-timeout", 0, "manual confirmation timeout")
		isoSource        = flag.String("iso-source", "", "nix-connect flake source used to build the deploytarget ISO")
		showVersion      = flag.Bool("version", false, "print the version")
	)
	flag.Var(&discoveryRanges, "discovery-range", "CIDR range to scan, repeatable")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	store, err := state.Open(*stateDir)
	if err != nil {
		return err
	}
	config := store.Config
	if *kind != "" {
		config.Kind = *kind
	}
	if *infoPort > 0 {
		config.InfoPort = *infoPort
	}
	if *controlPort > 0 {
		config.ControlPort = *controlPort
	}
	if *pamService != "" {
		config.PAMService = *pamService
	}
	if *account != "" {
		config.Account = *account
	}
	if *sshUser != "" {
		config.SSHUser = *sshUser
	}
	if *sshPort > 0 {
		config.SSHPort = *sshPort
	}
	config.MagicRollback.Enable = *magicEnabled
	if *magicMode != "" {
		config.MagicRollback.Mode = *magicMode
	}
	if *magicAuto > 0 {
		config.MagicRollback.AutomaticTimeout = *magicAuto
	}
	if *magicManual > 0 {
		config.MagicRollback.ManualTimeout = *magicManual
	}
	config.Discovery.Auto = *discoveryAuto
	if len(discoveryRanges) > 0 {
		config.Discovery.Ranges = discoveryRanges
	}
	config.Discovery.Interval = *discoveryEvery
	config.Discovery.Timeout = *discoveryTimeout
	config.Discovery.Workers = *discoveryWorkers
	config.Normalize()
	store.Config = config
	if err := store.SaveConfig(); err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	agentServer, err := agent.Start(*agentSocket, store.AdminKey)
	if err != nil {
		fallback := filepath.Join(*stateDir, "agent.sock")
		agentServer, err = agent.Start(fallback, store.AdminKey)
		if err != nil {
			return err
		}
		*agentSocket = fallback
	}
	defer agentServer.Close()

	jobManager, err := jobs.New(filepath.Join(*stateDir, "jobs"))
	if err != nil {
		return err
	}
	peerRegistry := registry.New()
	pairingManager := pairing.New(store.Identity.Fingerprint())
	gate := consent.New(store.Config.PAMService)

	core := &service.Service{
		Store:       store,
		JobManager:  jobManager,
		Registry:    peerRegistry,
		Pairing:     pairingManager,
		Gate:        gate,
		AgentSocket: *agentSocket,
		Version:     version,
		Hostname:    hostname,
		ISOSource:   *isoSource,
	}
	core.DeployFunc = core.RunDeploy
	core.UpdateFunc = core.RunUpdate
	core.ConfirmFunc = core.VerifyActivation
	core.ISOFunc = core.RunISO
	core.PublishFunc = core.RunPublish
	core.RotateAgent = agentServer.Rotate
	for _, job := range jobManager.List() {
		if job.State == jobs.StateConfirming {
			go func(job jobs.Job) {
				_ = core.VerifyActivation(context.Background(), job)
			}(job)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	infoHandler := func(w http.ResponseWriter, r *http.Request) {
		payload := buildInfo(store, jobManager, hostname, version)
		data, err := payload.Encode(store.Identity.Private)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
	}
	infoMux := http.NewServeMux()
	infoMux.HandleFunc("GET "+discovery.InfoPath, infoHandler)
	infoServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", store.Config.InfoPort),
		Handler:           infoMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := infoServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("info server failed", "error", err)
		}
	}()

	localServer := httpapi.NewServer(core, slog.Default())
	controlServer := httpapi.NewServer(core, slog.Default())

	go func() {
		if err := localServer.ServeLocal(ctx, *socketPath); err != nil {
			slog.Error("local control socket failed", "error", err)
		}
	}()

	certificate, err := store.Identity.TLSCertificate()
	if err != nil {
		return err
	}
	go func() {
		if err := controlServer.ServeTLS(ctx, fmt.Sprintf(":%d", store.Config.ControlPort), certificate, nil); err != nil {
			slog.Error("control plane failed", "error", err)
		}
	}()

	ranges, err := discovery.ParseRanges(store.Config.Discovery.Ranges)
	if err != nil {
		return err
	}
	prober := &discovery.Prober{
		Ranges:   ranges,
		Auto:     store.Config.Discovery.Auto,
		Port:     store.Config.InfoPort,
		Timeout:  time.Duration(store.Config.Discovery.Timeout) * time.Second,
		Workers:  store.Config.Discovery.Workers,
		SelfID:   store.Identity.Fingerprint(),
		Registry: peerRegistry,
		Logger:   slog.Default(),
	}
	go prober.Run(ctx, time.Duration(store.Config.Discovery.Interval)*time.Second)

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = infoServer.Shutdown(shutdownCtx)
	return nil
}

func buildInfo(store *state.Store, manager *jobs.Manager, hostname, version string) info.Payload {
	capabilities := []string{"ssh", "deploy", "update"}
	if store.Config.MagicRollback.Enable {
		capabilities = append(capabilities, "magic-rollback")
	}
	jobState := ""
	for _, job := range manager.List() {
		if job.State == jobs.StateInstalling || job.State == jobs.StateActivating ||
			job.State == jobs.StateAwaitingConfirmation || job.State == jobs.StatePreparing {
			jobState = string(job.State)
			break
		}
	}
	return info.Payload{
		ID:           store.Identity.Fingerprint(),
		Version:      info.Version,
		Hostname:     hostname,
		Kind:         store.Config.Kind,
		Addresses:    localAddresses(),
		SSHPort:      store.Config.SSHPort,
		ControlPort:  store.Config.ControlPort,
		Registered:   store.Registration.Registered(),
		Capabilities: capabilities,
		Job:          jobState,
		Daemon:       version,
		Timestamp:    time.Now().Unix(),
		PublicKey:    store.Identity.Public,
	}
}

func localAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]string, 0, 4)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || ip.IsLoopback() {
				continue
			}
			result = append(result, ip.String())
		}
	}
	return result
}
