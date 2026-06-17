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
├── pkg/contract    # low-level RPC wire contract
├── pkg/client           # sharded RPC client implementation
└── sdk.go               # public constructors
```

---

## Public API

The root package exposes two constructors:

- `New(cfg)` — creates a client for tests, debug flows, or non-TLS environments
- `NewWithTLS(cfg, tlsConfig)` — creates a client for production mTLS communication
- `NewWithMTLS(cfg, mtlsConfig)` — creates a client from PEM-encoded mTLS certificate material

## Configuration

The transport client is configured with `client.Configuration`.

```go
type Configuration struct {
	// Comma-separated list of shard addresses.
    // By default: cloud.mygaru.com:7937
    Addrs string

	// JWT token used for authentication.
	JwtToken []byte

    //Maximum allowed duration for a request.
    //If zero, a default timeout is used.
    MaxRequestDuration time.Duration

    //Maximum allowed time for establishing a TCP connection.
    //If zero, it falls back to `MaxRequestDuration`.
    MaxDialDuration    time.Duration

	// How often hostnames are re-resolved.
	// If zero, the default refresh interval is used.
	// If negative, periodic refresh is disabled.
	DNSRefreshInterval time.Duration

    // Maximum number of in-flight requests per underlying transport client.
    // By default: 8.
    MaxPendingRequests int

    // Number of underlying transport connections per configured shard address.
    // By default: 128.
    MaximumSimultaneousConnections int

	// Transport buffer sizes in bytes.
    ReadBufferSize  int
    WriteBufferSize int
}
```

## Throughput Sizing

`MaximumSimultaneousConnections` defines how many underlying transport connections are opened for each configured shard address.
The default value is `128`.

Use this conservative formula to estimate client-side throughput:

```text
requests_per_second = shard_count * MaximumSimultaneousConnections * (1000 / average_rpc_latency_ms)
```

With the default `MaximumSimultaneousConnections = 128`, one shard address, and an average RPC latency of `1 ms`:

```text
requests_per_second = 1 * 128 * (1000 / 1)
requests_per_second = 128,000 RPC/s
```

For multiple shard addresses, multiply by the number of addresses in `Configuration.Addrs`:

```text
requests_per_second = shard_count * 128,000
```

For example, with `3` shard addresses and `1 ms` average latency:

```text
requests_per_second = 3 * 128 * (1000 / 1)
requests_per_second = 384,000 RPC/s
```

To choose `MaximumSimultaneousConnections` for a target load:

```text
MaximumSimultaneousConnections = ceil(target_requests_per_second * average_rpc_latency_ms / (1000 * shard_count))
```

Example: to handle `50,000 RPC/s` with `2 ms` average latency and one shard address:

```text
MaximumSimultaneousConnections = ceil(50,000 * 2 / (1000 * 1))
MaximumSimultaneousConnections = 100
```

`MaxPendingRequests` is the per-connection limit for in-flight requests. The default is `8`.
If the application sends enough concurrent requests and the server supports pipelining efficiently, the theoretical transport ceiling is:

```text
requests_per_second = shard_count * MaximumSimultaneousConnections * MaxPendingRequests * (1000 / average_rpc_latency_ms)
```

With default values and `1 ms` average latency:

```text
requests_per_second = 1 * 128 * 8 * (1000 / 1)
requests_per_second = 1,024,000 RPC/s
```

Treat this as an upper bound. Real throughput also depends on server capacity, network latency distribution, payload size, CPU, TLS overhead, and how much concurrency the application actually produces.

## mTLS Configuration

Production clients should use mTLS. The SDK provides `NewWithMTLS`, which validates the client certificate with `gitlab.adtelligent.com/awesome/mtls`, builds a `tls.Config`, and then creates the RPC client through `NewWithTLS`.

Example:

```go
package main

import (
	"crypto/x509"
	"os"
	"time"

	dcr "github.com/mygaru/dcr-sdk"
	"github.com/mygaru/dcr-sdk/pkg/client"
)

