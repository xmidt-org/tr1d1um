package hooks

import (
	"encoding/json"
	"fmt"
	kitlog "github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/xmidt-org/argus/chrysom"
	"github.com/xmidt-org/argus/model"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/webpa-common/webhook"
	"io/ioutil"
	"net/http"
)

// Options describes the parameters needed to configure the webhook endpoints
type Options struct {
	// APIRouter is assumed to be a subrouter with the API prefix path (i.e. 'api/v2')
	APIRouter *mux.Router

	Authenticate *alice.Chain

	Log                kitlog.Logger
	WebhookStoreConfig chrysom.ClientConfig
}

// ConfigHandler configures a given handler with webhook endpoints
func ConfigHandler(o *Options) {
	r, _ := NewRegistry(RegistryConfig{
		Logger:   o.Log,
		Listener: nil,
		Config:   o.WebhookStoreConfig,
	})

	o.APIRouter.Handle("/hook", o.Authenticate.ThenFunc(r.UpdateRegistry)).Methods(http.MethodPost)
	o.APIRouter.Handle("/hooks", o.Authenticate.ThenFunc(r.GetRegistry)).Methods(http.MethodGet)

}

type HookStore interface {
	chrysom.Pusher
	chrysom.Reader
}

type Registry struct {
	hookStore HookStore
	config    RegistryConfig
}

type RegistryConfig struct {
	Logger   kitlog.Logger
	Listener chrysom.ListenerFunc
	Config   chrysom.ClientConfig
}

func NewRegistry(config RegistryConfig) (*Registry, error) {
	argus, err := chrysom.CreateClient(config.Config, chrysom.WithLogger(config.Logger))
	if err != nil {
		return nil, err
	}
	if config.Listener != nil {
		argus.SetListener(config.Listener)
	}
	return &Registry{
		config:    config,
		hookStore: argus,
	}, nil
}

// jsonResponse is an internal convenience function to write a json response
func jsonResponse(rw http.ResponseWriter, code int, msg string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(code)
	rw.Write([]byte(fmt.Sprintf(`{"message":"%s"}`, msg)))
}

// update is an api call to processes a listener registration for adding and updating
func (r *Registry) GetRegistry(rw http.ResponseWriter, req *http.Request) {
	owner := ""
	// get Owner
	if auth, ok := bascule.FromContext(req.Context()); ok {
		owner = auth.Token.Principal()
	}

	items, err := r.hookStore.GetItems(owner)
	if err != nil {
		// this should never happen
		jsonResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	hooks := []webhook.W{}
	for _, item := range items {
		hook, err := convertItemToWebhook(item)
		if err != nil {
			continue
		}
		hooks = append(hooks, hook)
	}

	data, err := json.Marshal(&hooks)
	if err != nil {
		// this should never happen
		jsonResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	rw.WriteHeader(http.StatusOK)
	rw.Write(data)
}

// update is an api call to processes a listener registration for adding and updating
func (r *Registry) UpdateRegistry(rw http.ResponseWriter, req *http.Request) {
	payload, err := ioutil.ReadAll(req.Body)

	w, err := webhook.NewW(payload, req.RemoteAddr)
	if err != nil {
		jsonResponse(rw, http.StatusBadRequest, err.Error())
		return
	}
	webhook := map[string]interface{}{}
	data, err := json.Marshal(&w)
	if err != nil {
		// this should never happen
		jsonResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}
	err = json.Unmarshal(data, &webhook)
	if err != nil {
		// this should never happen
		jsonResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}

	owner := ""
	// get Owner
	if auth, ok := bascule.FromContext(req.Context()); ok {
		owner = auth.Token.Principal()
	}

	_, err = r.hookStore.Push(model.Item{
		Identifier: w.ID(),
		Data:       webhook,
		TTL:        r.config.Config.DefaultTTL,
	}, owner)
	if err != nil {
		jsonResponse(rw, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResponse(rw, http.StatusOK, "Success")
}

func convertItemToWebhook(item model.Item) (webhook.W, error) {
	hook := webhook.W{}
	tempBytes, err := json.Marshal(&item.Data)
	if err != nil {
		return hook, err
	}
	err = json.Unmarshal(tempBytes, &hook)
	if err != nil {
		return hook, err
	}
	return hook, nil
}
