# Termux System Monitor

A modern web-based system dashboard for monitoring your Android device in Termux.  
Built with **Go** — single binary, zero dependencies, runs directly in Termux.

> **Original TUI version (Python):**
> ```bash
> curl -O https://raw.githubusercontent.com/AhmarZaidi/termux-status/main/status.py
> chmod +x status.py
> python status.py
> ```

<img width="940" height="587" alt="Screenshot 2026-01-14 at 4 26 45 PM" src="[https://github.com/user-attachments/assets/62af69f5-a6e7-4bd5-b0d3-4c7d2c681de4](https://github.com/sainz1407/termux-web/blob/a119d89d354ee1d22245a909fb48b8b8fa2b3d12/Screenshot%202026-03-11%20223357.png)" />

## Features

- **System Overview** — At-a-glance view of CPU, memory, storage, battery, and network
- **CPU Details** — Real-time usage, per-core frequencies, and processor info
- **Memory Stats** — RAM and swap usage with detailed breakdowns
- **Storage Browser** — Interactive file explorer with upload & download support
- **Battery Monitor** — Charge level, health, temperature, and time remaining
- **Network Info** — Real-time upload/download speeds, IP addresses, and packet stats
- **Process Manager** — Search and filter processes by name, top CPU consumers
- **Custom SVG Icons** — Clean Lucide-inspired icons throughout the UI

## Installation

### Prerequisites

```bash
pkg update && pkg upgrade
pkg install golang
```

### Build from source

```bash
git clone https://github.com/sainz1407/termux-status-web.git
cd termux-status
go build -o monitor .
```

### Or cross-compile on desktop (Linux ARM64 / Termux)

```bash
GOOS=linux GOARCH=arm64 go build -o monitor .
```

Then copy the `monitor` binary to your Termux device.

## Usage

```bash
./monitor
# or specify a custom port
./monitor --port 9090
```

Open your browser at `http://localhost:8080` (or the port you chose).

### File Explorer

- **Browse** — tap folders to navigate
- **Download** — tap any file to download it to your device
- **Upload** — use the Upload button to send files into the current folder

### Process Manager

- Search by process name (e.g. `python`, `node`, `bash`)
- Quick-filter buttons for common runtimes
- Top-10 processes by CPU usage, auto-refreshed every 1.5 s

## Requirements

- **Android** with [Termux](https://termux.dev) installed
- **Go 1.21+** (only needed to build)
- **Termux API** (optional, for battery info): `pkg install termux-api`

## Troubleshooting

**Battery info shows N/A:**
```bash
pkg install termux-api
```

**Permission errors:**  
Some `/proc` files may not be accessible. The monitor handles these gracefully and preserves the last known value.

**Port already in use:**
```bash
./monitor --port 9090
```

## License

MIT License — feel free to use and modify!

## Contributing

Issues and pull requests welcome [here](https://github.com/sainz1407/termux-status/issues)
# termux-web
