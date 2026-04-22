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

## Configuration

The transport client is configured with `client.Configuration`.

```go
type Configuration struct {
	// Comma-separated list of shard addresses.
    // By default: cloud.mygaru.com:7943
    Addrs string

    //Maximum allowed duration for a request.
    //If zero, a default timeout is used.
    MaxRequestDuration time.Duration

    //Maximum allowed time for establishing a TCP connection.
    //If zero, it falls back to `MaxRequestDuration`.
    MaxDialDuration    time.Duration

    //Maximum number of in-flight requests per underlying transport client.
    MaxPendingRequests int

    // Reserved for connection concurrency tuning.
    MaximumSimultaneousConnections int

	// Transport buffer sizes in bytes.
    ReadBufferSize  int
    WriteBufferSize int
}
```


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
