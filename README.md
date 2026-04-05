# DockTerm

<div align="center">

**A terminal-native Docker monitoring and management tool built in Go.**  
Real-time CPU & memory telemetry, concurrent stats streaming, and container control — all from your terminal.

[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-SDK-2496ED?style=flat-square&logo=docker)](https://github.com/moby/moby)
[![Bubbletea](https://img.shields.io/badge/TUI-Bubbletea-FF69B4?style=flat-square)](https://github.com/charmbracelet/bubbletea)

</div>

---

## Demo

[![DockTerm Demo](https://asciinema.org/a/P5d1vPktrPCgj3yT.svg)](https://asciinema.org/a/P5d1vPktrPCgj3yT)

> *Click the image above to watch the live demo — navigate containers, monitor stats, and kill a container in real time.*

---

## What Is DockTerm?

DockTerm is a lightweight, keyboard-driven terminal UI for monitoring and managing Docker containers. It bypasses the Docker CLI entirely, communicating directly with the **Docker daemon over a Unix socket** (`/var/run/docker.sock`) using the official Go SDK.

It streams live CPU and memory statistics for every running container simultaneously using **Goroutines and Channels**, renders a color-coded dashboard using the **Bubbletea TUI framework**, and lets you stop containers instantly from the keyboard.

This project was built from scratch as a deep dive into Go concurrency, Unix IPC, and terminal UI architecture.

---

## Features

- **Live telemetry** — CPU% and memory% update every second per container
- **Concurrent streaming** — one Goroutine per container, decoupled via a shared channel
- **Direct Unix socket communication** — no `docker` CLI subprocess, no shell commands
- **Color-coded thresholds** — green at normal usage, red/orange when CPU or memory is high
- **Keyboard-driven** — navigate and manage containers without touching the mouse
- **Instant container termination** — stop any container with a single keypress
- **Auto-resize** — the TUI adapts to your terminal window size

---

## Controls

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `x` | Stop selected container |
| `q` / `Ctrl+C` | Quit |

---

## Architecture

DockTerm is structured around three distinct layers that mirror the flow of data from Docker to your screen.

```
┌─────────────────────────────────────────────────────────────┐
│                      Docker Daemon                          │
│                  /var/run/docker.sock                       │
└───────────────────────────┬─────────────────────────────────┘
                            │  Unix Socket (IPC)
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                   Docker SDK Client                         │
│               github.com/moby/moby/client                   │
│                                                             │
│   ContainerList()  ──►  list of running containers          │
│   ContainerStats() ──►  continuous JSON stream per container│
│   ContainerStop()  ──►  send stop signal to container       │
└───────────────────────────┬─────────────────────────────────┘
                            │
               ┌────────────▼────────────┐
               │   Concurrency Pipeline  │
               │                         │
               │  for each container:    │
               │    go fetchStats() ──►  │
               │    go fetchStats() ──►  │──► statsChan (buffered channel)
               │    go fetchStats() ──►  │
               └────────────┬────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                    Bubbletea TUI                            │
│              (Elm Architecture: Model/Update/View)          │
│                                                             │
│   waitForStat(statsChan) ──► tea.Cmd (non-blocking listen) │
│   Update() ──► receives statMsg, updates model              │
│   View()   ──► renders Lipgloss-styled terminal output      │
└─────────────────────────────────────────────────────────────┘
```

### Layer 1 — The Docker Client (`docker/client.go`)

DockTerm initializes a client using `client.NewClientWithOpts(client.FromEnv)`. This automatically discovers the Docker socket path from the environment — on Linux and WSL2 that's `/var/run/docker.sock`. There is no `exec`-ing of the `docker` CLI binary; every operation is a direct API call over the socket.

Three SDK methods drive the entire application:

- **`ContainerList()`** — called once on startup to enumerate running containers
- **`ContainerStats(stream: true)`** — opens a persistent JSON stream for a container that the daemon pushes to every second
- **`ContainerStop()`** — sends a SIGTERM to the container process and waits for graceful shutdown

### Layer 2 — The Telemetry Engine (`docker/stats.go`)

Docker's stats API does not return percentages. It returns raw CPU tick counts from the Linux kernel. DockTerm applies the same formula the official `docker stats` CLI uses:

**CPU Utilization:**
```
cpu_delta    = current_cpu_total_usage  - previous_cpu_total_usage
system_delta = current_system_cpu_usage - previous_system_cpu_usage

CPU% = (cpu_delta / system_delta) × num_online_cpus × 100
```

The `precpu_stats` field in the JSON stream provides the previous sample automatically — Docker sends two snapshots in every tick so the delta can always be computed.

**Memory Utilization:**
```
used_memory = memory_usage - memory_cache
Mem% = (used_memory / memory_limit) × 100
```

Cache is subtracted because it is reclaimable memory — counting it would make memory usage appear artificially high.

### Layer 3 — The Concurrency Pipeline

The core insight that makes the UI responsive is that stats fetching and UI rendering are completely decoupled.

```go
statsChan := make(chan ContainerStat, 10)

for _, c := range containers {
    go client.GetContainerStats(ctx, c, statsChan)  // one goroutine per container
}
```

Each Goroutine owns one container's stats stream. It decodes the JSON, runs the math, and pushes a `ContainerStat` struct into the shared buffered channel. It never waits for the UI. The UI never waits for any individual container.

If you have 20 containers, you have 20 Goroutines running concurrently. Each one is blocked on a network read (the socket stream), which means they consume almost no CPU while idle — Go's scheduler parks them until data arrives.

### Layer 4 — The TUI (Bubbletea + Lipgloss)

Bubbletea implements the **Elm architecture**, a pattern where:

- **Model** — a plain Go struct holding all application state (container list, cursor position, terminal dimensions)
- **Update** — a pure function that receives a message (keyboard event, new stats, window resize) and returns a new model
- **View** — a pure function that takes the model and returns a formatted string to render

The bridge between the channel and the TUI is a `tea.Cmd`:

```go
func waitForStat(ch chan ContainerStat) tea.Cmd {
    return func() tea.Msg {
        return statMsg(<-ch)   // blocks until next stat arrives
    }
}
```

Bubbletea runs this command on a separate goroutine. When a stat arrives, it becomes a `statMsg` that flows into `Update()`, which updates the matching container in the model slice and immediately re-queues `waitForStat` for the next stat. This creates a continuous pull loop that never blocks the UI thread.

---

## Project Structure

```
dockterm/
├── main.go           # Bubbletea model, update, view — the TUI layer
├── docker/
│   ├── client.go     # Docker SDK initialization, container list, stop
│   └── stats.go      # Stats streaming, CPU/memory calculations
├── go.mod
├── go.sum
└── demo.tape         # VHS tape script for demo recording
```

---

## Tech Stack

| Technology | Role |
|---|---|
| [Go 1.24](https://go.dev/) | Systems language — concurrency primitives, fast compilation |
| [moby/moby client](https://github.com/moby/moby) | Official Docker SDK — Unix socket communication |
| [Bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework — Elm architecture for terminal apps |
| [Lipgloss](https://github.com/charmbracelet/lipgloss) | Terminal styling — colors, borders, layout |
| Docker Unix Socket | IPC mechanism — bypasses Docker CLI entirely |
| Go Goroutines + Channels | Concurrent stats pipeline — one worker per container |

---

## Run Locally

**Prerequisites:** Go 1.21+, Docker running

```bash
# Clone the repository
git clone https://github.com/xoberlierxo/dockterm
cd dockterm

# Start some containers to monitor
docker run -d --name web nginx
docker run -d --name cache redis

# Build and run
go build -ldflags="-s -w" -o dockterm
./dockterm
```

The `-ldflags="-s -w"` flags strip the symbol table and debug information from the binary, significantly reducing its size without affecting runtime behavior.

---

## What I Learned Building This

- **Unix socket IPC** — how processes communicate with the Docker daemon without HTTP or a CLI
- **Go concurrency model** — Goroutines are cheap enough to spawn one per container; channels provide safe communication without mutexes
- **The Elm architecture** — separating state, logic, and rendering makes TUI code surprisingly maintainable
- **Docker's stats API internals** — the daemon streams raw kernel CPU ticks, not percentages; the math to convert them matches what `docker stats` does under the hood
- **Go module system** — navigating API changes between SDK versions (the moby/moby migration from docker/docker)

---

## License

MIT — see [LICENSE](LICENSE)

---

<div align="center">
Built with Go · Charm · Docker SDK
</div>
