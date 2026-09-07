package contract

type RPCRegister uint8

const (
	Unknown RPCRegister = iota
	Target  RPCRegister = 1
	Report  RPCRegister = 2

	// Auth was the per-connection authentication request.
	//
	// Deprecated: the SDK no longer sends it - the payer travels in every
	// Target/Report request instead. The identifier is kept reserved so the
	// cloud can keep serving SDK versions that still perform the handshake, and
	// so the number is never reused for something else.
	Auth RPCRegister = 4

	MaxRequestIdentifier = Report
)

func (r RPCRegister) String() string {
	switch r {
	case Target:
		return "target"
	case Report:
		return "report"
	case Auth:
		return "auth"
	default:
		panic("unknown")
	}

}
