# Pusher Clone (Go)

A lightweight, in-memory, multi-tenant Pusher alternative written in Go, specifically designed to be compatible with Laravel's broadcasting system using the standard Pusher Protocol v7.

## Features

- Fully compatible with Pusher Protocol v7
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

## Running the Server

```bash
go run main.go
```

The server will start on the port specified in your `config.yaml` file (default: 8080).

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
        'port' => 8080,        // Your Go server's Port
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
sudo ./install.sh
```

The script will setup a multi-tenant `config.yaml` under `/opt/pusher-clone/config.yaml`.
