PROTO_FILES := $(wildcard base/v1/*.proto)

proto:
	protoc -I . \
		--go_out=. \
		--go-grpc_out=. \
		--go_opt=module=github.com/mygaru/dcr-sdk \
		--go-grpc_opt=module=github.com/mygaru/dcr-sdk \
		$(PROTO_FILES)