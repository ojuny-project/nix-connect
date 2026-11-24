package registration

import (
	"errors"
	"time"
)

type Mode string

const (
	ModePushed   Mode = "pushed"
	ModeNoCommit Mode = "no-commit"
)

var ErrNotRegistered = errors.New("device is not registered")

type Record struct {
	Repository     string    `json:"repository"`
	Branch         string    `json:"branch"`
	Host           string    `json:"host"`
	InstallMode    Mode      `json:"installMode"`
	UpdatesAllowed bool      `json:"updatesAllowed"`
	Reason         string    `json:"reason,omitempty"`
	SSHUser        string    `json:"sshUser,omitempty"`
	SSHPort        int       `json:"sshPort,omitempty"`
	Account        string    `json:"account,omitempty"`
	LastRevision   string    `json:"lastRevision,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (r Record) Registered() bool {
	return r.Repository != "" && r.Branch != "" && r.Host != ""
}

func (r Record) Validate() error {
	if !r.Registered() {
		return ErrNotRegistered
	}
	if r.SSHPort < 0 || r.SSHPort > 65535 {
		return errors.New("registration: invalid SSH port")
	}
	return nil
}

func (r *Record) Apply(repository, branch, host string, pushed bool, reason, revision string) {
	r.Repository = repository
	r.Branch = branch
	r.Host = host
	r.LastRevision = revision
	r.UpdatesAllowed = pushed
	if pushed {
		r.InstallMode = ModePushed
		r.Reason = ""
	} else {
		r.InstallMode = ModeNoCommit
		r.Reason = reason
	}
	r.UpdatedAt = time.Now().UTC()
}

func (r *Record) EnableUpdates() {
	r.InstallMode = ModePushed
	r.UpdatesAllowed = true
	r.Reason = ""
	r.UpdatedAt = time.Now().UTC()
}

func (r *Record) DisableUpdates(reason string) {
	r.InstallMode = ModeNoCommit
	r.UpdatesAllowed = false
	r.Reason = reason
	r.UpdatedAt = time.Now().UTC()
}
