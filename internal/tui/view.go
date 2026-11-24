package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ojuny-project/nix-connect/internal/jobs"
)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)
	panelTitleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	tableHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selectedStyle    = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("15"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	accentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

func (m model) terminalWidth() int {
	width := m.width
	if width <= 0 {
		width = 100
	}
	if width < 40 {
		width = 40
	}
	return width
}

func (m model) terminalHeight() int {
	height := m.height
	if height <= 0 {
		height = 30
	}
	return height
}

func (m model) pageWidth() int {
	return m.terminalWidth() - 4
}

func (m model) panel(title, body string, width int) string {
	inner := width - 4
	if inner < 20 {
		inner = 20
	}
	content := panelTitleStyle.Render(title) + "\n" + body
	return panelStyle.Width(inner).Render(content)
}

func (m model) separator() string {
	return dimStyle.Render(strings.Repeat("─", m.pageWidth()))
}

func (m model) header() string {
	line := titleStyle.Render("nix-connect")
	if m.status.Hostname != "" {
		line += "  " + m.status.Hostname + "  " + dimStyle.Render(m.status.ShortID)
	}
	state := activeStyle.Render("daemon online")
	if m.err != nil {
		state = errorStyle.Render("daemon error")
	}
	return line + "  " + state
}

func (m model) banner() string {
	lines := make([]string, 0, 2)
	if m.err != nil {
		lines = append(lines, errorStyle.Render(m.err.Error()))
	}
	if m.message != "" {
		lines = append(lines, activeStyle.Render(m.message))
	}
	return strings.Join(lines, "\n")
}

func (m model) screen(title, body, footer string) string {
	parts := []string{m.header(), m.separator()}
	if banner := m.banner(); banner != "" {
		parts = append(parts, banner)
	}
	if footer != "" {
		body += "\n\n" + footer
	}
	parts = append(parts, m.panel(title, body, m.terminalWidth()))
	return strings.Join(parts, "\n")
}

func (m model) viewHome() string {
	bodyWidth := m.pageWidth()
	parts := []string{m.header(), m.separator()}
	if banner := m.banner(); banner != "" {
		parts = append(parts, banner)
	}

	if bodyWidth >= 96 {
		leftWidth := bodyWidth * 55 / 100
		rightWidth := bodyWidth - leftWidth - 1
		left := m.devicesPanel(leftWidth)
		right := lipgloss.JoinVertical(
			lipgloss.Left,
			m.statusPanel(rightWidth),
			m.actionsPanel(rightWidth),
		)
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right))
	} else {
		parts = append(parts,
			m.devicesPanel(bodyWidth),
			m.statusPanel(bodyWidth),
			m.actionsPanel(bodyWidth),
		)
	}
	return strings.Join(parts, "\n")
}

func (m model) devicesPanel(width int) string {
	inner := width - 4
	if inner < 24 {
		inner = 24
	}
	stateWidth := 9
	kindWidth := 7
	remaining := inner - stateWidth - kindWidth - 6
	if remaining < 24 {
		remaining = 24
	}
	hostWidth := maxInt(8, remaining*33/100)
	idWidth := maxInt(8, remaining*33/100)
	addressWidth := remaining - hostWidth - idWidth
	if addressWidth < 8 {
		addressWidth = 8
	}

	var body strings.Builder
	body.WriteString(tableHeaderStyle.Render(fmt.Sprintf(
		"%-*s %-*s %-*s %-*s %-*s",
		hostWidth, "HOST",
		idWidth, "ID",
		addressWidth, "ADDRESS",
		kindWidth, "KIND",
		stateWidth, "STATE",
	)))
	if len(m.devices) == 0 {
		body.WriteString("\n")
		body.WriteString(dimStyle.Render("No devices discovered yet. Press r to scan."))
		return m.panel("Devices", body.String(), width)
	}

	limit := len(m.devices)
	maxRows := m.terminalHeight() - 14
	if maxRows < 3 {
		maxRows = 3
	}
	if limit > maxRows {
		limit = maxRows
	}
	for index := 0; index < limit; index++ {
		device := m.devices[index]
		address := device.Address
		if address == "" {
			address = "-"
		}
		kind := device.Kind
		if kind == "" {
			kind = "unknown"
		}
		state := "unpaired"
		if device.Paired {
			state = "paired"
		}
		if device.Self {
			state = "self"
		}
		line := fmt.Sprintf("%-*s %-*s %-*s %-*s %-*s",
			hostWidth, truncate(device.Hostname, hostWidth),
			idWidth, truncate(device.ShortID, idWidth),
			addressWidth, truncate(address, addressWidth),
			kindWidth, truncate(kind, kindWidth),
			stateWidth, truncate(state, stateWidth),
		)
		body.WriteString("\n")
		if index == m.cursor {
			body.WriteString(selectedStyle.Render("▸ " + line))
		} else {
			body.WriteString("  " + line)
		}
	}
	if limit < len(m.devices) {
		body.WriteString("\n")
		body.WriteString(dimStyle.Render(fmt.Sprintf("… %d more", len(m.devices)-limit)))
	}
	if len(m.devices) <= 1 || m.status.Peers == 0 {
		body.WriteString("\n")
		body.WriteString(dimStyle.Render("No peers discovered yet. This device only."))
	}
	return m.panel("Devices", body.String(), width)
}

