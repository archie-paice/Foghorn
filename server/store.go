package main

// store.go - data model and persistence.
//
// Everything lives in a few JSON files inside the data directory:
//   state.json   users, API tokens, saved groups, templates, sounds,
//                schedules, webhooks, settings
//   alerts.json  alert history including per-computer delivery records
//   locks.json   screen locks, current and recent, with per-computer state
//   clients.json computers that have checked in
//   sounds/      uploaded sound files, named by their id
//
// There is deliberately no database. A college with a few thousand PCs fits
// comfortably in memory, and "copy one folder" is the whole backup story.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	maxAlertsKept           = 300 // older alerts are dropped from history
	maxAlertsWithDeliveries = 60  // older alerts keep only their totals
	maxLocksKept            = 100
	clientForgetAfter       = 45 * 24 * time.Hour
)

type User struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	DisplayName     string    `json:"display_name"`
	Role            string    `json:"role"` // "admin" or "sender"
	PassHash        string    `json:"pass_hash"`
	MustChange      bool      `json:"must_change"`
	Disabled        bool      `json:"disabled"`
	AllowedGroupIDs []string  `json:"allowed_group_ids"` // empty = may send to anyone
	CanLock         bool      `json:"can_lock"`          // may lock screens (admins always can)
	CreatedAt       time.Time `json:"created_at"`
	LastLogin       time.Time `json:"last_login"`
}

type Token struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hash      string    `json:"hash"`             // sha256 of the token, hex
	Scopes    []string  `json:"scopes,omitempty"` // send | lock | read; nil = made before 1.1, which means send + read
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// Rule matches computers. Field is one of hostname, user, ou, group, ip.
type Rule struct {
	Field   string `json:"field"`
	Pattern string `json:"pattern"`
}

type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Rules []Rule `json:"rules"`
}

type Target struct {
	All      bool     `json:"all"`
	GroupIDs []string `json:"group_ids"`
	Rules    []Rule   `json:"rules"`
}

// AlertSpec is what a person fills in on the Send page.
type AlertSpec struct {
	Title          string `json:"title"`
	Message        string `json:"message"`
	Level          string `json:"level"`   // info | warning | critical
	Display        string `json:"display"` // corner | center | fullscreen
	RequireAck     bool   `json:"require_ack"`
	DisplaySeconds int    `json:"display_seconds"` // 0 = stays until closed
	Sound          bool   `json:"sound"`
	Link           string `json:"link"`
	ExpiresMinutes int    `json:"expires_minutes"` // how long late-arriving computers still receive it
	Target         Target `json:"target"`

	// Appearance, from the builder. All optional: empty means "as the level".
	Accent       string   `json:"accent"`        // #rrggbb for the band and the full-screen background
	Label        string   `json:"label"`         // replaces Notice / Warning / Urgent in the band
	Icon         string   `json:"icon"`          // one of alertIcons
	DismissLabel string   `json:"dismiss_label"` // replaces "Dismiss" / "I've read this"
	Responses    []string `json:"responses"`     // reply buttons; picking one closes the alert and is recorded
	SoundID      string   `json:"sound_id"`      // an uploaded sound; "" = the Windows sound for the level
	SoundLoop    bool     `json:"sound_loop"`    // keep playing until the alert closes
}

type Template struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Spec AlertSpec `json:"spec"`
}

