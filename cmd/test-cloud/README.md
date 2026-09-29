# test-cloud

`cmd/test-cloud` runs the SDK test RPC cloud. It uses the shared `internal/testcloud` implementation, so SDK tests and the command exercise the same Target and Report handlers.

Like the cloud, it serves mTLS connections only: the server certificate, its key and the CA of the client certificates are required, and a request on a connection that did not present a client certificate is refused with `UNAUTHORIZED`.

```sh
go run ./cmd/test-cloud \
  -listenAddr 127.0.0.1:7943 \
  -serverID 1024 \
  -tlsCert ./certs/server.pem \
  -tlsKey ./certs/server-key.pem \
  -clientCA ./certs/client-ca.pem
```

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
