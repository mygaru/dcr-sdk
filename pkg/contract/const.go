package contract

type RPCRegister uint8

const (
	Unknown RPCRegister = iota
	Target  RPCRegister = 1
	Report  RPCRegister = 2
	Touch   RPCRegister = 3

	// Auth was the per-connection authentication request.
	//
	// Deprecated: the SDK no longer sends it - the payer travels in every
	// Target/Report request instead. The identifier is kept reserved so the
	// cloud can keep serving SDK versions that still perform the handshake, and
	// so the number is never reused for something else.
	Auth RPCRegister = 4

	// Link links the identifiers of a request to a subscriber of the calling
	// telecom.
	Link RPCRegister = 5

	// Ident turns the tracking id of a Target into the user's id for a
	// partner.
	Ident RPCRegister = 6

	MaxRequestIdentifier = Ident
)

func (r RPCRegister) String() string {
	switch r {
	case Target:
		return "target"
	case Report:
		return "report"
	case Touch:
		return "touch"
	case Auth:
		return "auth"
	case Link:
		return "link"
	case Ident:
		return "ident"
	default:
		panic("unknown")
	}

}