// Delivery is one computer+user's experience of one alert.
type Delivery struct {
	Hostname    string     `json:"hostname"`
	User        string     `json:"user"`
	SID         string     `json:"sid"` // client process session the alert was last sent to
	SentAt      time.Time  `json:"sent_at"`
	DisplayedAt *time.Time `json:"displayed_at,omitempty"`
	AckedAt     *time.Time `json:"acked_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	CloseReason string     `json:"close_reason,omitempty"` // dismissed | acknowledged | timeout | recalled
	Response    string     `json:"response,omitempty"`     // which reply button was pressed
	RecallSent  bool       `json:"recall_sent,omitempty"`
}

type Totals struct {
	Sent      int `json:"sent"`
	Displayed int `json:"displayed"`
	Acked     int `json:"acked"`
	// How many pressed each reply button, for alerts that ask a question.
	Responses map[string]int `json:"responses,omitempty"`
}

type Alert struct {
	ID            string               `json:"id"`
	Spec          AlertSpec            `json:"spec"`
	Rules         []Rule               `json:"rules"` // target flattened at send time
	TargetSummary string               `json:"target_summary"`
	Sender        string               `json:"sender"`
	SenderName    string               `json:"sender_name"`
	CreatedAt     time.Time            `json:"created_at"`
	ExpiresAt     time.Time            `json:"expires_at"`
	RecalledAt    *time.Time           `json:"recalled_at,omitempty"`
	RecalledBy    string               `json:"recalled_by,omitempty"`
	Deliveries    map[string]*Delivery `json:"deliveries,omitempty"`
	Archived      *Totals              `json:"archived,omitempty"` // set once deliveries are compacted away
}

func (a *Alert) totals() Totals {
	if a.Archived != nil {
		return *a.Archived
	}
	t := Totals{Sent: len(a.Deliveries)}
	for _, d := range a.Deliveries {
		if d.DisplayedAt != nil {
			t.Displayed++
		}
		if d.AckedAt != nil {
			t.Acked++
		}
	}
	return t
}

func (a *Alert) status(now time.Time) string {
	switch {
	case a.RecalledAt != nil:
		return "recalled"
	case now.After(a.ExpiresAt):
		return "finished"
	default:
		return "active"
	}
}

// LockSpec is what a person fills in to lock screens.
type LockSpec struct {
	Mode            string `json:"mode"` // blank (screen covered) | freeze (screen visible, input blocked)
	Message         string `json:"message"`
	DurationMinutes int    `json:"duration_minutes"` // unlocks itself after this long, server reachable or not
	Target          Target `json:"target"`
}

// LockState is one computer+user's experience of one lock.
type LockState struct {
	Hostname   string     `json:"hostname"`
	User       string     `json:"user"`
	SID        string     `json:"sid"`
	SentAt     time.Time  `json:"sent_at"`
	LockedAt   *time.Time `json:"locked_at,omitempty"`
	ReleasedAt *time.Time `json:"released_at,omitempty"`
	UnlockSent bool       `json:"unlock_sent,omitempty"`
}

type Lock struct {
	ID            string                `json:"id"`
	Spec          LockSpec              `json:"spec"`
	Rules         []Rule                `json:"rules"`
	TargetSummary string                `json:"target_summary"`
	Sender        string                `json:"sender"`
	SenderName    string                `json:"sender_name"`
	CreatedAt     time.Time             `json:"created_at"`
	EndsAt        time.Time             `json:"ends_at"`
	UnlockedAt    *time.Time            `json:"unlocked_at,omitempty"`
	UnlockedBy    string                `json:"unlocked_by,omitempty"`
	States        map[string]*LockState `json:"states"`
}

func (l *Lock) active(now time.Time) bool { return l.UnlockedAt == nil && now.Before(l.EndsAt) }

// Sound is an uploaded audio file, stored as sounds/<id><ext>.
type Sound struct {
	ID         string    `json:"id"` // from the content's sha256, so a PC's cached copy can never go stale
	Name       string    `json:"name"`
	Ext        string    `json:"ext"` // .wav | .mp3
	Size       int64     `json:"size"`
	UploadedBy string    `json:"uploaded_by"`
	UploadedAt time.Time `json:"uploaded_at"`
}

// Schedule sends an alert later: once, or on a weekly pattern.
type Schedule struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Spec      AlertSpec `json:"spec"`
	Once      string    `json:"once"` // server-local "2026-10-28T15:50"; "" when repeating
	Days      []int     `json:"days"` // 0=Sunday .. 6=Saturday, when repeating
	Time      string    `json:"time"` // "15:50", when repeating
	Enabled   bool      `json:"enabled"`
	Owner     string    `json:"owner"` // id of the account the alert is sent as
	CreatedAt time.Time `json:"created_at"`
	NextRun   time.Time `json:"next_run"`
	LastRun   time.Time `json:"last_run"`
	LastAlert string    `json:"last_alert"`
	LastError string    `json:"last_error"`
}

// Webhook is somewhere Foghorn tells about things as they happen.
type Webhook struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Secret    string    `json:"secret"` // signs each delivery (HMAC-SHA256)
	Events    []string  `json:"events"` // empty = every event
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	LastAt    time.Time `json:"last_at"`
	LastError string    `json:"last_error"`
}

type Settings struct {
	OrgName   string `json:"org_name"`
	ClientKey string `json:"client_key"`
}

type State struct {
	Settings  Settings    `json:"settings"`
	Users     []*User     `json:"users"`
	Tokens    []*Token    `json:"tokens"`
	Groups    []*Group    `json:"groups"`
	Templates []*Template `json:"templates"`
	Sounds    []*Sound    `json:"sounds"`
	Schedules []*Schedule `json:"schedules"`
	Webhooks  []*Webhook  `json:"webhooks"`
}

type ClientInfo struct {
	ID        string    `json:"id"`
	Hostname  string    `json:"hostname"`
	User      string    `json:"user"`
	Domain    string    `json:"domain"`
	OU        string    `json:"ou"`
	Groups    []string  `json:"groups"`
	IP        string    `json:"ip"`
	Version   string    `json:"version"`
	OS        string    `json:"os"`
	SID       string    `json:"sid"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	polling   int       // long-polls currently held open (not persisted)
}

func (c *ClientInfo) online(now time.Time) bool {
	return c.polling > 0 || now.Sub(c.LastSeen) < 45*time.Second
}

