package main

// webhooks.go - telling other systems what happened.
//
// When something happens (an alert is sent, someone answers it, screens are
// locked...) Foghorn POSTs a small JSON document to every webhook that wants
// that event. Each delivery is signed with the webhook's secret so the
// receiver can check it really came from Foghorn:
//
//   X-Foghorn-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>.<body>">
//   X-Foghorn-Timestamp: <unix seconds>
//
// Deliveries happen in the background and never hold up the thing that
// caused them. A failed delivery is retried a few times, then dropped.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// webhookEvents is every event a webhook can ask for.
var webhookEvents = map[string]string{
	"alert.sent":         "An alert was sent",
	"alert.acknowledged": "Someone pressed “I’ve read this”",
	"alert.responded":    "Someone pressed one of an alert’s reply buttons",
	"alert.recalled":     "An alert was recalled",
	"lock.started":       "Screens were locked",
	"lock.unlocked":      "Screens were unlocked",
}

var webhookRetryDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second, 2 * time.Minute}

type webhookPayload struct {
	ID        string    `json:"id"`
	Event     string    `json:"event"`
	CreatedAt time.Time `json:"created_at"`
	Org       string    `json:"org"`
	Data      any       `json:"data"`
}

type webhookJob struct {
	hook    Webhook
	payload []byte
	event   string
	id      string
}

type webhookSender struct {
	queue  chan webhookJob
	client *http.Client
	wg     sync.WaitGroup
}

func newWebhookSender() *webhookSender {
	return &webhookSender{
		queue:  make(chan webhookJob, 2000),
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
}

func signWebhook(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func wantsEvent(h *Webhook, event string) bool {
	if event == "ping" {
		return true
	}
	if len(h.Events) == 0 {
		return true
	}
	return hasScope(h.Events, event)
}

// emit queues event for every webhook that wants it. Safe to call from any
// handler, but not while holding the store lock.
func (a *App) emit(event string, data any) {
	if a.hooks == nil {
		return
	}
	a.store.mu.Lock()
	org := a.store.State.Settings.OrgName
	var targets []Webhook
	for _, h := range a.store.State.Webhooks {
		if h.Enabled && wantsEvent(h, event) {
			targets = append(targets, *h)
		}
	}
	a.store.mu.Unlock()
	a.queueTo(targets, event, org, data)
}

func (a *App) queueTo(targets []Webhook, event, org string, data any) {
	if len(targets) == 0 {
		return
	}
	p := webhookPayload{ID: newID(), Event: event, CreatedAt: time.Now().UTC(), Org: org, Data: data}
	body, err := json.Marshal(p)
	if err != nil {
		log.Printf("ERROR webhook payload for %s: %v", event, err)
		return
	}
	for _, h := range targets {
		select {
		case a.hooks.queue <- webhookJob{hook: h, payload: body, event: event, id: p.ID}:
		default:
			log.Printf("webhook %q: queue full, dropped %s", h.Name, event)
		}
	}
}

// runWebhooks delivers queued events with a few workers until stop closes.
func (a *App) runWebhooks(stop <-chan struct{}) {
	for i := 0; i < 4; i++ {
		a.hooks.wg.Add(1)
		go func() {
			defer a.hooks.wg.Done()
			for {
				select {
				case <-stop:
					return
				case job := <-a.hooks.queue:
					a.deliver(job, stop)
				}
			}
		}()
	}
}

func (a *App) deliver(job webhookJob, stop <-chan struct{}) {
	var lastErr string
	for attempt, wait := range webhookRetryDelays {
		if wait > 0 {
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
		}
		lastErr = a.postOnce(job, attempt+1)
		if lastErr == "" {
			break
		}
	}
	a.store.mu.Lock()
	for _, h := range a.store.State.Webhooks {
		if h.ID == job.hook.ID {
			h.LastAt = time.Now()
			if h.LastError != lastErr {
				h.LastError = lastErr
				a.store.dirtyState = true
			}
		}
	}
	a.store.mu.Unlock()
	if lastErr != "" {
		log.Printf("webhook %q: gave up on %s %s: %s", job.hook.Name, job.event, job.id, lastErr)
	}
}

// postOnce makes one delivery attempt and returns "" on success.
func (a *App) postOnce(job webhookJob, attempt int) string {
	req, err := http.NewRequest(http.MethodPost, job.hook.URL, bytes.NewReader(job.payload))
	if err != nil {
		return err.Error()
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Foghorn/"+version)
	req.Header.Set("X-Foghorn-Event", job.event)
	req.Header.Set("X-Foghorn-Delivery", job.id)
	req.Header.Set("X-Foghorn-Attempt", strconv.Itoa(attempt))
	req.Header.Set("X-Foghorn-Timestamp", ts)
	req.Header.Set("X-Foghorn-Signature", signWebhook(job.hook.Secret, ts, job.payload))
	resp, err := a.hooks.client.Do(req)
	if err != nil {
		return err.Error()
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Sprintf("answered HTTP %d", resp.StatusCode)
	}
	return ""
}

// ---------- admin API ----------

type webhookView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	LastAt    time.Time `json:"last_at"`
	LastError string    `json:"last_error"`
}

func viewWebhook(h *Webhook) webhookView {
	ev := h.Events
	if ev == nil {
		ev = []string{}
	}
	return webhookView{h.ID, h.Name, h.URL, ev, h.Enabled, h.CreatedAt, h.LastAt, h.LastError}
}

func (a *App) handleListWebhooks(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	out := make([]webhookView, 0, len(a.store.State.Webhooks))
	for _, h := range a.store.State.Webhooks {
		out = append(out, viewWebhook(h))
	}
	a.store.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"webhooks": out, "events": webhookEvents})
}

