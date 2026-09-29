# serverauth

`serverauth` keeps the RPC connection identity: the partner UUID read from the client certificate of an mTLS connection.

The SDK connects with mTLS only. `fastrpc` still accepts a plaintext client on a listener whose `Server.TLSConfig` is set - it exchanges its own handshake first and starts TLS only for clients that ask for it - so a server that serves mTLS only has to refuse the requests of a connection that carries no identity (see below).

## Server setup

Wrap the listener and pass `serverauth.NewTLSConfig` to `fastrpc.Server`:

```go
ln, err := net.Listen("tcp4", *listenAddr)
if err != nil {
	panic(err)
}

ln = serverauth.NewListener(ln)

server := &fastrpc.Server{
	SniffHeader:     sniffHeader,
	ProtocolVersion: protocolVersion,
	Handler:         Handler,
	NewHandlerCtx: func() fastrpc.HandlerCtx {
		return &contract.RequestCtx{}
	},
	TLSConfig: serverauth.NewTLSConfig(&tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}, serverauth.MTLSConfig{
		Roots:       clientRoots,
		Issuer:      clientIssuer,
		RequireOCSP: true,
	}),
	CompressType:     fastrpc.CompressSnappy,
	PipelineRequests: true,
}

go server.Serve(ln)
```

## Business handlers

A connection that completed the mTLS handshake carries the certificate's UUID; a plaintext one carries none and is refused:

```go
func Target(ctx *contract.RequestCtx) {
	// the partner the certificate was issued to; the payer itself travels in
	// the request
	partnerID, ok := serverauth.GetUUID(ctx.Conn())
	if !ok {
		handlers.WriteError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("mTLS client certificate is required"))
		return
	}

	_ = partnerID
}
```

## Notes

`SetUUID` binds an identity to a connection by other means. It is what the
deprecated per-connection `contract.Auth` handshake of pre-mTLS SDKs used, and
goes with it.
