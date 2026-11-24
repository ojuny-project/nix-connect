package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ojuny-project/nix-connect/internal/httpapi"
	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

type model struct {
	client      *httpapi.Client
	devices     []httpapi.Device
	status      httpapi.Status
	err         error
	cursor      int
	detail      bool
	form        *form
	message     string
	trustView   bool
	trust       []trust.Record
	pending     []pairing.Request
	trustCursor int
	confirm     string
	pairTarget  string
	pair        *httpapi.PairResponse
	jobsView    bool
	jobList     []jobs.Job
	jobCursor   int
}

type form struct {
	kind   string
	labels []string
	values []string
	index  int
}

type loadedMsg struct {
	devices []httpapi.Device
	status  httpapi.Status
	err     error
}

type actionMsg struct {
	err     error
	message string
}

type trustMsg struct {
	records []trust.Record
	pending []pairing.Request
	err     error
}

type jobsMsg struct {
	list []jobs.Job
	err  error
}

func Run(client *httpapi.Client) error {
	program := tea.NewProgram(model{client: client}, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return load(m.client)
}

func load(client *httpapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		devices, devicesErr := client.Devices(ctx)
		status, statusErr := client.Status(ctx)
		if devicesErr != nil {
			return loadedMsg{err: devicesErr}
		}
		if statusErr != nil {
			return loadedMsg{err: statusErr}
		}
		return loadedMsg{devices: devices, status: status}
	}
}

func loadTrust(client *httpapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		records, err := client.TrustRecords(ctx)
		if err != nil {
			return trustMsg{err: err}
		}
		pending, err := client.PairPending(ctx)
		return trustMsg{records: records, pending: pending, err: err}
	}
}

func revokeTrust(client *httpapi.Client, fingerprint string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.RevokeTrust(ctx, fingerprint); err != nil {
			return trustMsg{err: err}
		}
		records, err := client.TrustRecords(ctx)
		if err != nil {
			return trustMsg{err: err}
		}
		pending, err := client.PairPending(ctx)
		return trustMsg{records: records, pending: pending, err: err}
	}
}

func confirmPair(client *httpapi.Client, request pairing.Request) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: request.ID, Code: request.Code}); err != nil {
			return trustMsg{err: err}
		}
		records, err := client.TrustRecords(ctx)
		if err != nil {
			return trustMsg{err: err}
		}
		pending, err := client.PairPending(ctx)
		return trustMsg{records: records, pending: pending, err: err}
	}
}

func loadJobs(client *httpapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		list, err := client.Jobs(ctx)
		return jobsMsg{list: list, err: err}
	}
}

func confirmJob(client *httpapi.Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Confirm(ctx, id); err != nil {
			return jobsMsg{err: err}
		}
		list, err := client.Jobs(ctx)
		return jobsMsg{list: list, err: err}
	}
}

func cancelJob(client *httpapi.Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.Cancel(ctx, id); err != nil {
			return jobsMsg{err: err}
		}
		list, err := client.Jobs(ctx)
		return jobsMsg{list: list, err: err}
	}
}

func resetRegistration(client *httpapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ResetRegistration(ctx); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{message: "registration reset to no-commit"}
	}
}

func rotateAdminKey(client *httpapi.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		publicKey, err := client.RotateAdminKey(ctx)
		if err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{message: "administration key rotated: " + truncate(publicKey, 40)}
	}
}

func newForm(kind string) *form {
	labels := map[string][]string{
		"deploy":     {"flake URL", "branch", "host", "disk", "account", "password"},
		"update":     {"password"},
		"register":   {"flake URL", "branch", "host", "account", "password"},
		"unregister": {"password"},
		"publish":    {"new remote", "branch", "password"},
	}[kind]
	values := make([]string, len(labels))
	if kind == "deploy" || kind == "register" {
		for index, label := range labels {
			switch label {
			case "branch":
				values[index] = "main"
			case "host":
				values[index] = "host"
			}
		}
	}
	return &form{kind: kind, labels: labels, values: values}
}

