package main

import (
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/key"
)

type Tr1d1umConfig struct {
	Addr              string   `json:"addr"`
	PprofAddr         string   `json:"pprofaddr"`
	Cert              string   `json:"cert"`
	Key               string   `json:"key"`
	AuthKey           []string `json:"authKey"`
	HandlerTimeout    string   `json:"handlerTimeout"`
	HttpTimeout       string   `json:"httpTimeout"`
	HealthInterval    string   `json:"healthInterval"`
	Version           string   `json:"version"`
	MaxApiTcpConns    int64    `json:"maxApiTcpConnections"`
	MaxHealthTcpConns int64    `json:"maxHealthTcpConnections"`
	ServiceList       []string `json:"serviceList"`
	WrpSource         string   `json:"wrpSource"`
	targetURL         string   `json:"targetUrl"`

	JWTValidators []struct {
		// JWTKeys is used to create the key.Resolver for JWT verification keys
		Keys key.ResolverFactory `json:"keys"`

		// Custom is an optional configuration section that defines
		// custom rules for validation over and above the standard RFC rules.
		Custom secure.JWTValidatorFactory `json:"custom"`
	} `json:"jwtValidators"`
}

type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory
}
