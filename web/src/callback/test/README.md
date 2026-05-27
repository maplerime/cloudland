# Callback Test Server

A simple HTTP server for testing CloudLand resource-change callback events.

## Features

- Receives and parses resource change events
- Prints event details in real time
- Tracks event statistics
- Provides health check endpoint

## Build

```bash
go build -o callback_test_server callback_test_server.go
```

## Usage

```bash
./callback_test_server
./callback_test_server -port 9000
./callback_test_server -verbose
```

Flags:

- `-host string`: listen host (default `0.0.0.0`)
- `-port int`: listen port (default `8080`)
- `-verbose`: enable verbose periodic stats logging

## Endpoints

- `POST /api/v1/resource-changes`: receive callback events
- `GET /stats`: view server statistics
- `GET /health`: health check

## Quick Test

```bash
./start_server.sh
./test_callback.sh
```

## CloudLand Config Example

See `config.example.toml`.

## License

Apache-2.0