func (m model) selected() (httpapi.Device, bool) {
	if len(m.devices) == 0 || m.cursor < 0 || m.cursor >= len(m.devices) {
		return httpapi.Device{}, false
	}
	return m.devices[m.cursor], true
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyMsg:
		if m.jobsView {
			return m.updateJobs(message)
		}
		if m.trustView {
			return m.updateTrust(message)
		}
		if m.form != nil {
			return m.updateForm(message)
		}
		return m.updateList(message)
	case loadedMsg:
		m.devices = message.devices
		m.status = message.status
		m.err = message.err
		if m.cursor >= len(m.devices) && len(m.devices) > 0 {
			m.cursor = len(m.devices) - 1
		}
	case trustMsg:
		m.trust = message.records
		m.pending = message.pending
		m.err = message.err
		if m.trustCursor >= len(m.pending)+len(m.trust) && len(m.pending)+len(m.trust) > 0 {
			m.trustCursor = len(m.pending) + len(m.trust) - 1
		}
	case jobsMsg:
		m.jobList = message.list
		m.err = message.err
		if m.jobCursor >= len(m.jobList) && len(m.jobList) > 0 {
			m.jobCursor = len(m.jobList) - 1
		}
	case actionMsg:
		if message.err != nil {
			m.err = message.err
		} else {
			m.err = nil
		}
		if message.message != "" {
			m.message = message.message
		}
		return m, load(m.client)
	}
	return m, nil
}

func (m model) updateList(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		m.confirm = ""
		return m, load(m.client)
	case "t":
		m.trustView = true
		m.confirm = ""
		m.message = ""
		return m, loadTrust(m.client)
	case "J":
		m.jobsView = true
		m.confirm = ""
		m.message = ""
		return m, loadJobs(m.client)
	case "R":
		if m.confirm != "reset" {
			m.confirm = "reset"
			m.message = "press R again to reset the registration to no-commit"
			return m, nil
		}
		m.confirm = ""
		return m, resetRegistration(m.client)
	case "K":
		if m.confirm != "rotate" {
			m.confirm = "rotate"
			m.message = "press K again to rotate the administration key; existing ISOs become invalid"
			return m, nil
		}
		m.confirm = ""
		return m, rotateAdminKey(m.client)
	case "S":
		device, ok := m.selected()
		if !ok {
			return m, nil
		}
		if m.confirm != "share:"+device.ID {
			m.confirm = "share:" + device.ID
			m.message = fmt.Sprintf("key sharing code %s; press S again to import the administration key from %s",
				pairing.Code(m.status.ID, device.ID), device.Hostname)
			return m, nil
		}
		m.confirm = ""
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		publicKey, err := m.client.ImportSharedKey(ctx, device.ID)
		if err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.message = "administration key imported: " + truncate(publicKey, 40)
	case "up", "k":
		m.confirm = ""
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		m.confirm = ""
		if m.cursor < len(m.devices)-1 {
			m.cursor++
		}
	case "enter", "i":
		m.confirm = ""
		m.detail = !m.detail
	case "d":
		m.confirm = ""
		m.form = newForm("deploy")
	case "u":
		m.confirm = ""
		m.form = newForm("update")
	case "n":
		m.confirm = ""
		m.form = newForm("register")
	case "x":
		m.confirm = ""
		m.form = newForm("unregister")
	case "p":
		m.confirm = ""
		m.form = newForm("publish")
	case "P":
		m.confirm = ""
		device, ok := m.selected()
		if !ok {
			return m, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if m.pair != nil && m.pairTarget == device.ID {
			if err := m.client.PairConfirm(ctx, httpapi.PairConfirmRequest{ID: m.pair.ID, Code: m.pair.Code}); err != nil {
				m.err = err
				return m, nil
			}
			m.err = nil
			m.message = "pairing confirmed for " + device.Hostname
			m.pair = nil
			m.pairTarget = ""
			return m, nil
		}
		response, err := m.client.PairStart(ctx, httpapi.PairRequest{PeerID: device.ID})
		if err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.pair = &response
		m.pairTarget = device.ID
		m.message = fmt.Sprintf("pairing code %s for %s; confirm on the target, then press P again", response.Code, device.Hostname)
	case "s":
		m.confirm = ""
		device, ok := m.selected()
		if !ok {
			return m, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		info, err := m.client.SSH(ctx, device.ID)
		if err != nil {
			m.err = err
			return m, nil
		}
		command := exec.Command("ssh", "-p", strconv.Itoa(info.Port), info.User+"@"+info.Host)
		command.Env = append(os.Environ(), "SSH_AUTH_SOCK="+info.AgentSocket)
		return m, tea.ExecProcess(command, func(err error) tea.Msg {
			if err != nil {
				return actionMsg{err: err}
			}
			return actionMsg{message: "returned from ssh"}
		})
	}
	return m, nil
}

func (m model) updateTrust(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "t":
		m.trustView = false
		m.confirm = ""
		return m, nil
	case "r":
		m.confirm = ""
		return m, loadTrust(m.client)
	case "up", "k":
		m.confirm = ""
		if m.trustCursor > 0 {
			m.trustCursor--
		}
	case "down", "j":
		m.confirm = ""
		if m.trustCursor < len(m.pending)+len(m.trust)-1 {
			m.trustCursor++
		}
	case "c":
		if len(m.pending) == 0 || m.trustCursor < 0 || m.trustCursor >= len(m.pending) {
			return m, nil
		}
		m.confirm = ""
		return m, confirmPair(m.client, m.pending[m.trustCursor])
	case "x", "delete":
		index := m.trustCursor - len(m.pending)
		if index < 0 || index >= len(m.trust) {
			return m, nil
		}
		fingerprint := m.trust[index].Fingerprint
		if m.confirm != "revoke:"+fingerprint {
			m.confirm = "revoke:" + fingerprint
			m.message = "press x again to revoke " + fingerprint
			return m, nil
		}
		m.confirm = ""
		return m, revokeTrust(m.client, fingerprint)
	}
	return m, nil
}

