# Callback Module

This module provides asynchronous callbacks for resource change events.  
When resource status changes, an event is pushed automatically to the configured URL.

## Features

- Zero-intrusion design: only one line changed in `rpcs/frontback.go`
- Metadata-driven: monitored commands are configured in a registry
- Asynchronous delivery: does not block core business flow
- Retry support: failed sends can be retried
- Type safety: resource types are defined by enums
- Easy to extend: add new resource monitoring by registry config

## Module Structure

```text
callback/
├── event.go
├── event_clone.go
├── config.go
├── queue.go
├── worker.go
├── metadata.go
├── rows.go
├── querys.go
├── README.md
└── test/
```

## Configuration

Add the following in `conf/config.toml`:

```toml
[callback]
enabled = true
url = "https://example.com/api/v1/resource-changes"
api_key = "your-api-key"
region = "cn-shanghai"
tls_insecure_skip_verify = false
workers = 3
queue_size = 10000
timeout = 30
retry_max = 3
retry_interval = 5
```

## Monitored Resources

Current registered commands include:

- instance: `launch_vm`, `action_vm`, `migrate_vm`
- volume: `create_volume_wds_vhost`, `attach_volume_wds_vhost`, `detach_volume_wds_vhost`, `resize_volume`
- image: `create_image`, `capture_image`
- interface: `attach_vm_nic`

## Testing

See test usage in [`test/README.md`](test/README.md).

## License

Apache-2.0
