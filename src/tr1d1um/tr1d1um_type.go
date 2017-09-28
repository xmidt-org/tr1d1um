package main

import (
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/key"
)

//Tr1d1umConfig wraps all the config
type Tr1d1umConfig struct {
	Addr              string   `json:"addr"`
	PprofAddr         string   `json:"pprofaddr"`
	Cert              string   `json:"cert"`
	Key               string   `json:"key"`
	AuthKey           []string `json:"authKey"`
	HandlerTimeout    string   `json:"handlerTimeout"`
	HTTPTimeout       string   `json:"httpTimeout"`
	HealthInterval    string   `json:"healthInterval"`
	Version           string   `json:"version"`
	MaxTCPConns       int64    `json:"maxApiTcpConnections"`
	MaxHealthTCPConns int64    `json:"maxHealthTcpConnections"`
	ServiceList       []string `json:"serviceList"`
	WrpSource         string   `json:"wrpSource"`
	TargetURL         string   `json:"targetURL"`

	JWTValidators []struct {
		// JWTKeys is used to create the key.Resolver for JWT verification keys
		Keys key.ResolverFactory `json:"keys"`

		// Custom is an optional configuration section that defines
		// custom rules for validation over and above the standard RFC rules.
		Custom secure.JWTValidatorFactory `json:"custom"`
	} `json:"jwtValidators"`
}

//JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory
}