func (m model) updateJobs(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "J":
		m.jobsView = false
		m.confirm = ""
		return m, nil
	case "r":
		m.confirm = ""
		return m, loadJobs(m.client)
	case "up", "k":
		m.confirm = ""
		if m.jobCursor > 0 {
			m.jobCursor--
		}
	case "down", "j":
		m.confirm = ""
		if m.jobCursor < len(m.jobList)-1 {
			m.jobCursor++
		}
	case "c":
		if len(m.jobList) == 0 || m.jobCursor < 0 || m.jobCursor >= len(m.jobList) {
			return m, nil
		}
		m.confirm = ""
		return m, confirmJob(m.client, m.jobList[m.jobCursor].ID)
	case "x":
		if len(m.jobList) == 0 || m.jobCursor < 0 || m.jobCursor >= len(m.jobList) {
			return m, nil
		}
		job := m.jobList[m.jobCursor]
		if m.confirm != "cancel:"+job.ID {
			m.confirm = "cancel:" + job.ID
			m.message = "press x again to cancel job " + job.ID
			return m, nil
		}
		m.confirm = ""
		return m, cancelJob(m.client, job.ID)
	}
	return m, nil
}

func (m model) updateForm(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.Type {
	case tea.KeyEsc:
		m.form = nil
		return m, nil
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyCtrlS:
		return m.submitForm()
	case tea.KeyBackspace:
		if len(m.form.values[m.form.index]) > 0 {
			value := []rune(m.form.values[m.form.index])
			m.form.values[m.form.index] = string(value[:len(value)-1])
		}
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		if m.form.index < len(m.form.values)-1 {
			m.form.index++
		}
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		if m.form.index > 0 {
			m.form.index--
		}
		return m, nil
	case tea.KeyEnter:
		if m.form.index == len(m.form.values)-1 {
			return m.submitForm()
		}
		m.form.index++
		return m, nil
	case tea.KeyRunes:
		m.form.values[m.form.index] += string(message.Runes)
		return m, nil
	}
	return m, nil
}

