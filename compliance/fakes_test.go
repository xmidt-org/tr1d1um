// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/wrp-go/v5"
)

// ---------------------------------------------------------------------------
// The fake XMiDT cluster.
//
// tr1d1um talks to XMiDT (scytale) over two routes: it POSTs msgpack WRP
// messages to {targetURL}/device and GETs {targetURL}/device/{id}/stat.  The
// fake answers for the devices tests have connected and 404s for every other
// device, as scytale does when talaria has no such device.  Every WRP a
// device receives is recorded so a test can inspect what tr1d1um sent.
// ---------------------------------------------------------------------------

// spanHeader is the header scytale adds to every response; the stat route
// forwards every X- header from XMiDT.
const spanHeader = "X-Xmidt-Span"

// received is one WRP a device got, with the HTTP request it arrived in.
type received struct {
	msg    wrp.Message
	header http.Header
}

// reply is how a device answers a WRP.  A zero Status answers 200 with a
// WRP response carrying Payload; otherwise Status and Payload are sent as
// they are, standing in for an error scytale would return.
type reply struct {
	Status  int
	Payload string
	Delay   time.Duration
}

// okReply answers every request with a 200 status code in the payload.
func okReply(*wrp.Message) reply {
	return reply{Payload: `{"statusCode":200}`}
}

// device is a connected device.
type device struct {
	id       string
	stat     string
	handler  func(*wrp.Message) reply
	received chan received
}

type fakeXmidt struct {
	server *httptest.Server
	lock   sync.Mutex
	// devices by canonical device ID
	devices map[string]*device
}

func newFakeXmidt() *fakeXmidt {
	f := &fakeXmidt{devices: map[string]*device{}}
	r := mux.NewRouter()
	r.HandleFunc("/api/v3/device", f.handleWRP).Methods(http.MethodPost)
	r.HandleFunc("/api/v3/device/{id}/stat", f.handleStat).Methods(http.MethodGet)
	f.server = httptest.NewServer(r)

	return f
}

func (f *fakeXmidt) URL() string { return f.server.URL }
func (f *fakeXmidt) Close()      { f.server.Close() }

// connect registers a device whose replies come from handler and returns
// it.  The device is disconnected when the test ends.
func (f *fakeXmidt) connect(t *testing.T, handler func(*wrp.Message) reply) *device {
	t.Helper()
	id := newMAC()
	d := &device{
		id:       id,
		stat:     fmt.Sprintf(`{"id":%q,"pending":0,"statistics":{"bytesSent":0,"messagesSent":0,"bytesReceived":0,"messagesReceived":0,"duplications":0,"connectedAt":%q,"upTime":"1s"}}`, id, time.Now().UTC().Format(time.RFC3339)),
		handler:  handler,
		received: make(chan received, 64),
	}

	f.lock.Lock()
	f.devices[id] = d
	f.lock.Unlock()

	t.Cleanup(func() {
		f.lock.Lock()
		delete(f.devices, id)
		f.lock.Unlock()
	})

	return d
}

func (f *fakeXmidt) lookup(id string) (*device, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()
	d, ok := f.devices[id]

	return d, ok
}

func notFound(w http.ResponseWriter) {
	w.Header().Set(spanHeader, "fake-xmidt,1ms")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"message":"device not found"}`))
}

func (f *fakeXmidt) handleStat(w http.ResponseWriter, r *http.Request) {
	d, ok := f.lookup(mux.Vars(r)["id"])
	if !ok {
		notFound(w)
		return
	}

	w.Header().Set(spanHeader, "fake-xmidt,1ms")
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(d.stat))
}

func (f *fakeXmidt) handleWRP(w http.ResponseWriter, r *http.Request) {
	var msg wrp.Message
	if err := wrp.NewDecoder(r.Body, wrp.Msgpack).Decode(&msg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"message":"fake xmidt: bad wrp: %v"}`, err)
		return
	}

	id, _, _ := strings.Cut(msg.Destination, "/")
	d, ok := f.lookup(id)
	if !ok {
		notFound(w)
		return
	}

	select {
	case d.received <- received{msg: msg, header: r.Header.Clone()}:
	default:
	}

	rep := d.handler(&msg)
	if rep.Delay > 0 {
		select {
		case <-time.After(rep.Delay):
		case <-r.Context().Done():
			return
		}
	}

	w.Header().Set(spanHeader, "fake-xmidt,1ms")
	if rep.Status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(rep.Status)
		_, _ = w.Write([]byte(rep.Payload))
		return
	}

	response := wrp.Message{
		Type:            msg.Type,
		Source:          msg.Destination,
		Destination:     msg.Source,
		TransactionUUID: msg.TransactionUUID,
		ContentType:     "application/json",
		Payload:         []byte(rep.Payload),
	}
	var body []byte
	if err := wrp.NewEncoderBytes(&body, wrp.Msgpack).Encode(&response, wrp.NoStandardValidation()); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/msgpack")
	_, _ = w.Write(body)
}

