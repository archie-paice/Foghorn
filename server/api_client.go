package main

// api_client.go - the endpoints desktop clients talk to.
//
// Clients long-poll POST /api/client/poll. The request carries who they are and
// anything that happened since last time (alert shown, acknowledged, closed).
// The server holds the request open for up to ~25 seconds and answers the
// moment there is something to show. Long-polling is plain HTTP, so it works
// through web filters and proxies that break WebSockets.

import (
	"crypto/sha1"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	pollHold      = 25 * time.Second
	resendAfter   = 60 * time.Second // resend if a client never confirmed it displayed an alert
	recallWindow  = 12 * time.Hour
	maxGroupsKept = 300
)

// hub wakes every waiting long-poll when something changes.
type hub struct {
	mu sync.Mutex
	ch chan struct{}
}

func newHub() *hub { return &hub{ch: make(chan struct{})} }

func (h *hub) wait() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ch
}

func (h *hub) wake() {
	h.mu.Lock()
	close(h.ch)
	h.ch = make(chan struct{})
	h.mu.Unlock()
}

type clientHello struct {
	Hostname string   `json:"hostname"`
	User     string   `json:"user"`
	Domain   string   `json:"domain"`
	OU       string   `json:"ou"`
	Groups   []string `json:"groups"`
	Version  string   `json:"version"`
	OS       string   `json:"os"`
	SID      string   `json:"sid"`
}

type clientEvent struct {
	AlertID string `json:"alert_id"`
	LockID  string `json:"lock_id"`
	Event   string `json:"event"`  // alerts: displayed | acknowledged | dismissed | timeout | recalled; locks: locked | released
	Choice  string `json:"choice"` // the reply button pressed, with "acknowledged"
}

type pollRequest struct {
	Client clientHello   `json:"client"`
	Events []clientEvent `json:"events"`
	Wait   bool          `json:"wait"`
}

type wireAlert struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Message        string `json:"message"`
	Level          string `json:"level"`
	Display        string `json:"display"`
	RequireAck     bool   `json:"require_ack"`
	DisplaySeconds int    `json:"display_seconds"`
	Sound          bool   `json:"sound"`
	Link           string `json:"link"`
	Sender         string `json:"sender"`
	Org            string `json:"org"`
	CreatedAt      string `json:"created_at"`

	Accent       string   `json:"accent"`
	Label        string   `json:"label"`
	Icon         string   `json:"icon"` // the symbol itself, not its name
	DismissLabel string   `json:"dismiss_label"`
	Responses    []string `json:"responses"`
	SoundID      string   `json:"sound_id"`
	SoundExt     string   `json:"sound_ext"`
	SoundLoop    bool     `json:"sound_loop"`
}

type wireLock struct {
	ID          string `json:"id"`
	Mode        string `json:"mode"`
	Message     string `json:"message"`
	Sender      string `json:"sender"`
	Org         string `json:"org"`
	SecondsLeft int    `json:"seconds_left"` // relative, so a PC with a wrong clock still unlocks on time
}

type pollResponse struct {
	Alerts   []wireAlert `json:"alerts"`
	Recalled []string    `json:"recalled"`
	Locks    []wireLock  `json:"locks"`
	Unlocks  []string    `json:"unlocks"`
	RetryMS  int         `json:"retry_ms"`
	Server   string      `json:"server"`
}