func (m model) submitForm() (tea.Model, tea.Cmd) {
	device, ok := m.selected()
	if !ok {
		m.err = fmt.Errorf("select a device first")
		m.form = nil
		return m, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	values := m.form.values
	var err error
	var job string
	switch m.form.kind {
	case "deploy":
		job, err = m.client.Deploy(ctx, device.ID, httpapi.DeployRequest{
			Repository: values[0],
			Branch:     values[1],
			Host:       values[2],
			Disk:       values[3],
			Account:    values[4],
			Password:   values[5],
		})
	case "update":
		job, err = m.client.Update(ctx, device.ID, httpapi.UpdateRequest{Password: values[0]})
	case "register":
		err = m.client.Register(ctx, device.ID, httpapi.RegisterRequest{
			Repository: values[0],
			Branch:     values[1],
			Host:       values[2],
			Account:    values[3],
			Password:   values[4],
		})
	case "unregister":
		err = m.client.Unregister(ctx, device.ID, values[0])
	case "publish":
		job, err = m.client.Publish(ctx, httpapi.PublishRequest{
			RemoteURL: values[0],
			Branch:    values[1],
			Password:  values[2],
		})
	}
	m.form = nil
	if err != nil {
		return m, func() tea.Msg { return actionMsg{err: err} }
	}
	if job != "" {
		return m, func() tea.Msg { return actionMsg{message: "job " + job} }
	}
	return m, func() tea.Msg { return actionMsg{message: "request accepted"} }
}

func (m model) View() string {
	if m.jobsView {
		return m.viewJobs()
	}
	if m.trustView {
		return m.viewTrust()
	}
	if m.form != nil {
		return m.viewForm()
	}
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("nix-connect"))
	builder.WriteString("  ")
	if m.status.Hostname != "" {
		builder.WriteString(fmt.Sprintf("%s  %s", m.status.Hostname, dimStyle.Render(m.status.ShortID)))
	}
	builder.WriteString("\n\n")

	if m.err != nil {
		builder.WriteString(errorStyle.Render(m.err.Error()))
		builder.WriteString("\n\n")
	}
	if m.message != "" {
		builder.WriteString(activeStyle.Render(m.message))
		builder.WriteString("\n\n")
	}
	if len(m.devices) == 0 {
		builder.WriteString(dimStyle.Render("No devices discovered yet. Press r to scan."))
		builder.WriteString("\n")
	}
	for index, device := range m.devices {
		cursor := "  "
		if index == m.cursor {
			cursor = "> "
		}
		state := "unpaired"
		if device.Paired {
			state = activeStyle.Render("paired")
		}
		if device.Self {
			state = "self"
		}
		address := device.Address
		if address == "" {
			address = "-"
		}
		kind := device.Kind
		if kind == "" {
			kind = "unknown"
		}
		builder.WriteString(fmt.Sprintf("%s%-18s %-22s %-10s %-9s %s\n",
			cursor,
			truncate(device.Hostname, 18),
			truncate(device.ID, 22),
			truncate(address, 10),
			kind,
			state,
		))
	}
	if m.detail && len(m.devices) > 0 {
		device := m.devices[m.cursor]
		builder.WriteString("\n")
		builder.WriteString(titleStyle.Render("Device"))
		builder.WriteString("\n")
		builder.WriteString(fmt.Sprintf("id:         %s\n", device.ID))
		builder.WriteString(fmt.Sprintf("hostname:   %s\n", device.Hostname))
		builder.WriteString(fmt.Sprintf("address:    %s\n", device.Address))
		builder.WriteString(fmt.Sprintf("kind:       %s\n", device.Kind))
		builder.WriteString(fmt.Sprintf("ssh port:   %d\n", device.SSHPort))
		builder.WriteString(fmt.Sprintf("registered: %t\n", device.Registered))
		builder.WriteString(fmt.Sprintf("paired:     %t\n", device.Paired))
	}
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("d deploy  u update  n register  x unregister  p publish  P pair  s ssh"))
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("t trust  J jobs  R reset registration  K rotate key  S share key"))
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("r refresh   enter details   q quit"))
	builder.WriteString("\n")
	if m.status.Registration.Registered() {
		builder.WriteString(dimStyle.Render(fmt.Sprintf("registered: %s#%s",
			m.status.Registration.Repository, m.status.Registration.Host)))
		builder.WriteString("\n")
	}
	return builder.String()
}

