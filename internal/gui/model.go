package gui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/pathutil"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type Model struct {
	Client   *httpapi.Client
	Status   httpapi.Status
	Devices  []httpapi.Device
	Jobs     []jobs.Job
	Trust    []trust.Record
	Pending  []pairing.Request
	Selected int
	Detail   bool
}

func NewModel(client *httpapi.Client) *Model {
	return &Model{Client: client, Selected: -1}
}

func (m *Model) Load(ctx context.Context) error {
	status, err := m.Client.Status(ctx)
	if err != nil {
		return err
	}
	devices, err := m.Client.Devices(ctx)
	if err != nil {
		return err
	}
	jobList, err := m.Client.Jobs(ctx)
	if err != nil {
		return err
	}
	trustList, err := m.Client.TrustRecords(ctx)
	if err != nil {
		return err
	}
	pending, err := m.Client.PairPending(ctx)
	if err != nil {
		return err
	}
	m.Status = status
	m.Devices = devices
	m.Jobs = jobList
	m.Trust = trustList
	m.Pending = pending
	if m.Selected >= len(m.Devices) {
		m.Selected = -1
		m.Detail = false
	}
	return nil
}

func (m *Model) SelectedDevice() (httpapi.Device, bool) {
	if m.Selected < 0 || m.Selected >= len(m.Devices) {
		return httpapi.Device{}, false
	}
	return m.Devices[m.Selected], true
}

func (m *Model) Select(index int) {
	if index < 0 || index >= len(m.Devices) {
		m.Selected = -1
		m.Detail = false
		return
	}
	m.Selected = index
	m.Detail = true
}

func (m *Model) SelfDevice() (httpapi.Device, bool) {
	for _, device := range m.Devices {
		if device.Self {
			return device, true
		}
	}
	return httpapi.Device{}, false
}

func (m *Model) ActiveJobs() (int, *jobs.Job) {
	active := 0
	var latest *jobs.Job
	for index := range m.Jobs {
		job := &m.Jobs[index]
		if jobTerminal(job.State) {
			continue
		}
		active++
		if latest == nil || job.Updated.After(latest.Updated) {
			latest = job
		}
	}
	return active, latest
}

func (m *Model) actionAvailable(action string) bool {
	device, ok := m.SelectedDevice()
	switch action {
	case "deploy":
		return ok
	case "update", "unregister":
		return ok && device.Registered
	case "register", "pair":
		return ok && !device.Self
	case "ssh":
		return ok && (device.Paired || device.Self)
	case "share":
		return ok && device.Paired
	default:
		return true
	}
}

func isoOutputPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "deploy-target-iso"
	}
	return pathutil.Expand(value)
}

func deviceState(device httpapi.Device) string {
	switch {
	case device.Self:
		return "self"
	case device.Paired:
		return "paired"
	default:
		return "unpaired"
	}
}

func deviceAddress(device httpapi.Device) string {
	if device.Address == "" {
		return "-"
	}
	return device.Address
}

func deviceKind(device httpapi.Device) string {
	if device.Kind == "" {
		return "unknown"
	}
	return device.Kind
}

func deviceShortID(device httpapi.Device) string {
	if device.ShortID != "" {
		return device.ShortID
	}
	return shortID(device.ID, 8)
}

func shortID(value string, length int) string {
	if length <= 0 || len(value) <= length {
		return value
	}
	return value[:length]
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func jobTerminal(state jobs.State) bool {
	switch state {
	case jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled, jobs.StateRolledBack:
		return true
	default:
		return false
	}
}

func jobStateClass(state jobs.State) string {
	switch state {
	case jobs.StateSucceeded:
		return "nc-state-ok"
	case jobs.StateFailed, jobs.StateRolledBack, jobs.StateInterrupted:
		return "nc-state-error"
	case jobs.StateAwaitingConfirmation, jobs.StateConfirming:
		return "nc-state-warn"
	default:
		return "nc-muted"
	}
}

func registrationSummary(text string, value string) string {
	if value == "" {
		return fmt.Sprintf("%s: -", text)
	}
	return fmt.Sprintf("%s: %s", text, value)
}
