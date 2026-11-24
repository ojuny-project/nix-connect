package httpapi

import (
	"context"
	"time"

	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/registration"
	"github.com/ojuny-project/nix-connect/internal/state"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type Status struct {
	ID            string                    `json:"id"`
	ShortID       string                    `json:"shortId"`
	Hostname      string                    `json:"hostname"`
	InfoPort      int                       `json:"infoPort"`
	ControlPort   int                       `json:"controlPort"`
	Peers         int                       `json:"peers"`
	Registration  registration.Record       `json:"registration"`
	MagicRollback state.MagicRollbackConfig `json:"magicRollback"`
}

type Device struct {
	ID         string    `json:"id"`
	ShortID    string    `json:"shortId"`
	Hostname   string    `json:"hostname"`
	Address    string    `json:"address,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Job        string    `json:"job,omitempty"`
	SSHPort    int       `json:"sshPort,omitempty"`
	Registered bool      `json:"registered"`
	Paired     bool      `json:"paired"`
	Self       bool      `json:"self"`
	LastSeen   time.Time `json:"lastSeen,omitempty"`
}

type RegisterRequest struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Host       string `json:"host"`
	Account    string `json:"account,omitempty"`
	SSHUser    string `json:"sshUser,omitempty"`
	SSHPort    int    `json:"sshPort,omitempty"`
	Password   string `json:"password,omitempty"`
}

type UnregisterRequest struct {
	Password string `json:"password,omitempty"`
}

type DeployRequest struct {
	Repository    string `json:"repository"`
	Branch        string `json:"branch"`
	Host          string `json:"host"`
	Disk          string `json:"disk,omitempty"`
	Account       string `json:"account,omitempty"`
	AdminKey      string `json:"adminKey,omitempty"`
	Firmware      string `json:"firmware,omitempty"`
	Password      string `json:"password,omitempty"`
	Mode          string `json:"mode,omitempty"`
	MagicRollback *bool  `json:"magicRollback,omitempty"`
	Timeout       int    `json:"timeout,omitempty"`
}

type UpdateRequest struct {
	Password      string `json:"password,omitempty"`
	AdminKey      string `json:"adminKey,omitempty"`
	Mode          string `json:"mode,omitempty"`
	MagicRollback *bool  `json:"magicRollback,omitempty"`
	Timeout       int    `json:"timeout,omitempty"`
}

type SSHInfo struct {
	Host        string `json:"host"`
	User        string `json:"user"`
	Port        int    `json:"port"`
	AgentSocket string `json:"agentSocket"`
}

type PairRequest struct {
	PeerID  string `json:"peerId"`
	PeerKey string `json:"peerKey"`
}

type PairResponse struct {
	ID      string    `json:"id"`
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

type PairConfirmRequest struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

type KeyShareRequest struct {
	PeerID        string `json:"peerId"`
	PeerPublicKey string `json:"peerPublicKey"`
}

type KeyImportRequest struct {
	PeerID string `json:"peerId"`
}

type ISORequest struct {
	Output string `json:"output,omitempty"`
}

type PublishRequest struct {
	RemoteURL string `json:"remoteUrl"`
	Branch    string `json:"branch,omitempty"`
	Password  string `json:"password,omitempty"`
}

type Service interface {
	Status(ctx context.Context) Status
	Devices(ctx context.Context) []Device
	Register(ctx context.Context, id string, request RegisterRequest) error
	Unregister(ctx context.Context, id string, password string) error
	Deploy(ctx context.Context, id string, request DeployRequest) (string, error)
	Update(ctx context.Context, id string, request UpdateRequest) (string, error)
	SSH(ctx context.Context, id string) (SSHInfo, error)
	Jobs(ctx context.Context) []jobs.Job
	Job(ctx context.Context, id string) (jobs.Job, error)
	Confirm(ctx context.Context, id, confirmer string) error
	Cancel(ctx context.Context, id string) error
	PairStart(ctx context.Context, request PairRequest) (PairResponse, error)
	PairConfirm(ctx context.Context, request PairConfirmRequest) error
	PairPending(ctx context.Context) []pairing.Request
	ShareKey(ctx context.Context, request KeyShareRequest) (pairing.Envelope, error)
	ImportSharedKey(ctx context.Context, peerID string) (string, error)
	ResetRegistration(ctx context.Context) error
	TrustRecords(ctx context.Context) []trust.Record
	RevokeTrust(ctx context.Context, fingerprint string) error
	RotateAdminKey(ctx context.Context) (string, error)
	BuildISO(ctx context.Context, request ISORequest) (string, error)
	Publish(ctx context.Context, request PublishRequest) (string, error)
}
