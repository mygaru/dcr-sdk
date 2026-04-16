package contract

type RPCRegister uint8

const (
	Unknown RPCRegister = iota
	Target  RPCRegister = 1
	Report  RPCRegister = 2
	Mock    RPCRegister = 3
	Auth    RPCRegister = 4

	MaxRequestIdentifier = Mock
)

func (r RPCRegister) String() string {
	switch r {
	case Target:
		return "target"
	case Report:
		return "report"
	case Mock:
		return "mock"
	case Auth:
		return "auth"
	default:
		panic("unknown")
	}

}