func (m model) statusPanel(width int) string {
	inner := width - 4
	if inner < 24 {
		inner = 24
	}
	var body strings.Builder

	if device, ok := m.selected(); ok && m.detail {
		body.WriteString(accentStyle.Render(device.Hostname + "  " + device.ShortID))
		body.WriteString("\n")
		body.WriteString(field("Device", truncate(device.ID, inner-9)))
		body.WriteString(field("Hostname", fallback(device.Hostname, "-")))
		body.WriteString(field("Address", fallback(device.Address, "-")))
		body.WriteString(field("Kind", fallback(device.Kind, "unknown")))
		body.WriteString(field("SSH port", fmt.Sprintf("%d", device.SSHPort)))
		body.WriteString(field("registered:", fmt.Sprintf("%t", device.Registered)))
		body.WriteString(field("Paired", fmt.Sprintf("%t", device.Paired)))
		if !device.LastSeen.IsZero() {
			body.WriteString(field("Last seen", device.LastSeen.Format("2006-01-02 15:04")))
		}
		return m.panel("Device", body.String(), width)
	}

	body.WriteString(accentStyle.Render(fallback(m.status.Hostname, "local device") + "  " + m.status.ShortID))
	body.WriteString("\n")
	body.WriteString(field("Address", fallback(m.selfAddress(), "-")))
	body.WriteString(field("Kind", fallback(m.selfKind(), "installed")))
	body.WriteString(field("Info port", fmt.Sprintf("%d", m.status.InfoPort)))
	body.WriteString(field("Control port", fmt.Sprintf("%d", m.status.ControlPort)))
	body.WriteString(field("Peers", fmt.Sprintf("%d", m.status.Peers)))
	body.WriteString(field("Devices", fmt.Sprintf("%d", len(m.devices))))

	body.WriteString("\n")
	body.WriteString(tableHeaderStyle.Render("Registration"))
	body.WriteString("\n")
	record := m.status.Registration
	if record.Registered() {
		body.WriteString(field("Repository", truncate(record.Repository, inner-12)))
		body.WriteString(field("Host", fallback(record.Host, "-")))
		body.WriteString(field("Branch", fallback(record.Branch, "-")))
		body.WriteString(field("Updates", fmt.Sprintf("%t", record.UpdatesAllowed)))
	} else {
		body.WriteString(dimStyle.Render("Not registered."))
		body.WriteString("\n")
	}

	body.WriteString("\n")
	body.WriteString(tableHeaderStyle.Render("Magic rollback"))
	body.WriteString("\n")
	rollback := m.status.MagicRollback
	body.WriteString(field("Enabled", fmt.Sprintf("%t", rollback.Enable)))
	body.WriteString(field("Mode", fallback(rollback.Mode, "automatic")))
	body.WriteString(field("Timeouts", fmt.Sprintf("%ds / %ds", rollback.AutomaticTimeout, rollback.ManualTimeout)))

	body.WriteString("\n")
	body.WriteString(tableHeaderStyle.Render("Activity"))
	body.WriteString("\n")
	active, latest := activeJob(m.jobList)
	body.WriteString(field("Jobs", fmt.Sprintf("%d (%d active)", len(m.jobList), active)))
	if latest != nil {
		body.WriteString(field("Latest job", truncate(string(latest.State)+" "+string(latest.Kind), inner-12)))
	}
	body.WriteString(field("Trusted", fmt.Sprintf("%d", len(m.trust))))
	body.WriteString(field("Pending", fmt.Sprintf("%d", len(m.pending))))
	return m.panel("Status", body.String(), width)
}

