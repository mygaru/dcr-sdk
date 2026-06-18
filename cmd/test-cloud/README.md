# test-cloud

`cmd/test-cloud` runs the SDK test RPC cloud. It uses the shared `internal/testcloud` implementation, so SDK tests and the command exercise the same Auth, Target, and Report handlers.

Run plaintext mode for legacy JWT authentication:

```sh
go run ./cmd/test-cloud \
  -listenAddr 127.0.0.1:7943 \
  -serverID 1024
```

In plaintext mode, clients must call `contract.Auth` first. The test JWT is simply a UUID string stored in the request body.

Run mTLS mode:

```sh
go run ./cmd/test-cloud \
  -listenAddr 127.0.0.1:7943 \
  -serverID 1024 \
  -tlsCert ./certs/server.pem \
  -tlsKey ./certs/server-key.pem \
  -clientCA ./certs/client-ca.pem
```

When mTLS is enabled, the same server can still accept legacy plaintext clients. mTLS clients are authenticated during the TLS handshake, so they do not need to call `contract.Auth`.

Enable OCSP checks for mTLS client certificates:

```sh
go run ./cmd/test-cloud \
  -listenAddr 127.0.0.1:7943 \
  -tlsCert ./certs/server.pem \
  -tlsKey ./certs/server-key.pem \
  -clientCA ./certs/client-ca.pem \
  -clientIssuer ./certs/client-issuer.pem \
  -requireOCSP
```
