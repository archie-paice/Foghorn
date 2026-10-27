package main

// locks.go - locking screens and blocking keyboard and mouse.
//
// A lock is like an alert that holds the screen: the client covers every
// monitor (or, in freeze mode, leaves the screen visible) and swallows the
// keyboard and mouse until the lock is lifted or runs out. Every lock has an
// end time that the client enforces on its own, so a room is never left
// locked because the server went away.

import (
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxLockMinutes = 8 * 60

type lockView struct {
	ID            string       `json:"id"`
	Spec          LockSpec     `json:"spec"`
	TargetSummary string       `json:"target_summary"`
	Sender        string       `json:"sender"`
	SenderName    string       `json:"sender_name"`
	CreatedAt     time.Time    `json:"created_at"`
	EndsAt        time.Time    `json:"ends_at"`
	UnlockedAt    *time.Time   `json:"unlocked_at,omitempty"`
	UnlockedBy    string       `json:"unlocked_by,omitempty"`
	Active        bool         `json:"active"`
	Sent          int          `json:"sent"`
	Locked        int          `json:"locked"` // computers showing the lock right now
	Computers     []*LockState `json:"computers,omitempty"`
}

func viewLock(l *Lock, now time.Time, withStates bool) lockView {
	v := lockView{
		ID: l.ID, Spec: l.Spec, TargetSummary: l.TargetSummary, Sender: l.Sender, SenderName: l.SenderName,
		CreatedAt: l.CreatedAt, EndsAt: l.EndsAt, UnlockedAt: l.UnlockedAt, UnlockedBy: l.UnlockedBy,
		Active: l.active(now), Sent: len(l.States),
	}
	for _, st := range l.States {
		if st.LockedAt != nil && st.ReleasedAt == nil && v.Active {
			v.Locked++
		}
		if withStates {
			cp := *st
			v.Computers = append(v.Computers, &cp)
		}
	}
	if withStates {
		if v.Computers == nil {
			v.Computers = []*LockState{}
		}
		sort.Slice(v.Computers, func(i, j int) bool {
			return strings.ToLower(v.Computers[i].Hostname) < strings.ToLower(v.Computers[j].Hostname)
		})
	}
	return v
}

func lockMatches(l *Lock, c *ClientInfo) bool {
	if l.Spec.Target.All {
		return true
	}
	return rulesMatch(l.Rules, c)
}

func validateLock(s *LockSpec) string {
	s.Message = cleanTextOpt(s.Message, 500, true)
	switch s.Mode {
	case "blank", "freeze":
	case "":
		s.Mode = "blank"
	default:
		return "Mode must be blank or freeze."
	}
	if s.DurationMinutes == 0 {
		s.DurationMinutes = 60
	}
	if s.DurationMinutes < 1 || s.DurationMinutes > maxLockMinutes {
		return "A lock can last between 1 minute and 8 hours."
	}
	s.Target.Rules = cleanRules(s.Target.Rules)
	if s.Target.GroupIDs == nil {
		s.Target.GroupIDs = []string{}
	}
	if !s.Target.All && len(s.Target.GroupIDs) == 0 && len(s.Target.Rules) == 0 {
		return "Choose which computers to lock."
	}
	if s.Target.All {
		s.Target.GroupIDs, s.Target.Rules = []string{}, []Rule{}
	}
	return ""
}

func (a *App) handleCreateLock(w http.ResponseWriter, r *http.Request, id *identity) {
	var spec LockSpec
	if !readJSON(w, r, &spec, 64<<10) {
		return
	}
	if msg := validateLock(&spec); msg != "" {
		writeErr(w, http.StatusBadRequest, "invalid", msg)
		return
	}
	now := time.Now()
	st := a.store
	st.mu.Lock()
	if msg := st.checkAllowed(&id.User, spec.Target); msg != "" {
		st.mu.Unlock()
		writeErr(w, http.StatusForbidden, "forbidden", msg)
		return
	}
	for _, gid := range spec.Target.GroupIDs {
		if st.groupByID(gid) == nil {
			st.mu.Unlock()
			writeErr(w, http.StatusBadRequest, "invalid", "One of the chosen groups no longer exists. Reload the page.")
			return
		}
	}
	rules, summary := st.flattenTarget(spec.Target)
	name := id.User.DisplayName
	if name == "" {
		name = id.User.Username
	}
	l := &Lock{
		ID: newID(), Spec: spec, Rules: rules, TargetSummary: summary,
		Sender: id.User.Username, SenderName: name, CreatedAt: now,
		EndsAt: now.Add(time.Duration(spec.DurationMinutes) * time.Minute),
		States: map[string]*LockState{},
	}
	st.Locks = append(st.Locks, l)
	st.dirtyLocks = true
	reach := 0
	for _, c := range st.Clients {
		if c.online(now) && lockMatches(l, c) {
			reach++
		}
	}
	v := viewLock(l, now, false)
	st.mu.Unlock()

	a.hub.wake()
	a.save()
	log.Printf("LOCK %s (%s, %d min) by %s on [%s] (%d online now)", l.ID, spec.Mode, spec.DurationMinutes, id.User.Username, summary, reach)
	a.emit("lock.started", map[string]any{"lock": v, "online_reach": reach})
	writeJSON(w, http.StatusCreated, map[string]any{"lock": v, "online_reach": reach})
}

func (a *App) handleListLocks(w http.ResponseWriter, r *http.Request, id *identity) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > maxLocksKept {
		limit = 50
	}
	activeOnly := r.URL.Query().Get("active") == "1" || r.URL.Query().Get("active") == "true"
	now := time.Now()
	a.store.mu.Lock()
	out := []lockView{}
	for i := len(a.store.Locks) - 1; i >= 0 && len(out) < limit; i-- {
		l := a.store.Locks[i]
		if activeOnly && !l.active(now) {
			continue
		}
		out = append(out, viewLock(l, now, false))
	}
	a.store.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleGetLock(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	l := a.store.lockByID(r.PathValue("id"))
	var v lockView
	if l != nil {
		v = viewLock(l, time.Now(), true)
	}
	a.store.mu.Unlock()
	if l == nil {
		writeErr(w, http.StatusNotFound, "not_found", "That lock is no longer in the history.")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// unlockLocked lifts one lock. Caller holds the store lock.
func (s *Store) unlockLocked(l *Lock, by string, now time.Time) bool {
	if !l.active(now) {
		return false
	}
	l.UnlockedAt = &now
	l.UnlockedBy = by
	s.dirtyLocks = true
	return true
}

func (a *App) handleUnlock(w http.ResponseWriter, r *http.Request, id *identity) {
	now := time.Now()
	a.store.mu.Lock()
	l := a.store.lockByID(r.PathValue("id"))
	if l == nil {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "not_found", "That lock is no longer in the history.")
		return
	}
	if id.User.Role != "admin" && l.Sender != id.User.Username {
		a.store.mu.Unlock()
		writeErr(w, http.StatusForbidden, "forbidden", "You can only unlock screens you locked. An administrator can unlock any.")
		return
	}
	changed := a.store.unlockLocked(l, id.User.Username, now)
	v := viewLock(l, now, false)
	a.store.mu.Unlock()
	if changed {
		a.hub.wake()
		a.save()
		log.Printf("LOCK %s lifted by %s", l.ID, id.User.Username)
		a.emit("lock.unlocked", map[string]any{"lock": v, "unlocked_by": id.User.Username})
	}
	writeJSON(w, http.StatusOK, v)
}

// handleUnlockAll lifts every active lock the caller is allowed to lift:
// all of them for an administrator, their own for anyone else.
func (a *App) handleUnlockAll(w http.ResponseWriter, r *http.Request, id *identity) {
	now := time.Now()
	var lifted []lockView
	a.store.mu.Lock()
	for _, l := range a.store.Locks {
		if id.User.Role != "admin" && l.Sender != id.User.Username {
			continue
		}
		if a.store.unlockLocked(l, id.User.Username, now) {
			lifted = append(lifted, viewLock(l, now, false))
		}
	}
	a.store.mu.Unlock()
	if len(lifted) > 0 {
		a.hub.wake()
		a.save()
		log.Printf("LOCK unlock-all by %s lifted %d lock(s)", id.User.Username, len(lifted))
		for _, v := range lifted {
			a.emit("lock.unlocked", map[string]any{"lock": v, "unlocked_by": id.User.Username})
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"unlocked": len(lifted)})
}

// pendingLocksFor works out which locks this client should apply or lift
// right now, and marks them as sent. Caller holds the store lock.
func (s *Store) pendingLocksFor(c *ClientInfo, now time.Time) (locks []wireLock, unlocks []string) {
	locks = []wireLock{}
	unlocks = []string{}
	for _, l := range s.Locks {
		d := l.States[c.ID]
		if !l.active(now) {
			// Tell a client still holding the lock to let go. It would let go
			// at the end time on its own; this covers early unlocks.
			if d != nil && d.LockedAt != nil && d.ReleasedAt == nil && !d.UnlockSent && now.Sub(l.EndsAt) < recallWindow {
				d.UnlockSent = true
				s.dirtyLocks = true
				unlocks = append(unlocks, l.ID)
			}
			continue
		}
		if !lockMatches(l, c) {
			continue
		}
		send := false
		switch {
		case d == nil:
			send = true
		case d.SID == c.SID:
			send = d.LockedAt == nil && now.Sub(d.SentAt) > resendAfter
		default:
			// The client restarted - logged off and on, rebooted, or was
			// ended in Task Manager. The lock still stands.
			send = true
		}
		if !send {
			continue
		}
		if d == nil {
			d = &LockState{Hostname: c.Hostname, User: c.User}
			l.States[c.ID] = d
		}
		if d.SID != c.SID {
			d.LockedAt, d.ReleasedAt = nil, nil
		}
		d.Hostname, d.User = c.Hostname, c.User
		d.SID = c.SID
		d.SentAt = now
		s.dirtyLocks = true
		left := int(l.EndsAt.Sub(now).Seconds())
		if left < 1 {
			left = 1
		}
		locks = append(locks, wireLock{
			ID: l.ID, Mode: l.Spec.Mode, Message: l.Spec.Message, Sender: l.SenderName,
			Org: s.State.Settings.OrgName, SecondsLeft: left,
		})
	}
	return locks, unlocks
}

// applyLockEvent records a client's "locked" or "released". Caller holds the store lock.
func (s *Store) applyLockEvent(c *ClientInfo, ev clientEvent, now time.Time) bool {
	l := s.lockByID(ev.LockID)
	if l == nil {
		return false
	}
	d := l.States[c.ID]
	if d == nil || d.SID != c.SID {
		return false // not a lock we sent to this client process
	}
	t := now
	switch ev.Event {
	case "locked":
		if d.LockedAt == nil {
			d.LockedAt = &t
			return true
		}
	case "released":
		if d.ReleasedAt == nil {
			if d.LockedAt == nil {
				d.LockedAt = &t
			}
			d.ReleasedAt = &t
			return true
		}
	}
	return false
}