func clientIDFor(hostname, domain, user string) string {
	sum := sha1.Sum([]byte(strings.ToLower(hostname + "|" + domain + `\` + user)))
	return hex.EncodeToString(sum[:8])
}

func (a *App) remoteIP(r *http.Request) string {
	if a.cfg.TrustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) handlePoll(w http.ResponseWriter, r *http.Request) {
	if !a.checkClientKey(r) {
		writeErr(w, http.StatusUnauthorized, "bad_client_key",
			"The client key is missing or wrong. Copy it from Settings in the web console.")
		return
	}
	var req pollRequest
	if !readJSON(w, r, &req, 256<<10) {
		return
	}
	h := req.Client
	h.Hostname = cleanText(h.Hostname, 64)
	h.User = cleanText(h.User, 104)
	if h.Hostname == "" || h.User == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "client.hostname and client.user are required.")
		return
	}
	ip := a.remoteIP(r)
	now := time.Now()

	st := a.store
	st.mu.Lock()
	c := st.touchClient(h, ip, now)
	c.polling++
	_, hooks := st.applyEvents(c, req.Events, now)
	st.mu.Unlock()
	for _, ev := range hooks {
		a.emit(ev.name, ev.data)
	}

	defer func() {
		st.mu.Lock()
		c.polling--
		c.LastSeen = time.Now()
		st.mu.Unlock()
	}()

	deadline := time.NewTimer(pollHold)
	defer deadline.Stop()

	for {
		// Grab the wake channel before looking, so a send that lands between
		// "look" and "wait" still wakes us.
		wakeCh := a.hub.wait()

		st.mu.Lock()
		alerts, recalled := st.pendingFor(c, time.Now())
		locks, unlocks := st.pendingLocksFor(c, time.Now())
		st.mu.Unlock()

		if len(alerts) > 0 || len(recalled) > 0 || len(locks) > 0 || len(unlocks) > 0 || !req.Wait || a.closing.Load() {
			writeJSON(w, http.StatusOK, pollResponse{
				Alerts: alerts, Recalled: recalled, Locks: locks, Unlocks: unlocks, RetryMS: 1000, Server: version,
			})
			return
		}
		select {
		case <-wakeCh:
		case <-deadline.C:
			writeJSON(w, http.StatusOK, pollResponse{Alerts: []wireAlert{}, Recalled: []string{},
				Locks: []wireLock{}, Unlocks: []string{}, RetryMS: 250, Server: version})
			return
		case <-r.Context().Done():
			return
		}
	}
}

// touchClient records a check-in. Caller holds the lock.
func (s *Store) touchClient(h clientHello, ip string, now time.Time) *ClientInfo {
	domain := cleanText(h.Domain, 64)
	id := clientIDFor(h.Hostname, domain, h.User)
	c := s.Clients[id]
	if c == nil {
		c = &ClientInfo{ID: id, FirstSeen: now}
		s.Clients[id] = c
		s.dirtyClients = true
	}
	groups := h.Groups
	if len(groups) > maxGroupsKept {
		groups = groups[:maxGroupsKept]
	}
	clean := make([]string, 0, len(groups))
	for _, g := range groups {
		if g = cleanText(g, 256); g != "" {
			clean = append(clean, g)
		}
	}
	sid := cleanText(h.SID, 64)
	if c.SID != sid || c.IP != ip || c.OU != h.OU || len(c.Groups) != len(clean) {
		s.dirtyClients = true
	}
	if now.Sub(c.LastSeen) > 5*time.Minute {
		s.dirtyClients = true // keep "last seen" on disk roughly current
	}
	c.Hostname, c.User, c.Domain = h.Hostname, h.User, domain
	c.OU = cleanText(h.OU, 512)
	c.Groups = clean
	c.IP = ip
	c.Version = cleanText(h.Version, 32)
	c.OS = cleanText(h.OS, 128)
	c.SID = sid
	c.LastSeen = now
	return c
}

// hookEvent is a webhook event worked out under the store lock and sent after it is released.
type hookEvent struct {
	name string
	data map[string]any
}

// applyEvents records what the client says happened, and returns the webhook
// events that follow from it. Caller holds the lock.
func (s *Store) applyEvents(c *ClientInfo, events []clientEvent, now time.Time) (bool, []hookEvent) {
	changed := false
	var hooks []hookEvent
	if len(events) > 200 {
		events = events[:200]
	}
	for _, ev := range events {
		if ev.LockID != "" {
			if s.applyLockEvent(c, ev, now) {
				s.dirtyLocks = true
			}
			continue
		}
		a := s.alertByID(ev.AlertID)
		if a == nil || a.Deliveries == nil {
			continue
		}
		d := a.Deliveries[c.ID]
		if d == nil {
			continue // we never sent this alert to this client
		}
		t := now
		switch ev.Event {
		case "displayed":
			if d.DisplayedAt == nil {
				d.DisplayedAt = &t
				changed = true
			}
		case "acknowledged":
			if d.DisplayedAt == nil {
				d.DisplayedAt = &t
			}
			if d.AckedAt == nil {
				d.AckedAt = &t
				d.ClosedAt = &t
				d.CloseReason = "acknowledged"
				changed = true
				// Only a reply the alert actually offered is recorded.
				for _, r := range a.Spec.Responses {
					if r == ev.Choice {
						d.Response = r
					}
				}
				name := "alert.acknowledged"
				if d.Response != "" {
					name = "alert.responded"
				}
				hooks = append(hooks, hookEvent{name, map[string]any{
					"alert_id": a.ID, "title": a.Spec.Title, "hostname": d.Hostname, "user": d.User,
					"domain": c.Domain, "response": d.Response, "at": t,
				}})
			}
		case "dismissed", "timeout", "recalled":
			if d.ClosedAt == nil {
				d.ClosedAt = &t
				d.CloseReason = ev.Event
				changed = true
			}
		}
	}
	if changed {
		s.dirtyAlerts = true
	}
	return changed, hooks
}

// pendingFor works out what this client should be told right now, and marks
// those alerts as sent. Caller holds the lock.
func (s *Store) pendingFor(c *ClientInfo, now time.Time) (alerts []wireAlert, recalled []string) {
	alerts = []wireAlert{}
	recalled = []string{}
	for _, a := range s.Alerts {
		if a.Deliveries == nil {
			continue
		}
		d := a.Deliveries[c.ID]

		if a.RecalledAt != nil {
			// Tell a client to take down anything it may still be showing.
			if d != nil && d.ClosedAt == nil && !d.RecallSent && now.Sub(*a.RecalledAt) < recallWindow {
				d.RecallSent = true
				s.dirtyAlerts = true
				recalled = append(recalled, a.ID)
			}
			continue
		}
		if now.After(a.ExpiresAt) || !alertMatches(a, c) {
			continue
		}

		send := false
		switch {
		case d == nil:
			send = true
		case d.SID == c.SID:
			// Same client process. Only resend if it never confirmed display.
			send = d.DisplayedAt == nil && now.Sub(d.SentAt) > resendAfter
		default:
			// The client restarted (logoff/logon, reboot). Show again if it was
			// never seen, or if it needs an acknowledgement we do not have yet.
			send = d.DisplayedAt == nil || (a.Spec.RequireAck && d.AckedAt == nil)
		}
		if !send {
			continue
		}
		if d == nil {
			d = &Delivery{Hostname: c.Hostname, User: c.User}
			a.Deliveries[c.ID] = d
		}
		d.SID = c.SID
		d.SentAt = now
		s.dirtyAlerts = true

		wa := wireAlert{
			ID: a.ID, Title: a.Spec.Title, Message: a.Spec.Message,
			Level: a.Spec.Level, Display: a.Spec.Display,
			RequireAck: a.Spec.RequireAck, DisplaySeconds: a.Spec.DisplaySeconds,
			Sound: a.Spec.Sound, Link: a.Spec.Link,
			Sender: a.SenderName, Org: s.State.Settings.OrgName,
			CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
			Accent:    a.Spec.Accent, Label: a.Spec.Label, Icon: alertIcons[a.Spec.Icon],
			DismissLabel: a.Spec.DismissLabel, Responses: a.Spec.Responses, SoundLoop: a.Spec.SoundLoop,
		}
		if wa.Responses == nil {
			wa.Responses = []string{}
		}
		if snd := s.soundByID(a.Spec.SoundID); snd != nil {
			wa.SoundID, wa.SoundExt = snd.ID, snd.Ext
		}
		alerts = append(alerts, wa)
	}
	return alerts, recalled
}
