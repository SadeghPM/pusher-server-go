# Pusher Clone (Go)

A lightweight, in-memory, multi-tenant Pusher alternative written in Go, specifically designed to be compatible with Laravel's broadcasting system using the standard Pusher Protocol v7.

## Features

- Real-time Admin Dashboard & Debug Console
- Fully compatible with Pusher Protocol v7
- Built-in Prometheus metrics exporter (`/metrics` endpoint)
- Multi-tenant: Support multiple Laravel applications with a single server instance
- Configured via YAML
- Supports public and private channels
- Event broadcasting from Laravel
- In-memory state management
- Implements Pusher signature validation for API requests and private channel subscriptions

## Setup and Installation

1. Make sure you have Go installed.
2. Clone this repository.
3. Install dependencies:
   ```bash
   go mod tidy
   ```
4. Copy the `config.yaml.example` file to `config.yaml`:
   ```bash
   cp config.yaml.example config.yaml
   ```
5. Update `config.yaml` with your app credentials. You can add as many apps as needed under the `apps` array.

## Configuration

The server relies on a `config.yaml` file for setup. Each app in your `apps` block requires an `app_id`, `app_key`, and `app_secret`.

To enhance security, you should configure the `allowed_origins` list for each app. If `allowed_origins` is omitted or empty, the server defaults to allowing connections from any origin.

```yaml
port: "6001"
metrics_port: "9601"
dashboard_port: "5174"
admin_token: "my-super-secret-admin-token"
debug: false
apps:
  - app_id: "my-app-id-1"
    app_key: "my-app-key-1"
    app_secret: "my-app-secret-1"
    allowed_origins: ["http://localhost:3000", "https://myproductionapp.com"]
```

## Running the Server

```bash
go run main.go
```

The server will start on the port specified in your `config.yaml` file (default: 6001).

## Admin Dashboard

The server includes a real-time admin dashboard with a debug console and event creator, accessible by default on port `5174`. It is secured using an `admin_token` configured in `config.yaml`.

## Observability & Metrics

The server exposes Prometheus metrics at the `/metrics` endpoint (on port 9601 by default, configurable via `metrics_port` in `config.yaml`) to help monitor the health, scale, and performance of your applications.

Available metrics include:
- `pusher_active_connections` (Gauge) - Current number of active WebSocket connections per app.
- `pusher_channels_active` (Gauge) - Current number of active channels per app.
- `pusher_messages_published_total` (Counter) - Total number of messages published per app.
- `pusher_rest_api_events_total` (Counter) - Total number of events published via the REST API per app.
- `pusher_websocket_errors_total` (Counter) - Total number of WebSocket errors per app (broken down by read/write/ping).

## Laravel Configuration

To use this clone in your Laravel application, update your `config/broadcasting.php` file:

```php
'pusher' => [
    'driver' => 'pusher',
    'key' => env('PUSHER_APP_KEY'),
    'secret' => env('PUSHER_APP_SECRET'),
    'app_id' => env('PUSHER_APP_ID'),
    'options' => [
        'host' => '127.0.0.1', // Your Go server's IP
        'port' => 6001,        // Your Go server's Port
        'scheme' => 'http',
        'encrypted' => false,
        'useTLS' => false,
    ],
],
```

Ensure the credentials in your Laravel `.env` match the ones in your `config.yaml` file.

## Production Installation

You can use the provided bash script to automatically download, configure, and install `pusher-clone` as a systemd service on Linux.

```bash
curl -sSL https://raw.githubusercontent.com/SadeghPM/pusher-server-go/main/install.sh | sudo bash
```

The script will setup a multi-tenant `config.yaml` under `/opt/pusher-clone/config.yaml`.
