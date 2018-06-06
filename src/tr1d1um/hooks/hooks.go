package hooks

import (
	"net/url"

	"github.com/Comcast/webpa-common/webhook"
	"github.com/Comcast/webpa-common/xmetrics"
	kitlog "github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

type HooksOptions struct {
	APIRouter    *mux.Router
	RootRouter   *mux.Router
	Authenticate *alice.Chain
	M            xmetrics.Registry
	Host         string
	HooksFactory *webhook.Factory
	Log          kitlog.Logger
	Scheme       string
}

func ConfigHandler(o *HooksOptions) {
	hooksRegistry, hooksHandler := o.HooksFactory.NewRegistryAndHandler(o.M)

	o.APIRouter.Handle("/hook", o.Authenticate.ThenFunc(hooksRegistry.UpdateRegistry))
	o.APIRouter.Handle("/hooks", o.Authenticate.ThenFunc(hooksRegistry.GetRegistry))

	selfURL := &url.URL{
		Scheme: o.Scheme,
		Host:   o.Host,
	}

	o.HooksFactory.Initialize(o.RootRouter, selfURL, hooksHandler, o.Log, o.M, nil)
	return
}
