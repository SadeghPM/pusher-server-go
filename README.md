<p align="center">
  <a href="https://laravel.com" target="_blank">
    <img src="https://raw.githubusercontent.com/laravel/art/master/logo-lockup/5%20SVG/2%20COLOURED%20light.svg" width="350" alt="Laravel Logo">
  </a>
</p>

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

### Requirements

- Go 1.24 or higher installed on your system.

### Step 1: Clone the Repository

Clone the project to your local machine or server:

```bash
git clone https://github.com/SadeghPM/pusher-server-go.git
cd pusher-server-go
```

### Step 2: Install Go Dependencies

Run `go mod tidy` to download all necessary packages:

```bash
go mod tidy
```

### Step 3: Configure the Server

Copy the example configuration file:

```bash
cp config.yaml.example config.yaml
```

Open `config.yaml` and configure your settings:

```yaml
port: "6001"
metrics_port: "9601"
dashboard_port: "5174"
admin_token: "your-super-secret-admin-token"
debug: false
apps:
  - app_id: "your-app-id-1"
    app_key: "your-app-key-1"
    app_secret: "your-app-secret-1"
    allowed_origins: ["http://localhost:3000", "https://your-production-app.com"]
```

> [!NOTE]
> If `allowed_origins` is empty or omitted, the server will allow connections from any origin.

### Step 4: Run the Server

Start the Go application:

```bash
go run main.go
```

The WebSocket server will start running on the port defined in your configuration file (default is `6001`).

---

## Laravel Configuration

To use Pusher Server Go in your Laravel application, configure the `pusher` connection in `config/broadcasting.php`:

```php
'pusher' => [
    'driver' => 'pusher',
    'key' => env('PUSHER_APP_KEY'),
    'secret' => env('PUSHER_APP_SECRET'),
    'app_id' => env('PUSHER_APP_ID'),
    'options' => [
        'host' => env('PUSHER_HOST', '127.0.0.1'),
        'port' => env('PUSHER_PORT', 6001),
        'scheme' => env('PUSHER_SCHEME', 'http'),
        'encrypted' => false,
        'useTLS' => false,
    ],
],
```

Next, update your application's `.env` file:

```env
BROADCAST_CONNECTION=pusher
PUSHER_APP_ID=your-app-id-1
PUSHER_APP_KEY=your-app-key-1
PUSHER_APP_SECRET=your-app-secret-1
PUSHER_HOST=127.0.0.1
PUSHER_PORT=6001
PUSHER_SCHEME=http
```

Make sure the credentials match the ones configured in your server's `config.yaml`.

---

## Admin Dashboard

The server includes a real-time admin dashboard with a debug console and event creator.
- **Default URL**: `http://localhost:5174`
- **Authentication**: Secured using the `admin_token` configured in `config.yaml`.

With the dashboard, you can:
- Track active channels and client connections.
- View real-time log messages and broadcasted events.
- Trigger test events to verify connection state.

---

## Observability & Metrics

The server exports Prometheus metrics on port `9601` by default (at the `/metrics` endpoint).

The following metrics are exported:

| Metric | Type | Description |
| :--- | :--- | :--- |
| `pusher_active_connections` | Gauge | Current number of active WebSocket connections per app. |
| `pusher_channels_active` | Gauge | Current number of active channels per app. |
| `pusher_messages_published_total` | Counter | Total number of messages published per app. |
| `pusher_rest_api_events_total` | Counter | Total number of events published via the REST API per app. |
| `pusher_websocket_errors_total` | Counter | Total number of WebSocket errors per app (broken down by read/write/ping). |

---

## Production Deployment

To automatically download, configure, and run Pusher Server Go as a systemd service on Linux, run the following command:

```bash
curl -sSL https://raw.githubusercontent.com/SadeghPM/pusher-server-go/main/install.sh | sudo bash
```

This script will set up the project under `/opt/pusher-clone` and configure the configuration file at `/opt/pusher-clone/config.yaml`.

---

## Contributing

Thank you for considering contributing to Pusher Server Go! You can contribute by opening issues, submitting pull requests, or improving documentation.

## License

Pusher Server Go is open-source software licensed under the [MIT license](LICENSE).
