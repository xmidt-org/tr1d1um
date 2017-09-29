package main

import (
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/key"
)

//JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory `json:"keys"`

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory `json:"custom"`
}
