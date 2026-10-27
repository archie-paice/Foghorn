package main

// schedules.go - alerts sent later, once or every week.
//
// Times are the server's local time, which on a school network is the same
// as everybody else's. A schedule sends as the account that made it, with
// that account's restrictions, so scheduling is never a way round them.

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	onceLayout = "2006-01-02T15:04"
	// A run missed by more than this while the server was off is skipped, not
	// sent late: "save your work" at three in the morning helps nobody.
	missedGrace = 10 * time.Minute
)

// nextRun is when sc should next fire after the given moment, or zero.
func nextRun(sc *Schedule, after time.Time) time.Time {
	if sc.Once != "" {
		t, err := time.ParseInLocation(onceLayout, sc.Once, time.Local)
		if err != nil || !t.After(after) {
			return time.Time{}
		}
		return t
	}
	hm, err := time.Parse("15:04", sc.Time)
	if err != nil || len(sc.Days) == 0 {
		return time.Time{}
	}
	y, m, d := after.Date()
	for i := 0; i <= 7; i++ {
		t := time.Date(y, m, d+i, hm.Hour(), hm.Minute(), 0, 0, time.Local)
		if !t.After(after) {
			continue
		}
		for _, day := range sc.Days {
			if int(t.Weekday()) == day {
				return t
			}
		}
	}
	return time.Time{}
}

type scheduleRequest struct {
	Name    string    `json:"name"`
	Spec    AlertSpec `json:"spec"`
	Once    string    `json:"once"`
	Days    []int     `json:"days"`
	Time    string    `json:"time"`
	Enabled *bool     `json:"enabled"`
}

func (a *App) handleListSchedules(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	out := make([]Schedule, 0, len(a.store.State.Schedules))
	for _, sc := range a.store.State.Schedules {
		out = append(out, *sc)
	}
	a.store.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].NextRun.IsZero() != out[j].NextRun.IsZero() {
			return !out[i].NextRun.IsZero()
		}
		return out[i].NextRun.Before(out[j].NextRun)
	})
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSaveSchedule(w http.ResponseWriter, r *http.Request, id *identity) {
	var req scheduleRequest
	if !readJSON(w, r, &req, 64<<10) {
		return
	}
	req.Name = cleanText(req.Name, 60)
	if req.Name == "" {
		req.Name = cleanText(req.Spec.Title, 60)
	}
	if msg := validateSpec(&req.Spec); msg != "" {
		writeErr(w, http.StatusBadRequest, "invalid", msg)
		return
	}
	req.Once, req.Time = strings.TrimSpace(req.Once), strings.TrimSpace(req.Time)
	now := time.Now()
	probe := &Schedule{Once: req.Once, Days: req.Days, Time: req.Time}
	if req.Once != "" {
		probe.Days, probe.Time = []int{}, ""
		t, err := time.ParseInLocation(onceLayout, req.Once, time.Local)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", "Give the date and time as 2026-10-28T15:50.")
			return
		}
		if !t.After(now) {
			writeErr(w, http.StatusBadRequest, "invalid", "That time has already passed.")
			return
		}
	} else {
		if _, err := time.Parse("15:04", req.Time); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", "Give the time as 24-hour HH:MM, e.g. 15:50.")
			return
		}
		seen := map[int]bool{}
		days := []int{}
		for _, d := range req.Days {
			if d < 0 || d > 6 {
				writeErr(w, http.StatusBadRequest, "invalid", "Days run from 0 (Sunday) to 6 (Saturday).")
				return
			}
			if !seen[d] {
				seen[d] = true
				days = append(days, d)
			}
		}
		if len(days) == 0 {
			writeErr(w, http.StatusBadRequest, "invalid", "Choose at least one day, or a single date and time.")
			return
		}
		sort.Ints(days)
		probe.Days = days
	}

	sid := r.PathValue("id")
	st := a.store
	st.mu.Lock()
	defer st.mu.Unlock()
	var sc *Schedule
	if sid != "" {
		if sc = a.scheduleByIDLocked(sid); sc == nil {
			writeErr(w, http.StatusNotFound, "not_found", "That schedule no longer exists.")
			return
		}
		if id.User.Role != "admin" && sc.Owner != id.User.ID {
			writeErr(w, http.StatusForbidden, "forbidden", "You can only change schedules you made.")
			return
		}
	}
	// Check against whoever the schedule will send as.
	if msg := st.checkAllowed(&id.User, req.Spec.Target); msg != "" {
		writeErr(w, http.StatusForbidden, "forbidden", msg)
		return
	}
	for _, gid := range req.Spec.Target.GroupIDs {
		if st.groupByID(gid) == nil {
			writeErr(w, http.StatusBadRequest, "invalid", "One of the chosen groups no longer exists. Reload the page.")
			return
		}
	}
	if req.Spec.SoundID != "" && st.soundByID(req.Spec.SoundID) == nil {
		writeErr(w, http.StatusBadRequest, "invalid", "That sound has been deleted. Choose another.")
		return
	}
	if sc == nil {
		sc = &Schedule{ID: newID(), Owner: id.User.ID, CreatedAt: now, Enabled: true}
		st.State.Schedules = append(st.State.Schedules, sc)
	}
	sc.Name, sc.Spec, sc.Once, sc.Days, sc.Time = req.Name, req.Spec, probe.Once, probe.Days, probe.Time
	if req.Enabled != nil {
		sc.Enabled = *req.Enabled
	}
	sc.LastError = ""
	sc.NextRun = time.Time{}
	if sc.Enabled {
		sc.NextRun = nextRun(sc, now)
	}
	st.dirtyState = true
	out := *sc
	log.Printf("schedule %q saved by %s, next run %v", sc.Name, id.User.Username, sc.NextRun)
	go a.save()
	writeJSON(w, http.StatusOK, out)
}

