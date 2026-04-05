package docker

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"
)

// DockerClient wraps the moby SDK client
type DockerClient struct {
	cli *client.Client
}

// NewClient creates a new Docker client using environment defaults
func NewClient() (*DockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}
	return &DockerClient{cli: cli}, nil
}

// ContainerInfo holds the basic info we care about for each container
type ContainerInfo struct {
	ID    string
	Name  string
	Image string
	State string
}

// GetContainers returns all running containers
func (d *DockerClient) GetContainers() ([]ContainerInfo, error) {
	result, err := d.cli.ContainerList(context.Background(), client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	var containers []ContainerInfo
	for _, c := range result.Items {
		name := "unnamed"
		if len(c.Names) > 0 {
			name = c.Names[0][1:] // strip leading slash
		}

		containers = append(containers, ContainerInfo{
			ID:    c.ID[:12],
			Name:  name,
			Image: c.Image,
			State: string(c.State),
		})
	}

	return containers, nil
}

// Close cleans up the client connection
func (d *DockerClient) Close() {
	d.cli.Close()
}

// StopContainer stops a running container by ID
func (d *DockerClient) StopContainer(containerID string) error {
	_, err := d.cli.ContainerStop(context.Background(), containerID, client.ContainerStopOptions{})
	if err != nil {
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}
	return nil
}