func (m model) actionsPanel(width int) string {
	inner := width - 4
	if inner < 24 {
		inner = 24
	}
	var body strings.Builder
	if inner < 46 {
		body.WriteString("d deploy   u update\n")
		body.WriteString("n register x unregister\n")
		body.WriteString("p publish  s ssh\n")
		body.WriteString("P pair     t trust\n")
		body.WriteString("J jobs     I build ISO\n")
		body.WriteString("K rotate   R reset\n")
		body.WriteString("S share    r refresh\n")
		body.WriteString("enter details  q quit")
		return m.panel("Actions", body.String(), width)
	}
	body.WriteString(tableHeaderStyle.Render("MANAGE"))
	body.WriteString("\n")
	body.WriteString("d deploy          u update\n")
	body.WriteString("n register        x unregister\n")
	body.WriteString("p publish\n")
	body.WriteString("\n")
	body.WriteString(tableHeaderStyle.Render("CONNECT"))
	body.WriteString("\n")
	body.WriteString("P pair            S share key\n")
	body.WriteString("s ssh             t trust\n")
	body.WriteString("\n")
	body.WriteString(tableHeaderStyle.Render("SYSTEM"))
	body.WriteString("\n")
	body.WriteString("J jobs            I build ISO\n")
	body.WriteString("K rotate key      R reset\n")
	body.WriteString("r refresh         enter details\n")
	body.WriteString("q quit")
	return m.panel("Actions", body.String(), width)
}

func (m model) viewJobsScreen() string {
	var body strings.Builder
	compact := m.terminalWidth() < 90
	if compact {
		inner := m.terminalWidth() - 8
		idWidth := maxInt(8, inner/3)
		stateWidth := inner - idWidth - 10
		if stateWidth < 10 {
			stateWidth = 10
		}
		body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-*s %-*s %s", idWidth, "ID", stateWidth, "STATE", "KIND")))
		for index, job := range m.jobList {
			line := fmt.Sprintf("%-*s %-*s %s",
				idWidth, truncate(job.ID, idWidth),
				stateWidth, truncate(string(job.State), stateWidth),
				job.Kind,
			)
			body.WriteString("\n")
			if index == m.jobCursor {
				body.WriteString(selectedStyle.Render("▸ " + line))
			} else {
				body.WriteString("  " + line)
			}
		}
	} else {
		body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-12s %-8s %-20s %-22s %s", "ID", "KIND", "TARGET", "STATE", "UPDATED")))
		for index, job := range m.jobList {
			line := fmt.Sprintf("%-12s %-8s %-20s %-22s %s",
				truncate(job.ID, 12),
				job.Kind,
				truncate(job.Target, 20),
				truncate(string(job.State), 22),
				job.Updated.Format("15:04:05"),
			)
			body.WriteString("\n")
			if index == m.jobCursor {
				body.WriteString(selectedStyle.Render("▸ " + line))
			} else {
				body.WriteString("  " + line)
			}
		}
	}
	if len(m.jobList) == 0 {
		body.WriteString("\n")
		body.WriteString(dimStyle.Render("No jobs."))
	}
	if len(m.jobList) > 0 {
		job := m.jobList[m.jobCursor]
		body.WriteString("\n")
		body.WriteString(field("Phase", job.Phase))
		if job.Error != "" {
			body.WriteString(field("Error", truncate(job.Error, m.terminalWidth()-16)))
		}
		if !job.ConfirmDeadline.IsZero() {
			body.WriteString(field("Confirm by", job.ConfirmDeadline.Format("15:04:05")))
		}
	}
	return m.screen("Jobs", body.String(), "c confirm   x cancel   r refresh   esc back   q quit")
}

