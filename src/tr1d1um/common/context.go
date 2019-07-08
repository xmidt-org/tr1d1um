package common

type contextKey int

//Keys to important context values on incoming requests to TR1D1UM
const (
	ContextKeyRequestArrivalTime contextKey = iota
	ContextKeyRequestTID
	ContextKeyRequestWDMPParamNames
	ContextKeyRequestWDMPCommand
)