type Store struct {
	mu      sync.Mutex
	dir     string
	State   State
	Alerts  []*Alert // oldest first
	Locks   []*Lock  // oldest first
	Clients map[string]*ClientInfo

	dirtyState, dirtyAlerts, dirtyLocks, dirtyClients bool
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func randomSecret(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, Clients: map[string]*ClientInfo{}}
	if err := loadJSON(filepath.Join(dir, "state.json"), &s.State); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "alerts.json"), &s.Alerts); err != nil {
		return nil, err
	}
	if err := loadJSON(filepath.Join(dir, "locks.json"), &s.Locks); err != nil {
		return nil, err
	}
	for _, l := range s.Locks {
		if l.States == nil {
			l.States = map[string]*LockState{}
		}
	}
	var clients []*ClientInfo
	if err := loadJSON(filepath.Join(dir, "clients.json"), &clients); err != nil {
		return nil, err
	}
	for _, c := range clients {
		if c != nil && c.ID != "" {
			s.Clients[c.ID] = c
		}
	}
	for _, a := range s.Alerts {
		if a.Deliveries == nil && a.Archived == nil {
			a.Deliveries = map[string]*Delivery{}
		}
	}
	return s, nil
}

func loadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// writeFileAtomic writes to a temp file then renames, so a power cut mid-write
// never leaves a half-written state file behind.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// flush writes whatever is dirty. Marshalling happens under the lock (fast),
// disk I/O happens outside it.
func (s *Store) flush() error {
	s.mu.Lock()
	var stateB, alertsB, locksB, clientsB []byte
	var err error
	if s.dirtyState {
		stateB, err = json.MarshalIndent(&s.State, "", "  ")
		s.dirtyState = false
	}
	if err == nil && s.dirtyAlerts {
		s.compactLocked()
		alertsB, err = json.Marshal(s.Alerts)
		s.dirtyAlerts = false
	}
	if err == nil && s.dirtyLocks {
		if n := len(s.Locks); n > maxLocksKept {
			s.Locks = append([]*Lock(nil), s.Locks[n-maxLocksKept:]...)
		}
		locksB, err = json.Marshal(s.Locks)
		s.dirtyLocks = false
	}
	if err == nil && s.dirtyClients {
		list := make([]*ClientInfo, 0, len(s.Clients))
		for _, c := range s.Clients {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		clientsB, err = json.Marshal(list)
		s.dirtyClients = false
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if stateB != nil {
		if err := writeFileAtomic(filepath.Join(s.dir, "state.json"), stateB); err != nil {
			return err
		}
	}
	if alertsB != nil {
		if err := writeFileAtomic(filepath.Join(s.dir, "alerts.json"), alertsB); err != nil {
			return err
		}
	}
	if locksB != nil {
		if err := writeFileAtomic(filepath.Join(s.dir, "locks.json"), locksB); err != nil {
			return err
		}
	}
	if clientsB != nil {
		if err := writeFileAtomic(filepath.Join(s.dir, "clients.json"), clientsB); err != nil {
			return err
		}
	}
	return nil
}

// compactLocked trims history so the files stay small on big sites.
func (s *Store) compactLocked() {
	if n := len(s.Alerts); n > maxAlertsKept {
		s.Alerts = append([]*Alert(nil), s.Alerts[n-maxAlertsKept:]...)
	}
	now := time.Now()
	cut := len(s.Alerts) - maxAlertsWithDeliveries
	for i, a := range s.Alerts {
		if i >= cut {
			break
		}
		if a.Archived == nil && a.status(now) != "active" {
			t := a.totals()
			t.Responses = a.tally()
			a.Archived = &t
			a.Deliveries = nil
		}
	}
	for id, c := range s.Clients {
		if c.polling == 0 && now.Sub(c.LastSeen) > clientForgetAfter {
			delete(s.Clients, id)
			s.dirtyClients = true
		}
	}
}

// ---- lookups (callers hold s.mu) ----

func (s *Store) userByName(name string) *User {
	for _, u := range s.State.Users {
		if equalFold(u.Username, name) {
			return u
		}
	}
	return nil
}

func (s *Store) userByID(id string) *User {
	for _, u := range s.State.Users {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Store) groupByID(id string) *Group {
	for _, g := range s.State.Groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func (s *Store) alertByID(id string) *Alert {
	for i := len(s.Alerts) - 1; i >= 0; i-- {
		if s.Alerts[i].ID == id {
			return s.Alerts[i]
		}
	}
	return nil
}

func (s *Store) lockByID(id string) *Lock {
	for i := len(s.Locks) - 1; i >= 0; i-- {
		if s.Locks[i].ID == id {
			return s.Locks[i]
		}
	}
	return nil
}

func (s *Store) soundByID(id string) *Sound {
	for _, x := range s.State.Sounds {
		if x.ID == id {
			return x
		}
	}
	return nil
}

func (s *Store) adminCount() int {
	n := 0
	for _, u := range s.State.Users {
		if u.Role == "admin" && !u.Disabled {
			n++
		}
	}
	return n
}
