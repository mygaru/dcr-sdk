package contract

type RPCRegister uint8

const (
	Unknown RPCRegister = iota
	Target  RPCRegister = 1
	Report  RPCRegister = 2
	Auth    RPCRegister = 4

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