func (m model) viewTrustScreen() string {
	var body strings.Builder
	compact := m.terminalWidth() < 90
	if len(m.pending) == 0 && len(m.trust) == 0 {
		body.WriteString(dimStyle.Render("No pending pairings or paired devices."))
	}
	if len(m.pending) > 0 {
		if compact {
			body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-16s %-10s %s", "PAIRING", "CODE", "PEER")))
			for index, request := range m.pending {
				line := fmt.Sprintf("%-16s %-10s %s",
					truncate(request.ID, 16),
					request.Code,
					truncate(request.PeerID, maxInt(8, m.terminalWidth()-34)),
				)
				body.WriteString("\n")
				if index == m.trustCursor {
					body.WriteString(selectedStyle.Render("▸ " + line))
				} else {
					body.WriteString("  " + line)
				}
			}
		} else {
			body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-22s %-20s %-8s %s", "PAIRING", "PEER", "CODE", "EXPIRES")))
			for index, request := range m.pending {
				line := fmt.Sprintf("%-22s %-20s %-8s %s",
					truncate(request.ID, 22),
					truncate(request.PeerID, 20),
					request.Code,
					request.Expires.Format("15:04"),
				)
				body.WriteString("\n")
				if index == m.trustCursor {
					body.WriteString(selectedStyle.Render("▸ " + line))
				} else {
					body.WriteString("  " + line)
				}
			}
		}
		body.WriteString("\n")
	}
	if len(m.trust) > 0 {
		if compact {
			inner := m.terminalWidth() - 8
			fingerprintWidth := maxInt(12, inner/2)
			hostWidth := inner - fingerprintWidth - 2
			if hostWidth < 8 {
				hostWidth = 8
			}
			body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-*s %-*s", fingerprintWidth, "FINGERPRINT", hostWidth, "HOST")))
			for offset, record := range m.trust {
				index := len(m.pending) + offset
				line := fmt.Sprintf("%-*s %-*s",
					fingerprintWidth, truncate(record.Fingerprint, fingerprintWidth),
					hostWidth, truncate(fallback(record.Hostname, "-"), hostWidth),
				)
				body.WriteString("\n")
				if index == m.trustCursor {
					body.WriteString(selectedStyle.Render("▸ " + line))
				} else {
					body.WriteString("  " + line)
				}
			}
		} else {
			body.WriteString(tableHeaderStyle.Render(fmt.Sprintf("%-24s %-18s %-14s %s", "FINGERPRINT", "HOST", "ADDRESS", "PAIRED")))
			for offset, record := range m.trust {
				index := len(m.pending) + offset
				line := fmt.Sprintf("%-24s %-18s %-14s %s",
					truncate(record.Fingerprint, 24),
					truncate(fallback(record.Hostname, "-"), 18),
					truncate(fallback(record.Address, "-"), 14),
					record.PairedAt.Format("2006-01-02 15:04"),
				)
				body.WriteString("\n")
				if index == m.trustCursor {
					body.WriteString(selectedStyle.Render("▸ " + line))
				} else {
					body.WriteString("  " + line)
				}
			}
		}
	}
	return m.screen("Trusted devices", body.String(), "c confirm pending   x revoke   r refresh   esc back   q quit")
}

func (m model) viewFormScreen() string {
	width := m.terminalWidth()
	if width > 76 {
		width = 76
	}
	var body strings.Builder
	for index, label := range m.form.labels {
		cursor := "  "
		if index == m.form.index {
			cursor = "▸ "
		}
		value := m.form.values[index]
		if strings.Contains(strings.ToLower(label), "password") && value != "" {
			value = strings.Repeat("*", len(value))
		}
		body.WriteString(fmt.Sprintf("%s%-12s %s\n", cursor, label, truncate(value, width-20)))
	}
	title := strings.Title(m.form.kind)
	if m.form.kind == "iso" {
		title = "ISO"
	}
	return m.screen(title, body.String(), "tab next   enter next or submit   ctrl+s submit   esc cancel")
}

func (m model) selfAddress() string {
	for _, device := range m.devices {
		if device.Self {
			return device.Address
		}
	}
	return ""
}

func (m model) selfKind() string {
	for _, device := range m.devices {
		if device.Self {
			return device.Kind
		}
	}
	return ""
}

func field(label, value string) string {
	return labelStyle.Render(fmt.Sprintf("%-12s", label)) + " " + value + "\n"
}

func fallback(value, fallbackValue string) string {
	if value == "" {
		return fallbackValue
	}
	return value
}

func activeJob(list []jobs.Job) (int, *jobs.Job) {
	active := 0
	var latest *jobs.Job
	for index := range list {
		job := &list[index]
		switch job.State {
		case jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled, jobs.StateRolledBack:
		default:
			active++
			if latest == nil || job.Updated.After(latest.Updated) {
				latest = job
			}
		}
	}
	return active, latest
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
