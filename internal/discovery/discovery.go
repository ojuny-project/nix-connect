package discovery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/ojuny-project/nix-connect/internal/info"
	"github.com/ojuny-project/nix-connect/internal/registry"
)

const (
	InfoPath      = "/.nix-connect/v1/info"
	MaxAddresses  = 65536
	maxInfoLength = 64 << 10
)

type Prober struct {
	Ranges   []netip.Prefix
	Auto     bool
	Port     int
	Timeout  time.Duration
	Workers  int
	SelfID   string
	Gate     func() bool
	Registry *registry.Registry
	Logger   *slog.Logger
}

func (p *Prober) Scan(ctx context.Context) int {
	if p.Gate != nil && !p.Gate() {
		return 0
	}
	targets := p.targets()
	if len(targets) == 0 {
		return 0
	}

	workers := p.Workers
	if workers <= 0 {
		workers = 64
	}
	if workers > len(targets) {
		workers = len(targets)
	}

	addresses := make(chan netip.Addr)
	var wait sync.WaitGroup
	var count int
	var mutex sync.Mutex

	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for address := range addresses {
				probeCtx, cancel := context.WithTimeout(ctx, p.Timeout)
				ok := p.probe(probeCtx, address)
				cancel()
				if ok {
					mutex.Lock()
					count++
					mutex.Unlock()
				}
			}
		}()
	}

	for _, address := range targets {
		select {
		case addresses <- address:
		case <-ctx.Done():
			close(addresses)
			wait.Wait()
			return count
		}
	}
	close(addresses)
	wait.Wait()
	return count
}

func (p *Prober) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	p.Scan(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.Scan(ctx)
		}
	}
}

func (p *Prober) probe(ctx context.Context, address netip.Addr) bool {
	client := &http.Client{Timeout: p.Timeout}
	url := fmt.Sprintf("http://%s:%d%s", address.String(), p.Port, InfoPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInfoLength))
	if err != nil {
		return false
	}
	payload, err := info.Decode(data)
	if err != nil {
		if p.Logger != nil {
			p.Logger.Debug("invalid info payload", "address", address, "error", err)
		}
		return false
	}
	if payload.ID == p.SelfID {
		return false
	}
	if p.Registry != nil {
		p.Registry.Upsert(payload, address.String())
	}
	return true
}

func (p *Prober) targets() []netip.Addr {
	prefixes := append([]netip.Prefix(nil), p.Ranges...)
	if p.Auto {
		prefixes = append(prefixes, PrimaryPrefixes()...)
	}

	seen := make(map[netip.Addr]struct{})
	result := make([]netip.Addr, 0, 256)
	for _, prefix := range prefixes {
		if !prefix.IsValid() {
			continue
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is6() && prefix.Bits() < 120 {
			continue
		}
		enumerate(prefix, func(address netip.Addr) bool {
			if len(result) >= MaxAddresses {
				return false
			}
			if _, ok := seen[address]; ok {
				return true
			}
			seen[address] = struct{}{}
			result = append(result, address)
			return true
		})
		if len(result) >= MaxAddresses {
			break
		}
	}
	return result
}

func enumerate(prefix netip.Prefix, add func(netip.Addr) bool) {
	address := prefix.Addr()
	for {
		if !prefix.Contains(address) {
			return
		}
		if !add(address) {
			return
		}
		next := address.Next()
		if !next.IsValid() {
			return
		}
		address = next
	}
}

func PrimaryPrefixes() []netip.Prefix {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	result := make([]netip.Prefix, 0, 4)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || !prefix.Addr().Is4() {
				continue
			}
			if prefix.Addr().IsLoopback() || prefix.Addr().IsLinkLocalUnicast() || prefix.Addr().IsPrivate() {
				result = append(result, prefix)
			}
		}
	}
	return result
}

func ParseRanges(values []string) ([]netip.Prefix, error) {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !strings.Contains(value, "/") {
			value += "/32"
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("discovery: invalid range %q: %w", value, err)
		}
		result = append(result, prefix)
	}
	return result, nil
}
