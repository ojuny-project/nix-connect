package httpapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/ojuny-project/nix-connect/internal/jobs"
	"github.com/ojuny-project/nix-connect/internal/pairing"
	"github.com/ojuny-project/nix-connect/internal/trust"
)

type Client struct {
	base string
	http *http.Client
}

func NewLocal(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{base: "http://unix", http: &http.Client{Transport: transport}}
}

func NewRemote(address string, tlsConfig *tls.Config) *Client {
	base := address
	if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
		base = "https://" + base
	}
	return &Client{
		base: base,
		http: &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}},
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var result Status
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &result)
	return result, err
}

func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var result []Device
	err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &result)
	return result, err
}

func (c *Client) Register(ctx context.Context, id string, request RegisterRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/devices/"+id+"/register", request, nil)
}

func (c *Client) Unregister(ctx context.Context, id, password string) error {
	return c.do(ctx, http.MethodPost, "/v1/devices/"+id+"/unregister", UnregisterRequest{Password: password}, nil)
}

func (c *Client) Deploy(ctx context.Context, id string, request DeployRequest) (string, error) {
	var response struct {
		Job string `json:"job"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/devices/"+id+"/deploy", request, &response)
	return response.Job, err
}

func (c *Client) Update(ctx context.Context, id string, request UpdateRequest) (string, error) {
	var response struct {
		Job string `json:"job"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/devices/"+id+"/update", request, &response)
	return response.Job, err
}

func (c *Client) SSH(ctx context.Context, id string) (SSHInfo, error) {
	var result SSHInfo
	err := c.do(ctx, http.MethodPost, "/v1/devices/"+id+"/ssh", nil, &result)
	return result, err
}

func (c *Client) Target(ctx context.Context, id string) (TargetInfo, error) {
	var result TargetInfo
	err := c.do(ctx, http.MethodGet, "/v1/devices/"+id+"/target", nil, &result)
	return result, err
}

func (c *Client) Jobs(ctx context.Context) ([]jobs.Job, error) {
	var result []jobs.Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs", nil, &result)
	return result, err
}

func (c *Client) Job(ctx context.Context, id string) (jobs.Job, error) {
	var result jobs.Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+id, nil, &result)
	return result, err
}

func (c *Client) Confirm(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/jobs/"+id+"/confirm", nil, nil)
}

func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/jobs/"+id+"/cancel", nil, nil)
}

func (c *Client) PairStart(ctx context.Context, request PairRequest) (PairResponse, error) {
	var result PairResponse
	err := c.do(ctx, http.MethodPost, "/v1/pair/start", request, &result)
	return result, err
}

func (c *Client) PairConfirm(ctx context.Context, request PairConfirmRequest) error {
	return c.do(ctx, http.MethodPost, "/v1/pair/confirm", request, nil)
}

func (c *Client) PairPending(ctx context.Context) ([]pairing.Request, error) {
	var result []pairing.Request
	err := c.do(ctx, http.MethodGet, "/v1/pair/pending", nil, &result)
	return result, err
}

func (c *Client) ShareKey(ctx context.Context, request KeyShareRequest) (pairing.Envelope, error) {
	var envelope pairing.Envelope
	err := c.do(ctx, http.MethodPost, "/v1/key/share", request, &envelope)
	return envelope, err
}

func (c *Client) ResetRegistration(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/registration/reset", nil, nil)
}

func (c *Client) TrustRecords(ctx context.Context) ([]trust.Record, error) {
	var result []trust.Record
	err := c.do(ctx, http.MethodGet, "/v1/trust", nil, &result)
	return result, err
}

func (c *Client) RevokeTrust(ctx context.Context, fingerprint string) error {
	return c.do(ctx, http.MethodPost, "/v1/trust/"+fingerprint+"/revoke", nil, nil)
}

func (c *Client) RotateAdminKey(ctx context.Context) (string, error) {
	var result struct {
		PublicKey string `json:"publicKey"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/key/rotate", nil, &result)
	return result.PublicKey, err
}

func (c *Client) ImportSharedKey(ctx context.Context, peerID string) (string, error) {
	var result struct {
		PublicKey string `json:"publicKey"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/key/import", KeyImportRequest{PeerID: peerID}, &result)
	return result.PublicKey, err
}

func (c *Client) BuildISO(ctx context.Context, request ISORequest) (string, error) {
	var response struct {
		Job string `json:"job"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/iso/deploytarget", request, &response)
	return response.Job, err
}

func (c *Client) Publish(ctx context.Context, request PublishRequest) (string, error) {
	var response struct {
		Job string `json:"job"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/publish", request, &response)
	return response.Job, err
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&failure)
		if failure.Error == "" {
			failure.Error = fmt.Sprintf("httpapi: status %d", response.StatusCode)
		}
		return errors.New(failure.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
}
