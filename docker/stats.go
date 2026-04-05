package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/moby/moby/client"
)

// ContainerStat holds the calculated stats for one container
type ContainerStat struct {
	ID     string
	Name   string
	State  string
	CPUPct float64
	MemPct float64
}

// rawStats mirrors the JSON structure Docker streams to us
// We only define the fields we actually need
type rawStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint64 `json:"online_cpus"`
	} `json:"cpu_stats"`

	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`

	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			Cache uint64 `json:"cache"`
		} `json:"stats"`
	} `json:"memory_stats"`
}

// GetContainerStats opens a live stream and pushes ContainerStat into the channel
// This function runs forever in a goroutine until the context is cancelled
func (d *DockerClient) GetContainerStats(ctx context.Context, container ContainerInfo, statsChan chan<- ContainerStat) {
	// Open the stats stream - stream:true means it keeps sending data every second
	resp, err := d.cli.ContainerStats(ctx, container.ID, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		fmt.Printf("Error opening stats stream for %s: %v\n", container.Name, err)
		return
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)

	for {
		// Check if context was cancelled (e.g. app is shutting down)
		select {
		case <-ctx.Done():
			return
		default:
		}

		var stats rawStats
		if err := decoder.Decode(&stats); err != nil {
			if err == io.EOF {
				return // Container stopped, stream closed
			}
			return
		}

		// --- CPU % Calculation ---
		cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) -
			float64(stats.PreCPUStats.CPUUsage.TotalUsage)

		systemDelta := float64(stats.CPUStats.SystemCPUUsage) -
			float64(stats.PreCPUStats.SystemCPUUsage)

		numCPUs := float64(stats.CPUStats.OnlineCPUs)
		if numCPUs == 0 {
			numCPUs = 1 // safety fallback
		}

		cpuPct := 0.0
		if systemDelta > 0 {
			cpuPct = (cpuDelta / systemDelta) * numCPUs * 100.0
		}

		// --- Memory % Calculation ---
		// We subtract cache from usage because cached memory isn't really "used"
		usedMemory := float64(stats.MemoryStats.Usage - stats.MemoryStats.Stats.Cache)
		availableMemory := float64(stats.MemoryStats.Limit)

		memPct := 0.0
		if availableMemory > 0 {
			memPct = (usedMemory / availableMemory) * 100.0
		}

		// Push the result into the channel
		statsChan <- ContainerStat{
			ID:     container.ID,
			Name:   container.Name,
			State:  container.State,
			CPUPct: cpuPct,
			MemPct: memPct,
		}
	}
}