func (m model) viewJobs() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Jobs"))
	builder.WriteString("\n\n")
	if m.err != nil {
		builder.WriteString(errorStyle.Render(m.err.Error()))
		builder.WriteString("\n\n")
	}
	if m.message != "" {
		builder.WriteString(activeStyle.Render(m.message))
		builder.WriteString("\n\n")
	}
	if len(m.jobList) == 0 {
		builder.WriteString(dimStyle.Render("No jobs."))
		builder.WriteString("\n")
	}
	for index, job := range m.jobList {
		cursor := "  "
		if index == m.jobCursor {
			cursor = "> "
		}
		state := string(job.State)
		if job.State == jobs.StateAwaitingConfirmation {
			state = activeStyle.Render(state)
		}
		builder.WriteString(fmt.Sprintf("%s%-12s %-8s %-22s %-22s %s\n",
			cursor,
			truncate(job.ID, 12),
			job.Kind,
			truncate(job.Target, 22),
			state,
			job.Updated.Format("15:04:05"),
		))
	}
	if len(m.jobList) > 0 {
		job := m.jobList[m.jobCursor]
		builder.WriteString("\n")
		builder.WriteString(fmt.Sprintf("phase:  %s\n", job.Phase))
		if job.Error != "" {
			builder.WriteString(fmt.Sprintf("error:  %s\n", job.Error))
		}
		if !job.ConfirmDeadline.IsZero() {
			builder.WriteString(fmt.Sprintf("confirm by: %s\n", job.ConfirmDeadline.Format("15:04:05")))
		}
	}
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("c confirm   x cancel   r refresh   esc back   q quit"))
	builder.WriteString("\n")
	return builder.String()
}

func (m model) viewTrust() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render("Trusted devices"))
	builder.WriteString("\n\n")
	if m.err != nil {
		builder.WriteString(errorStyle.Render(m.err.Error()))
		builder.WriteString("\n\n")
	}
	if m.message != "" {
		builder.WriteString(activeStyle.Render(m.message))
		builder.WriteString("\n\n")
	}
	if len(m.pending) == 0 && len(m.trust) == 0 {
		builder.WriteString(dimStyle.Render("No pending pairings or paired devices."))
		builder.WriteString("\n")
	}
	for index, request := range m.pending {
		cursor := "  "
		if index == m.trustCursor {
			cursor = "> "
		}
		builder.WriteString(fmt.Sprintf("%s%-22s %-20s code %s  %s\n",
			cursor,
			truncate(request.ID, 22),
			truncate(request.PeerID, 20),
			request.Code,
			request.Expires.Format("15:04"),
		))
	}
	for offset, record := range m.trust {
		index := len(m.pending) + offset
		cursor := "  "
		if index == m.trustCursor {
			cursor = "> "
		}
		hostname := record.Hostname
		if hostname == "" {
			hostname = "-"
		}
		address := record.Address
		if address == "" {
			address = "-"
		}
		builder.WriteString(fmt.Sprintf("%s%-24s %-18s %-14s %s\n",
			cursor,
			truncate(record.Fingerprint, 24),
			truncate(hostname, 18),
			truncate(address, 14),
			record.PairedAt.Format("2006-01-02 15:04"),
		))
	}
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("c confirm pending   x revoke   r refresh   esc back   q quit"))
	builder.WriteString("\n")
	return builder.String()
}

func (m model) viewForm() string {
	var builder strings.Builder
	builder.WriteString(titleStyle.Render(strings.Title(m.form.kind)))
	builder.WriteString("\n\n")
	for index, label := range m.form.labels {
		cursor := "  "
		if index == m.form.index {
			cursor = "> "
		}
		value := m.form.values[index]
		if strings.Contains(strings.ToLower(label), "password") {
			if value != "" {
				value = strings.Repeat("*", len(value))
			}
		}
		builder.WriteString(fmt.Sprintf("%s%-12s %s\n", cursor, label, value))
	}
	builder.WriteString("\n")
	builder.WriteString(dimStyle.Render("tab next   enter next or submit   ctrl+s submit   esc cancel"))
	builder.WriteString("\n")
	return builder.String()
}

func truncate(value string, length int) string {
	if len(value) <= length {
		return value
	}
	if length <= 1 {
		return value[:length]
	}
	return value[:length-1] + "…"
}