func (a *App) handleSaveWebhook(w http.ResponseWriter, r *http.Request, id *identity) {
	var req struct {
		Name         string   `json:"name"`
		URL          string   `json:"url"`
		Events       []string `json:"events"`
		Enabled      *bool    `json:"enabled"`
		RotateSecret bool     `json:"rotate_secret"`
	}
	if !readJSON(w, r, &req, 16<<10) {
		return
	}
	req.Name = cleanText(req.Name, 60)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "Name the webhook after what receives it, e.g. “School website”.")
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(req.URL) > 500 {
		writeErr(w, http.StatusBadRequest, "invalid", "The address must be a full http:// or https:// URL.")
		return
	}
	events := []string{}
	for _, e := range req.Events {
		if _, ok := webhookEvents[e]; !ok {
			writeErr(w, http.StatusBadRequest, "invalid", "Unknown event “"+e+"”.")
			return
		}
		if !hasScope(events, e) {
			events = append(events, e)
		}
	}
	hid := r.PathValue("id")
	a.store.mu.Lock()
	var h *Webhook
	if hid == "" {
		h = &Webhook{ID: newID(), CreatedAt: time.Now(), Enabled: true, Secret: "whsec_" + randomSecret(24)}
		a.store.State.Webhooks = append(a.store.State.Webhooks, h)
	} else {
		for _, x := range a.store.State.Webhooks {
			if x.ID == hid {
				h = x
			}
		}
		if h == nil {
			a.store.mu.Unlock()
			writeErr(w, http.StatusNotFound, "not_found", "That webhook no longer exists.")
			return
		}
	}
	h.Name, h.URL, h.Events = req.Name, req.URL, events
	if req.Enabled != nil {
		h.Enabled = *req.Enabled
	}
	secret := ""
	if hid == "" || req.RotateSecret {
		if hid != "" {
			h.Secret = "whsec_" + randomSecret(24)
		}
		secret = h.Secret
	}
	a.store.dirtyState = true
	v := viewWebhook(h)
	a.store.mu.Unlock()
	a.save()
	log.Printf("webhook %q -> %s saved by %s", v.Name, v.URL, id.User.Username)
	writeJSON(w, http.StatusOK, map[string]any{"webhook": v, "secret": secret})
}

func (a *App) handleDeleteWebhook(w http.ResponseWriter, r *http.Request, id *identity) {
	hid := r.PathValue("id")
	a.store.mu.Lock()
	keep := a.store.State.Webhooks[:0]
	for _, x := range a.store.State.Webhooks {
		if x.ID != hid {
			keep = append(keep, x)
		}
	}
	a.store.State.Webhooks = keep
	a.store.dirtyState = true
	a.store.mu.Unlock()
	a.save()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTestWebhook sends a "ping" straight away and reports what happened,
// so whoever is setting it up can see it working (or why not).
func (a *App) handleTestWebhook(w http.ResponseWriter, r *http.Request, id *identity) {
	hid := r.PathValue("id")
	a.store.mu.Lock()
	var h Webhook
	found := false
	for _, x := range a.store.State.Webhooks {
		if x.ID == hid {
			h, found = *x, true
		}
	}
	org := a.store.State.Settings.OrgName
	a.store.mu.Unlock()
	if !found {
		writeErr(w, http.StatusNotFound, "not_found", "That webhook no longer exists.")
		return
	}
	p := webhookPayload{ID: newID(), Event: "ping", CreatedAt: time.Now().UTC(), Org: org,
		Data: map[string]string{"message": "Test from Foghorn, sent by " + id.User.Username}}
	body, _ := json.Marshal(p)
	problem := a.postOnce(webhookJob{hook: h, payload: body, event: "ping", id: p.ID}, 1)
	if problem != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": problem})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- idempotency ----------

// idemCache remembers which alert each Idempotency-Key produced, for a day.
type idemCache struct {
	gate sync.Mutex // held across "look up, send, remember" so two retries cannot both send
	mu   sync.Mutex
	m    map[string]idemEntry
}

type idemEntry struct {
	alertID string
	expires time.Time
}

func newIdemCache() *idemCache { return &idemCache{m: map[string]idemEntry{}} }

func (c *idemCache) lock() func() {
	c.gate.Lock()
	return c.gate.Unlock
}

func (c *idemCache) get(key string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		return ""
	}
	return e.alertID
}

func (c *idemCache) put(key, alertID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if len(c.m) > 10000 {
		for k, e := range c.m {
			if now.After(e.expires) {
				delete(c.m, k)
			}
		}
	}
	c.m[key] = idemEntry{alertID, now.Add(24 * time.Hour)}
}
