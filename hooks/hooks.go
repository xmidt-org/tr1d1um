package hooks

import (
	"net/http"
	"net/url"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/xmidt-org/webpa-common/webhook"
	"github.com/xmidt-org/webpa-common/xmetrics"
)

// Options describes the parameters needed to configure the webhook endpoints
type Options struct {

	//APIRouter is assumed to be a subrouter with the API prefix path (i.e. 'api/v2')
	APIRouter *mux.Router

	//RootRouter is the main Tr1d1um router
	RootRouter *mux.Router //Router with empty path prefix

	Authenticate *alice.Chain

	SoAProvider string

	M xmetrics.Registry

	Host         string
	HooksFactory *webhook.Factory
	Log          kitlog.Logger
	Scheme       string
}

// ConfigHandler configures a given handler with webhook endpoints
func ConfigHandler(o *Options) {
	hooksRegistry, hooksHandler := o.HooksFactory.NewRegistryAndHandler(o.M)

	o.APIRouter.Handle("/hook", o.Authenticate.ThenFunc(hooksRegistry.UpdateRegistry)).Methods(http.MethodPost)
	o.APIRouter.Handle("/hooks", o.Authenticate.ThenFunc(hooksRegistry.GetRegistry)).Methods(http.MethodGet)

	selfURL := &url.URL{
		Scheme: o.Scheme,
		Host:   o.Host,
	}

	//Initialize must take the router without any prefixes
	o.HooksFactory.Initialize(o.RootRouter, selfURL, o.SoAProvider, hooksHandler, o.Log, o.M, time.Now)
}
