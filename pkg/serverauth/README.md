# serverauth

`serverauth` keeps RPC connection identity in one place for both legacy `contract.Auth` and mTLS clients.

`fastrpc` can accept plaintext and TLS clients on the same listener when `Server.TLSConfig` is set. The library first exchanges its own handshake, then starts TLS only for clients that requested it. Because of that, a single server can support:

- old clients: plaintext connection, then explicit `contract.Auth` with JWT;
- new clients: mTLS connection, UUID read from the client certificate.

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

## Legacy `contract.Auth`

Keep the old auth handler, but write the UUID through the package helper:

```go
func Auth(ctx *contract.RequestCtx) {
	partnerID, err := oauth.ValidateTokenString(string(ctx.Request.Value()), oauth.ScopeCloudSegmentsTouch)
	if err != nil {
		handlers.WriteError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, err)
		return
	}

	payer := core.Partners.GetUUID(partnerID)
	if payer == nil {
		handlers.WriteError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("partner %s not found", partnerID))
		return
	}

	if err := serverauth.SetUUID(ctx.Conn(), payer.ID); err != nil {
		handlers.WriteError(ctx, base.RPCServerResponseCode_TECH_ERROR, err)
		return
	}

	ctx.Response.SetStatusCode(base.RPCServerResponseCode_OK)
}
```

## Business handlers

Every handler can read the authenticated UUID the same way, regardless of whether it came from JWT auth or mTLS:

```go
func Target(ctx *contract.RequestCtx) {
	payerID, ok := serverauth.GetUUID(ctx.Conn())
	if !ok {
		handlers.WriteError(ctx, base.RPCServerResponseCode_UNAUTHORIZED, fmt.Errorf("unauthorized"))
		return
	}

	_ = payerID
}
```

## Notes

Use one `fastrpc.Server` and one port when both old SDK clients and new mTLS clients should be accepted. Plaintext clients still need to call `contract.Auth`; mTLS clients can be treated as authenticated immediately after the TLS handshake.