// next returns the next WRP the device received, failing the test if none
// arrives in time.
func (d *device) next(t *testing.T) received {
	t.Helper()
	select {
	case got := <-d.received:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the device did not receive a WRP from tr1d1um")
		return received{}
	}
}

// none asserts the device received nothing.
func (d *device) none(t *testing.T) {
	t.Helper()
	select {
	case got := <-d.received:
		t.Fatalf("the device received a WRP it should not have: %s", got.msg.Payload)
	case <-time.After(200 * time.Millisecond):
	}
}

// payload decodes the JSON command in a WRP.
func (rcv received) payload(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rcv.msg.Payload, &out), "WRP payload should be JSON: %q", rcv.msg.Payload)

	return out
}

// newMAC returns a random device ID in the locally administered MAC range,
// so it can never be a real device.
func newMAC() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}

	return "mac:020000" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------------------
// The fake Argus.
//
// tr1d1um stores webhooks through ancla's chrysom client, which uses three
// routes of Argus' store API: GET {store}/{bucket} lists a bucket's items,
// PUT {store}/{bucket}/{id} creates (201) or replaces (200) one, and DELETE
// {store}/{bucket}/{id} removes one.  Items carry a TTL in seconds; the fake
// drops an item once it has elapsed, as Argus does.
// ---------------------------------------------------------------------------

type storedItem struct {
	item    map[string]any
	owner   string
	expires time.Time
}

type fakeArgus struct {
	server *httptest.Server
	lock   sync.Mutex
	items  map[string]storedItem // by bucket + "/" + id
}

func newFakeArgus() *fakeArgus {
	f := &fakeArgus{items: map[string]storedItem{}}
	r := mux.NewRouter()
	r.HandleFunc("/api/v1/store/{bucket}", f.handleList).Methods(http.MethodGet)
	r.HandleFunc("/api/v1/store/{bucket}/{id}", f.handlePut).Methods(http.MethodPut)
	r.HandleFunc("/api/v1/store/{bucket}/{id}", f.handleDelete).Methods(http.MethodDelete)
	f.server = httptest.NewServer(r)

	return f
}

func (f *fakeArgus) URL() string { return f.server.URL }
func (f *fakeArgus) Close()      { f.server.Close() }

// reset forgets every stored item.
func (f *fakeArgus) reset() {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items = map[string]storedItem{}
}

func (f *fakeArgus) handleList(w http.ResponseWriter, r *http.Request) {
	bucket := mux.Vars(r)["bucket"] + "/"
	now := time.Now()

	f.lock.Lock()
	items := make([]map[string]any, 0, len(f.items))
	for key, stored := range f.items {
		if !strings.HasPrefix(key, bucket) {
			continue
		}
		if !stored.expires.IsZero() && now.After(stored.expires) {
			delete(f.items, key)
			continue
		}
		items = append(items, stored.item)
	}
	f.lock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func (f *fakeArgus) handlePut(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	var item map[string]any
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	stored := storedItem{item: item, owner: r.Header.Get("X-Xmidt-Owner")}
	if ttl, ok := item["ttl"].(float64); ok && ttl > 0 {
		stored.expires = time.Now().Add(time.Duration(ttl) * time.Second)
	}

	key := vars["bucket"] + "/" + vars["id"]
	f.lock.Lock()
	_, existed := f.items[key]
	f.items[key] = stored
	f.lock.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if existed {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_ = json.NewEncoder(w).Encode(item)
}

func (f *fakeArgus) handleDelete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["bucket"] + "/" + vars["id"]

	f.lock.Lock()
	stored, ok := f.items[key]
	delete(f.items, key)
	f.lock.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stored.item)
}
