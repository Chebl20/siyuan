package multiuser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DockerClient struct {
	client        *http.Client
	image         string
	network       string
	workspaceRoot string
}

type dockerContainer struct {
	ID              string                 `json:"Id"`
	State           struct{ Running bool } `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

func NewDockerClient(config Config) *DockerClient {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}
	return &DockerClient{
		client:        &http.Client{Transport: transport, Timeout: 30 * time.Second},
		image:         config.SiYuanImage,
		network:       config.DockerNetwork,
		workspaceRoot: config.DockerWorkspaceRoot,
	}
}

func (d *DockerClient) CreateWorkspace(ctx context.Context, workspace Workspace) error {
	workspacePath := filepath.Join(d.workspaceRoot, workspace.ID.String())
	request := map[string]any{
		"Image": d.image,
		"Cmd":   []string{"serve"},
		"Env": []string{
			"RUN_IN_CONTAINER=true",
			"SIYUAN_ACCESS_AUTH_CODE_BYPASS=true",
		},
		"Labels": map[string]string{
			"io.siyuan.multiuser.managed":      "true",
			"io.siyuan.multiuser.workspace-id": workspace.ID.String(),
		},
		"HostConfig": map[string]any{
			"Binds":         []string{workspacePath + ":/siyuan/workspace"},
			"NetworkMode":   d.network,
			"RestartPolicy": map[string]string{"Name": "unless-stopped"},
		},
	}
	if err := d.requestJSON(ctx, http.MethodPost, "/v1.47/containers/create?name="+url.QueryEscape(workspace.ContainerName), request, nil); err != nil {
		return err
	}
	if err := d.requestJSON(ctx, http.MethodPost, "/v1.47/containers/"+url.PathEscape(workspace.ContainerName)+"/start", nil, nil); err != nil {
		return err
	}
	return nil
}

func (d *DockerClient) RemoveWorkspace(ctx context.Context, containerName string) error {
	path := "/v1.47/containers/" + url.PathEscape(containerName)
	if err := d.requestJSON(ctx, http.MethodPost, path+"/stop?t=15", nil, nil); err != nil && !errors.Is(err, errDockerNotFound) {
		return err
	}
	if err := d.requestJSON(ctx, http.MethodDelete, path+"?v=false&force=true", nil, nil); err != nil && !errors.Is(err, errDockerNotFound) {
		return err
	}
	return nil
}

var errDockerNotFound = errors.New("docker resource not found")

func (d *DockerClient) Target(ctx context.Context, containerName string) (*url.URL, error) {
	var container dockerContainer
	if err := d.requestJSON(ctx, http.MethodGet, "/v1.47/containers/"+url.PathEscape(containerName)+"/json", nil, &container); err != nil {
		return nil, err
	}
	if !container.State.Running {
		return nil, errors.New("workspace is starting")
	}
	network, ok := container.NetworkSettings.Networks[d.network]
	if !ok || network.IPAddress == "" {
		return nil, errors.New("workspace network is unavailable")
	}
	return &url.URL{Scheme: "http", Host: net.JoinHostPort(network.IPAddress, "6806")}, nil
}

func (d *DockerClient) requestJSON(ctx context.Context, method, path string, input any, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := d.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return errDockerNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("docker API %s %s returned %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(message)))
	}
	if output != nil && response.StatusCode != http.StatusNoContent {
		return json.NewDecoder(response.Body).Decode(output)
	}
	return nil
}

func containerName(id uuid.UUID) string {
	return "siyuan-workspace-" + strings.ReplaceAll(id.String(), "-", "")
}