func (a *App) scheduleByIDLocked(id string) *Schedule {
	for _, sc := range a.store.State.Schedules {
		if sc.ID == id {
			return sc
		}
	}
	return nil
}

func (a *App) handleDeleteSchedule(w http.ResponseWriter, r *http.Request, id *identity) {
	sid := r.PathValue("id")
	a.store.mu.Lock()
	sc := a.scheduleByIDLocked(sid)
	if sc != nil && id.User.Role != "admin" && sc.Owner != id.User.ID {
		a.store.mu.Unlock()
		writeErr(w, http.StatusForbidden, "forbidden", "You can only delete schedules you made.")
		return
	}
	keep := a.store.State.Schedules[:0]
	for _, x := range a.store.State.Schedules {
		if x.ID != sid {
			keep = append(keep, x)
		}
	}
	a.store.State.Schedules = keep
	a.store.dirtyState = true
	a.store.mu.Unlock()
	a.save()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// scheduleOwner finds the account a schedule sends as. Caller holds the store lock.
func (a *App) scheduleOwner(sc *Schedule) (*User, string) {
	if tid, ok := strings.CutPrefix(sc.Owner, "token:"); ok {
		for _, t := range a.store.State.Tokens {
			if t.ID == tid {
				return &User{ID: sc.Owner, Username: "token:" + t.Name, DisplayName: t.Name, Role: "sender"}, ""
			}
		}
		return nil, "The API token that made this schedule has been deleted."
	}
	u := a.store.userByID(sc.Owner)
	if u == nil {
		return nil, "The account that made this schedule has been deleted."
	}
	if u.Disabled {
		return nil, "The account that made this schedule is disabled."
	}
	cp := *u
	return &cp, ""
}

// runSchedule sends one schedule's alert now, as its owner.
func (a *App) runSchedule(sid string, now time.Time, manual bool) (alertView, *sendFailure) {
	a.store.mu.Lock()
	sc := a.scheduleByIDLocked(sid)
	if sc == nil {
		a.store.mu.Unlock()
		return alertView{}, &sendFailure{http.StatusNotFound, "not_found", "That schedule no longer exists."}
	}
	owner, problem := a.scheduleOwner(sc)
	spec := sc.Spec
	name := sc.Name
	a.store.mu.Unlock()

	var v alertView
	var fail *sendFailure
	if problem != "" {
		fail = &sendFailure{http.StatusConflict, "owner_gone", problem}
	} else {
		v, _, fail = a.createAlert(spec, owner)
	}

	a.store.mu.Lock()
	if sc = a.scheduleByIDLocked(sid); sc != nil {
		sc.LastRun = now
		if fail != nil {
			sc.LastError = fail.msg
		} else {
			sc.LastError, sc.LastAlert = "", v.ID
		}
		if !manual {
			sc.NextRun = nextRun(sc, now)
			if sc.Once != "" {
				sc.Enabled = false
			}
		}
		a.store.dirtyState = true
	}
	a.store.mu.Unlock()
	if fail != nil {
		log.Printf("schedule %q could not send: %s", name, fail.msg)
	}
	return v, fail
}

func (a *App) handleRunSchedule(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	sc := a.scheduleByIDLocked(r.PathValue("id"))
	mine := sc != nil && (id.User.Role == "admin" || sc.Owner == id.User.ID)
	a.store.mu.Unlock()
	if sc != nil && !mine {
		writeErr(w, http.StatusForbidden, "forbidden", "You can only run schedules you made.")
		return
	}
	v, fail := a.runSchedule(r.PathValue("id"), time.Now(), true)
	if fail != nil {
		writeErr(w, fail.status, fail.code, fail.msg)
		return
	}
	a.save()
	writeJSON(w, http.StatusCreated, map[string]any{"alert": v})
}

// scheduler fires due schedules until ctx ends.
func (a *App) scheduler(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		var due []string
		a.store.mu.Lock()
		for _, sc := range a.store.State.Schedules {
			if !sc.Enabled || sc.NextRun.IsZero() || sc.NextRun.After(now) {
				continue
			}
			if now.Sub(sc.NextRun) > missedGrace {
				sc.LastError = "Skipped the " + sc.NextRun.Format("Mon 2 Jan 15:04") + " run: the server was not running then."
				sc.NextRun = nextRun(sc, now)
				if sc.Once != "" {
					sc.Enabled = false
				}
				a.store.dirtyState = true
				continue
			}
			due = append(due, sc.ID)
		}
		a.store.mu.Unlock()
		for _, sid := range due {
			a.runSchedule(sid, now, false)
		}
		if len(due) > 0 {
			a.save()
		}
	}
}
