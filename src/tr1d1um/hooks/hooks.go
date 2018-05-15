package hooks

import (
	"fmt"
	"net/url"

	"github.com/Comcast/webpa-common/webhook"
	"github.com/Comcast/webpa-common/xmetrics"
	kitlog "github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

type HooksOptions struct {
	R            *mux.Router
	Authenticate *alice.Chain
	M            xmetrics.Registry
	Host         string
	HooksFactory *webhook.Factory
	Log          kitlog.Logger
	Scheme       string
	ApiBase      string
}

func ConfigHandler(o *HooksOptions) {
	hooksRegistry, hooksHandler := o.HooksFactory.NewRegistryAndHandler(o.M)

	o.R.Handle(
		fmt.Sprintf("%s/hook", o.ApiBase),
		o.Authenticate.ThenFunc(hooksRegistry.UpdateRegistry),
	)
	o.R.Handle(
		fmt.Sprintf("%s/hooks", o.ApiBase),
		o.Authenticate.ThenFunc(hooksRegistry.GetRegistry),
	)

	selfURL := &url.URL{
		Scheme: o.Scheme,
		Host:   o.Host,
	}

	o.HooksFactory.Initialize(o.R, selfURL, hooksHandler, o.Log, o.M, nil)
	return
}