func main() {
	clientCertPEM, err := os.ReadFile("./certs/client.pem")
	if err != nil {
		panic(err)
	}
	clientKeyPEM, err := os.ReadFile("./certs/client-key.pem")
	if err != nil {
		panic(err)
	}
	serverCAPEM, err := os.ReadFile("./certs/server-ca.pem")
	if err != nil {
		panic(err)
	}
	clientCAPEM, err := os.ReadFile("./certs/client-ca.pem")
	if err != nil {
		panic(err)
	}

	serverRoots := x509.NewCertPool()
	if !serverRoots.AppendCertsFromPEM(serverCAPEM) {
		panic("cannot parse server CA")
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(clientCAPEM) {
		panic("cannot parse client CA")
	}

	rpc, err := dcr.NewWithMTLS(&client.Configuration{
		Addrs:                          "cloud.mygaru.com:7937",
		JwtToken:                       []byte("JWT_TOKEN"),
		MaxRequestDuration:             time.Second,
		MaximumSimultaneousConnections: 128,
	}, dcr.MTLSConfig{
		CertPEM:         clientCertPEM,
		KeyPEM:          clientKeyPEM,
		ServerRootCAs:   serverRoots,
		ServerName:      "cloud.mygaru.com",
		ClientCertRoots: clientRoots,
	})
	if err != nil {
		panic(err)
	}

	_ = rpc
}
```

Use `ServerRootCAs` to verify the DCR RPC server certificate. Use `ClientCertRoots` to validate the client certificate before the SDK opens the connection. If `ClientCertRoots` is nil, `mtls.CheckTLS` falls back to the system root CA pool.

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
   Uids: []*base.UID{
      {Id: []byte("SOME_OTP_HERE"), Type: base.UID_OTP},
      {Id: []byte("019d2555-7874-7e9d-a284-9b45a0b2f165"), Type: base.UID_DEVICE_ID},
      {Id: []byte("AAABBBCCCDDDEEE"), Type: base.UID_EXTERNAL_UID},
   },
   // List of segments to be checked
   Match: []*base.Match_Rule{
      {TrafficType: base.TrafficType_TRAFFIC_TYPE_DISPLAY, SegmentIds: []uint32{1, 2, 3}},
      {TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, SegmentIds: []uint32{4, 5}},
   },
   // List of frequency keys to be checked;
   // Modes for operating the cap
   // - CAP_MODE_CAPPING = 0;  Increment frequency cap upon event
   // - CAP_MODE_FREEZE = 1;  Apply freeze and wait for an event

   Frequency: []*base.Frequency_Rule{
      {Key: 429496729345, Limit: &base.Frequency_Limit{AdsPerUser: 10, Period: 1, Mode: base.CAP_MODE_FREEZE, PeriodType:base.Frequency_Limit_TYPE_WEEK}},
      {Key: 429412463295, Limit: &base.Frequency_Limit{AdsPerUser: 1, Period: 1, PeriodType:base.Frequency_Limit_TYPE_MINUTE}},
   },

   Context: &base.Context{
      Ip: []byte("127.0.0.1"),
      Ua: []byte("MY USER AGENT HERE"),
      Url: []byte("https://www.site1.com"),
      Referrer: []byte("https://www.site2.com"),
   },
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
   TrackingId: []byte("0CA45E006B041868I1"),
   Event:      base.EventType_EVENT_TYPE_IMPRESSION,
   Rules: []*base.ReportRequest_Rule{
    {
		// Traffic type used for pricing
		TrafficType: base.TrafficType_TRAFFIC_TYPE_VIDEO, 
		
		// Amount of events, aka Impressions
		EventsCount: 1, 
		// List of segments used to make the decision
		SegmentIds: []uint32{1,2,3}, 
		
		// List of keys used to make the decision and which need to be increased
		Frequency: []uint64{429496729345}},
   },
   }
status, err := cli.Report(req)
if err != nil {
    panic(err)
}

if status == base.RPCServerResponseCode_NETWORK_ERROR {
   // The request can be resubmitted if the error is network related. 
   // The operation is idempotent.
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

 
### Important distinction

- `error != nil` usually means transport, timeout, marshal/unmarshal, or low-level RPC failure
- `status != OK` means the request reached the server, but the server returned a non-success application status

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
