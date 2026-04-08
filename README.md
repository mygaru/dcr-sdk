# dcr-sdk

`dcr-sdk` is a Go client SDK for communicating with the DCR RPC service.

It provides a lightweight RPC client built on top of a custom binary protocol and a sharded transport layer for high-throughput request routing.

## Features

- Go SDK for DCR RPC services
- Sharded client with round-robin request distribution
- Low-overhead binary protocol
- Snappy-compressed transport
- Built on top of `fastrpc`
- Optional mTLS support for production environments
- Request/response protobuf contracts

---

## Installation

```bash
go get github.com/mygaru/dcr-sdk
```

---

## Package Layout

```text
.
├── base/v1              # protobuf schemas
├── gen/base1            # generated protobuf Go code
├── internal/contract    # low-level RPC wire contract
├── pkg/client           # sharded RPC client implementation
└── sdk.go               # public constructors
```

---

## Public API

The root package exposes two constructors:

- `New(cfg)` — creates a client for tests, debug flows, or non-TLS environments
- `NewWithTLS(cfg, tlsConfig)` — creates a client for production mTLS communication

## Configuration

The transport client is configured with `client.Configuration`.

```go
type Configuration struct {
    Addrs string

    MaxRequestDuration time.Duration
    MaxDialDuration    time.Duration

    MaxPendingRequests int

    MaximumSimultaneousConnections int

    ReadBufferSize  int
    WriteBufferSize int
}
```

### Fields

#### `Addrs`
Comma-separated list of shard addresses.

Example:

```go
Addrs: "127.0.0.1:9001,127.0.0.1:9002,127.0.0.1:9003"
```

#### `MaxRequestDuration`
Maximum allowed duration for a request.

If zero, a default timeout is used.

#### `MaxDialDuration`
Maximum allowed time for establishing a TCP connection.

If zero, it falls back to `MaxRequestDuration`.

#### `MaxPendingRequests`
Maximum number of in-flight requests per underlying transport client.

#### `MaximumSimultaneousConnections`
Reserved for connection concurrency tuning.

#### `ReadBufferSize` / `WriteBufferSize`
Transport buffer sizes in bytes.

---

## Main RPC Methods

### `Target`

`Target` is the main request used by a third-party platform to:

- check whether the user belongs to one or more target segments
- validate frequency capping
- determine matching quality / identification quality

```go
resp, status, err := cli.Target(req)
```

Example:

```go
req := &base.TargetRequest{
    // Example only.
    // Fill fields according to your protobuf schema.
}

resp, status, err := cli.Target(req)
if err != nil {
    panic(err)
}

if status != base.RPCServerResponseCode_OK {
    panic(status.String())
}

fmt.Printf("tracking_id=%s\n", resp.GetTrackingId())
fmt.Printf("match=%v\n", resp.GetMatch())
fmt.Printf("frequency=%v\n", resp.GetFrequency())
```

---

### `Report`

`Report` is used to submit event information back to the DCR service.

Typical use cases:

- impression reporting
- click reporting
- delivery accounting
- billing/statistics input

```go
status, err := cli.Report(req)
```

Example:

```go
req := &base.ReportRequest{
    // Fill according to your protobuf schema
}

status, err := cli.Report(req)
if err != nil {
    panic(err)
}

if status != base.RPCServerResponseCode_OK {
    panic(status.String())
}
```


## Protocol Overview

The SDK uses a custom binary RPC protocol over TCP.

### Transport characteristics

- TCP-based
- request/response protocol
- protobuf payloads
- Snappy compression
- fixed wire envelope
- request type identified by numeric RPC register
- maximum payload protection limit

### Request flow

1. A protobuf request is marshaled into bytes
2. The request is wrapped into an internal transport envelope
3. The envelope contains the RPC method identifier
4. The payload is sent via `fastrpc`
5. The server returns a response envelope with:
    - status code
    - optional response payload
6. If the status code is `OK`, the payload is unmarshaled into a protobuf response

---

## Wire Contract

At the transport layer, each request carries:

- **request name / method identifier**
- **binary payload**

The payload itself is the protobuf-serialized request body.

The response contains:

- **status code**
- **binary payload**

The payload is protobuf-serialized response data when the status code is successful.

### Compression

Transport compression uses **Snappy**.

### Payload size limit

The transport layer includes a maximum payload size limit to protect the server and client from unexpectedly large frames.

---

## Response Handling

Each RPC call returns:

1. protobuf response object or `nil`
2. `base.RPCServerResponseCode`
3. `error`

Typical handling pattern:

```go
resp, status, err := cli.Target(req)
if err != nil {
    // network error, marshal/unmarshal error, timeout, protocol error, etc.
    panic(err)
}

if status != base.RPCServerResponseCode_OK {
    // server-side application-level error
    panic(status.String())
}

_ = resp
```

### Important distinction

- `error != nil` usually means transport, timeout, marshal/unmarshal, or low-level RPC failure
- `status != OK` means the request reached the server, but the server returned a non-success application status

---

## Sharding Model

`ShardedClient` maintains multiple underlying transport clients.

The `Addrs` configuration field is a comma-separated list of shard addresses.

Requests are distributed across shard clients using a round-robin counter.

Example:

```go
cfg := &client.Configuration{
    Addrs: "10.0.0.1:9000,10.0.0.2:9000,10.0.0.3:9000",
}
```

This creates three internal transport clients and balances requests across them.

---

## Monitoring / Debugging

### Pending requests

You can inspect the current number of in-flight requests:

```go
n := cli.PendingRequests()
fmt.Println("pending:", n)
```

This is useful for:

- debugging transport overload
- monitoring request pressure
- observing backlog during latency spikes

---

## Error Cases

Typical error sources include:

- TCP dial failure
- request timeout
- pending requests overflow
- invalid server response
- protobuf marshal failure
- protobuf unmarshal failure
- non-OK application response

A standard calling pattern should always check both `error` and `status`.
