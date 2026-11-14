<h1 align="center">Pusher Server Go</h1>

<p align="center">
  A lightweight, high-performance, in-memory, multi-tenant Pusher server alternative written in Go. Designed to be fully compatible with Laravel's broadcasting system using the standard Pusher Protocol v7.
</p>

<p align="center">
  <a href="https://github.com/SadeghPM/pusher-server-go/actions"><img src="https://img.shields.io/github/actions/workflow/status/SadeghPM/pusher-server-go/release.yml?branch=main&style=flat-square&color=FF2D20" alt="Build Status"></a>
  <a href="https://github.com/SadeghPM/pusher-server-go/releases"><img src="https://img.shields.io/github/v/release/SadeghPM/pusher-server-go?style=flat-square&color=FF2D20" alt="Latest Release"></a>
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go Version"></a>
  <a href="https://github.com/SadeghPM/pusher-server-go/blob/main/LICENSE"><img src="https://img.shields.io/github/license/SadeghPM/pusher-server-go?style=flat-square&color=FF2D20" alt="License"></a>
</p>

---

## About Pusher Server Go

Pusher Server Go is a self-hosted, drop-in replacement for Pusher or Laravel Reverb. It handles real-time broadcasting for Laravel applications with zero external dependencies and very low resource usage.

By using Go's lightweight concurrency model, this server can handle thousands of concurrent WebSocket connections efficiently, making it ideal for both small projects and high-traffic production applications.

## Key Features

- **Pusher compatible**: Fully compatible with the Pusher Protocol (v7). Works with Laravel Echo out-of-the-box.
- **Multi-Tenant**: Run multiple Laravel applications on a single server instance with isolated credentials.
- **Admin Dashboard & Debug Console**: A built-in web interface to view active connections, debug channels, and send test events.
- **Prometheus Metrics**: Built-in `/metrics` endpoint to monitor connections, channels, messages, and API errors.
- **Secure**: Restrict connections per app using `allowed_origins`.
- **Easy Configuration**: Simple setup via a single `config.yaml` file.

---

## Installation

### Method 1: Automatic Installer (Recommended)

To download, configure, and install Pusher Server Go as a systemd service on Linux, run the following command:

```bash
curl -sSL https://raw.githubusercontent.com/SadeghPM/pusher-server-go/main/install.sh | sudo bash
```

The script installs the latest Linux x86_64 release, prompts you for configuration details (including the public Pusher URL), creates a `config.yaml` file under `/opt/pusher-clone/config.yaml`, and sets up a systemd service to run it. On other platforms, build from source with `go build .`.

### Method 2: Manual Binary Installation

1. Download the latest Linux x86_64 binary from the [GitHub Releases](https://github.com/SadeghPM/pusher-server-go/releases) page, or build from source with `go build .`.
2. Place the binary in your desired installation directory (e.g., `/usr/local/bin` or `/opt/pusher-clone`).
3. Create a `config.yaml` file in the same directory. Here is an example configuration:

```yaml
port: "6001"
metrics_port: "9601"
dashboard_port: "5174"
host: "https://pusher.example.com"
admin_token: "your-super-secret-admin-token"
debug: false
apps:
  - app_id: "your-app-id-1"
    app_key: "your-app-key-1"
    app_secret: "your-app-secret-1"
    allowed_origins: ["http://localhost:3000", "https://your-production-app.com"]
```

> [!NOTE]
> `host` is the public base URL of the Pusher server. It is shown as the **Pusher URL** in each tenant's App credentials so third-party apps can connect in production. Use the full `https://` URL with no path. If omitted, it defaults to `http://localhost:<port>`.
>
> If `allowed_origins` is empty or omitted, the server will allow connections from any origin.

4. Run the server binary:

```bash
./pusher-clone
```

---

## Web Interface

The dashboard port serves both the project landing page and the admin control room.

- **Home**: `http://localhost:5174/`
- **Admin**: `http://localhost:5174/admin`
- **Authentication**: Secured using the `admin_token` configured in `config.yaml`. The token is stored in the browser after sign-in.

With the control room, you can:
- Create, switch and delete tenants without restarting the server.
- Track active channels and client connections, with pause/resume and filtering for the live event stream.
- Trigger test events to verify connection state.
- Copy app credentials — including the public **Pusher URL** from `host` — and edit allowed origins and webhook URLs.
- Toggle between dark and light themes.

---

## Observability & Metrics

The server exports Prometheus metrics on port `9601` by default (at the `/metrics` endpoint).

### Health check

The main server exposes an unauthenticated `GET /health` endpoint on its own port (default `6001`). It returns `200` with a JSON body while the process is up, so it can be used by load balancers, uptime monitors, and the systemd service:

```json
{"status":"ok","apps_count":2}
```

The dashboard port additionally provides `GET /api/health` (default `5174`, also unauthenticated).

The following metrics are exported:

| Metric | Type | Description |
| :--- | :--- | :--- |
| `pusher_active_connections` | Gauge | Current number of active WebSocket connections per app. |
| `pusher_channels_active` | Gauge | Current number of active channels per app. |
| `pusher_messages_published_total` | Counter | Total number of messages published per app. |
| `pusher_rest_api_events_total` | Counter | Total number of events published via the REST API per app. |
| `pusher_websocket_errors_total` | Counter | Total number of WebSocket errors per app (broken down by read/write/ping). |

---

## Contributing

Thank you for considering contributing to Pusher Server Go! You can contribute by opening issues, submitting pull requests, or improving documentation.

## License

Pusher Server Go is open-source software licensed under the [MIT license](LICENSE).
