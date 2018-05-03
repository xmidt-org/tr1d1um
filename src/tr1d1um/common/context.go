package common

type contextKey int

const (
	ContextKeyRequestArrivalTime contextKey = iota
	ContextKeyResponseError
)
