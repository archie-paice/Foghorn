package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLockLifecycle walks one lock through apply, restart, release and unlock.
func TestLockLifecycle(t *testing.T) {
	now := time.Now()
	s := &Store{Clients: map[string]*ClientInfo{}}
	l := &Lock{ID: "l1", Spec: LockSpec{Mode: "freeze", Message: "Eyes front"}, Rules: []Rule{{"hostname", "LAB1-*"}},
		CreatedAt: now, EndsAt: now.Add(30 * time.Minute), States: map[string]*LockState{}}
	s.Locks = []*Lock{l}

	other := s.touchClient(clientHello{Hostname: "LAB2-PC01", User: "zed", SID: "z"}, "10.0.0.9", now)
	if got, _ := s.pendingLocksFor(other, now); len(got) != 0 {
		t.Fatalf("LAB2 must not be locked, got %+v", got)
	}

	c := s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s1"}, "10.0.0.1", now)
	got, _ := s.pendingLocksFor(c, now)
	if len(got) != 1 || got[0].Mode != "freeze" || got[0].SecondsLeft < 1790 {
		t.Fatalf("first poll should lock with ~30 min left, got %+v", got)
	}
	if got, _ = s.pendingLocksFor(c, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("must not resend immediately, got %+v", got)
	}
	s.applyEvents(c, []clientEvent{{LockID: "l1", Event: "locked"}}, now)
	if l.States[c.ID].LockedAt == nil {
		t.Fatal("locked event not recorded")
	}
	if got, _ = s.pendingLocksFor(c, now.Add(5*time.Minute)); len(got) != 0 {
		t.Fatalf("an applied lock must not be resent to the same session, got %+v", got)
	}
	if v := viewLock(l, now, false); v.Locked != 1 {
		t.Fatalf("view should count one locked computer, got %+v", v)
	}

	// The student ends the client in Task Manager; it starts again at logon.
	c = s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s2"}, "10.0.0.1", now)
	if got, _ = s.pendingLocksFor(c, now.Add(time.Minute)); len(got) != 1 {
		t.Fatalf("a restarted client must be locked again, got %+v", got)
	}
	// A stale event from the old process does not count for the new one.
	s.applyEvents(c, []clientEvent{{LockID: "l1", Event: "locked"}}, now)
	s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s1"}, "10.0.0.1", now)

	c = s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s2"}, "10.0.0.1", now)
	s.unlockLocked(l, "teacher", now.Add(2*time.Minute))
	_, un := s.pendingLocksFor(c, now.Add(2*time.Minute))
	if len(un) != 1 || un[0] != "l1" {
		t.Fatalf("unlock should be delivered, got %+v", un)
	}
	if _, un = s.pendingLocksFor(c, now.Add(3*time.Minute)); len(un) != 0 {
		t.Fatalf("unlock should be delivered once, got %+v", un)
	}
	if !s.applyLockEvent(c, clientEvent{LockID: "l1", Event: "released"}, now) {
		t.Fatal("released not recorded")
	}
	if s.unlockLocked(l, "teacher", now.Add(4*time.Minute)) {
		t.Fatal("unlocking twice should be a no-op")
	}
}

func TestResponsesAndLook(t *testing.T) {
	spec := AlertSpec{Title: "Are you safe?", Responses: []string{" I'm safe ", "Need help", ""}, DisplaySeconds: 30,
		Accent: "#AA0000", Icon: "alarm", Target: Target{All: true}}
	if msg := validateSpec(&spec); msg != "" {
		t.Fatal(msg)
	}
	if !spec.RequireAck || spec.DisplaySeconds != 0 || len(spec.Responses) != 2 || spec.Accent != "#aa0000" {
		t.Fatalf("replies should force acknowledgement and be tidied: %+v", spec)
	}
	for _, bad := range []AlertSpec{
		{Title: "x", Accent: "red", Target: Target{All: true}},
		{Title: "x", Icon: "skull", Target: Target{All: true}},
		{Title: "x", Responses: []string{"a", "b", "c", "d", "e"}, Target: Target{All: true}},
		{Title: "x", Responses: []string{"Yes", "yes"}, Target: Target{All: true}},
	} {
		b := bad
		if validateSpec(&b) == "" {
			t.Errorf("should reject %+v", bad)
		}
	}

	now := time.Now()
	s := &Store{Clients: map[string]*ClientInfo{}}
	a := &Alert{ID: "q", Spec: spec, CreatedAt: now, ExpiresAt: now.Add(time.Hour), Deliveries: map[string]*Delivery{}}
	s.Alerts = []*Alert{a}
	c := s.touchClient(clientHello{Hostname: "PC1", User: "amy", SID: "1"}, "10.0.0.1", now)
	got, _ := s.pendingFor(c, now)
	if len(got) != 1 || got[0].Icon != alertIcons["alarm"] || len(got[0].Responses) != 2 {
		t.Fatalf("wire alert missing builder fields: %+v", got)
	}
	_, hooks := s.applyEvents(c, []clientEvent{{AlertID: "q", Event: "acknowledged", Choice: "Need help"}}, now)
	if len(hooks) != 1 || hooks[0].name != "alert.responded" || hooks[0].data["response"] != "Need help" {
		t.Fatalf("expected an alert.responded webhook event, got %+v", hooks)
	}
	if tally := a.tally(); tally["Need help"] != 1 || tally["I'm safe"] != 0 {
		t.Fatalf("tally wrong: %+v", tally)
	}
	// A reply the alert never offered is not recorded.
	c2 := s.touchClient(clientHello{Hostname: "PC2", User: "bob", SID: "2"}, "10.0.0.2", now)
	s.pendingFor(c2, now)
	s.applyEvents(c2, []clientEvent{{AlertID: "q", Event: "acknowledged", Choice: "<script>"}}, now)
	if r := a.Deliveries[c2.ID].Response; r != "" {
		t.Fatalf("made-up reply recorded: %q", r)
	}
}

func TestNextRun(t *testing.T) {
	// Wednesday 28 October 2026, 10:00 local.
	wed := time.Date(2026, 10, 28, 10, 0, 0, 0, time.Local)
	weekdays := &Schedule{Days: []int{1, 2, 3, 4, 5}, Time: "15:50"}
	if got := nextRun(weekdays, wed); !got.Equal(time.Date(2026, 10, 28, 15, 50, 0, 0, time.Local)) {
		t.Errorf("later today: got %v", got)
	}
	fri := time.Date(2026, 10, 30, 16, 0, 0, 0, time.Local)
	if got := nextRun(weekdays, fri); !got.Equal(time.Date(2026, 11, 2, 15, 50, 0, 0, time.Local)) {
		t.Errorf("Friday evening should roll to Monday: got %v", got)
	}
	once := &Schedule{Once: "2026-10-28T12:30"}
	if got := nextRun(once, wed); got.Hour() != 12 || got.Minute() != 30 {
		t.Errorf("one-off: got %v", got)
	}
	if got := nextRun(once, wed.Add(5*time.Hour)); !got.IsZero() {
		t.Errorf("a past one-off has no next run: got %v", got)
	}
	if got := nextRun(&Schedule{Time: "15:50"}, wed); !got.IsZero() {
		t.Errorf("no days means never: got %v", got)
	}
}

func TestSoundKind(t *testing.T) {
	cases := map[string]string{
		"RIFF\x00\x00\x00\x00WAVEfmt ": ".wav",
		"ID3\x04\x00":                  ".mp3",
		"\xff\xfb\x90\x00":             ".mp3",
		"<html>":                       "",
		"MZ\x90\x00":                   "",
	}
	for in, want := range cases {
		if got := soundKind([]byte(in)); got != want {
			t.Errorf("soundKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWebhookSignature(t *testing.T) {
	// The same calculation a receiver makes: hex HMAC-SHA256 of "<ts>.<body>".
	// The expected value was computed independently with Python's hmac module;
	// PHP's hash_hmac('sha256', "$ts.$body", $secret) gives the same.
	got := signWebhook("whsec_test", "1700000000", []byte(`{"event":"ping"}`))
	if want := "sha256=aa8efe37b751e71157c508c5ac4acb1e9fe5225db98355dfc00f4b680afbc447"; got != want {
		t.Fatalf("signature %s, want %s", got, want)
	}
	if got == signWebhook("whsec_other", "1700000000", []byte(`{"event":"ping"}`)) {
		t.Fatal("different secrets must give different signatures")
	}
	if got == signWebhook("whsec_test", "1700000001", []byte(`{"event":"ping"}`)) {
		t.Fatal("the timestamp must be part of the signature")
	}
}

func TestTokenScopes(t *testing.T) {
	old := &Token{}
	if s := tokenScopes(old); !hasScope(s, "send") || !hasScope(s, "read") || hasScope(s, "lock") {
		t.Fatalf("tokens from before 1.1 should keep send+read only, got %v", s)
	}
	tok := &identity{ViaToken: true, Scopes: []string{"read"}}
	if tok.can("send") || !tok.can("read") || !tok.can("") {
		t.Fatal("read-only token scope check wrong")
	}
	sender := &identity{User: User{Role: "sender"}}
	if sender.can("lock") || !sender.can("send") {
		t.Fatal("senders may not lock unless allowed")
	}
	sender.User.CanLock = true
	if !sender.can("lock") {
		t.Fatal("a sender allowed to lock should be able to")
	}
}

// TestHTTPAPI drives the real routes the way an integration (a Laravel site,
// a script) would: with a bearer token and JSON.
func TestHTTPAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: Config{Listen: ":0"}, store: st, hub: newHub(), sessions: newSessionStore(),
		throttle: newThrottle(), hooks: newWebhookSender(), idem: newIdemCache()}
	if err := app.bootstrap(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	app.runWebhooks(stop)

	// a webhook receiver
	received := make(chan *http.Request, 10)
	bodies := make(chan []byte, 10)
	hookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- r
		bodies <- b
	}))
	defer hookSrv.Close()

	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	sendTok, lockTok := "fh_send_test", "fh_lock_test"
	st.mu.Lock()
	st.State.Tokens = append(st.State.Tokens,
		&Token{ID: "t1", Name: "Website", Hash: hashToken(sendTok), Scopes: []string{"read", "send"}},
		&Token{ID: "t2", Name: "Lock bot", Hash: hashToken(lockTok), Scopes: []string{"lock"}})
	st.State.Webhooks = append(st.State.Webhooks, &Webhook{ID: "w1", Name: "site", URL: hookSrv.URL, Secret: "whsec_x", Enabled: true,
		Events: []string{"alert.sent"}})
	st.mu.Unlock()

	call := func(tok, method, path string, body any, hdr map[string]string) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	alert := map[string]any{"title": "Parents' evening tonight", "target": map[string]any{"all": true},
		"responses": []string{"Coming", "Can't make it"}, "icon": "calendar", "accent": "#336699"}
	code, out := call(sendTok, "POST", "/api/alerts", alert, map[string]string{"Idempotency-Key": "order-42"})
	if code != 201 {
		t.Fatalf("send: %d %v", code, out)
	}
	first := out["alert"].(map[string]any)["id"]
	code, out = call(sendTok, "POST", "/api/alerts", alert, map[string]string{"Idempotency-Key": "order-42"})
	if code != 200 || out["replayed"] != true || out["alert"].(map[string]any)["id"] != first {
		t.Fatalf("retry with the same Idempotency-Key must not send twice: %d %v", code, out)
	}
	st.mu.Lock()
	n := len(st.Alerts)
	st.mu.Unlock()
	if n != 1 {
		t.Fatalf("want 1 alert stored, have %d", n)
	}

	select {
	case r := <-received:
		b := <-bodies
		if r.Header.Get("X-Foghorn-Event") != "alert.sent" {
			t.Fatalf("wrong event header %q", r.Header.Get("X-Foghorn-Event"))
		}
		if want := signWebhook("whsec_x", r.Header.Get("X-Foghorn-Timestamp"), b); r.Header.Get("X-Foghorn-Signature") != want {
			t.Fatal("webhook signature does not verify")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook delivery")
	}

	if code, _ = call(sendTok, "POST", "/api/locks", map[string]any{"target": map[string]any{"all": true}}, nil); code != 403 {
		t.Fatalf("a send+read token must not lock screens, got %d", code)
	}
	if code, _ = call(lockTok, "GET", "/api/alerts", nil, nil); code != 403 {
		t.Fatalf("a lock-only token must not read history, got %d", code)
	}
	code, out = call(lockTok, "POST", "/api/locks", map[string]any{"mode": "blank", "duration_minutes": 5, "target": map[string]any{"all": true}}, nil)
	if code != 201 {
		t.Fatalf("lock: %d %v", code, out)
	}
	lid := out["lock"].(map[string]any)["id"].(string)
	if code, out = call(lockTok, "POST", "/api/locks/"+lid+"/unlock", nil, nil); code != 200 || out["active"] != false {
		t.Fatalf("unlock: %d %v", code, out)
	}
	if code, _ = call(sendTok, "PUT", "/api/settings", map[string]any{"org_name": "x"}, nil); code != 403 {
		t.Fatalf("tokens must never reach admin routes, got %d", code)
	}

	// schedules
	code, out = call(sendTok, "POST", "/api/schedules", map[string]any{"name": "Home time", "days": []int{1, 2, 3, 4, 5}, "time": "15:50",
		"spec": map[string]any{"title": "Save your work", "target": map[string]any{"all": true}}}, nil)
	if code != 200 || out["next_run"] == nil || out["enabled"] != true {
		t.Fatalf("schedule: %d %v", code, out)
	}
	sid := out["id"].(string)
	if code, out = call(sendTok, "POST", "/api/schedules/"+sid+"/run", nil, nil); code != 201 {
		t.Fatalf("run schedule: %d %v", code, out)
	}

	// a client polling sees the alert with its builder fields
	st.mu.Lock()
	key := st.State.Settings.ClientKey
	st.mu.Unlock()
	pb, _ := json.Marshal(pollRequest{Client: clientHello{Hostname: "PC1", User: "amy", SID: "x"}})
	req, _ := http.NewRequest("POST", srv.URL+"/api/client/poll", bytes.NewReader(pb))
	req.Header.Set("X-Foghorn-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var poll pollResponse
	json.NewDecoder(resp.Body).Decode(&poll)
	resp.Body.Close()
	if len(poll.Alerts) != 2 || poll.Alerts[0].Accent != "#336699" || len(poll.Alerts[0].Responses) != 2 {
		t.Fatalf("poll: %+v", poll)
	}

	// sounds: upload as an admin session would, then fetch as a client
	wav := append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 64)...)
	st.mu.Lock()
	admin := st.userByName("admin")
	admin.MustChange = false
	st.mu.Unlock()
	cookie := app.sessions.create(admin.ID)
	up, _ := http.NewRequest("POST", srv.URL+"/api/sounds?name=Bell", bytes.NewReader(wav))
	up.Header.Set("X-Foghorn", "1")
	up.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	resp, err = http.DefaultClient.Do(up)
	if err != nil {
		t.Fatal(err)
	}
	var snd Sound
	json.NewDecoder(resp.Body).Decode(&snd)
	resp.Body.Close()
	if resp.StatusCode != 201 || snd.Ext != ".wav" {
		t.Fatalf("upload: %d %+v", resp.StatusCode, snd)
	}
	if _, err := os.Stat(filepath.Join(dir, "sounds", snd.ID+".wav")); err != nil {
		t.Fatalf("sound file not written: %v", err)
	}
	get, _ := http.NewRequest("GET", srv.URL+"/api/client/sounds/"+snd.ID, nil)
	get.Header.Set("X-Foghorn-Key", key)
	resp, err = http.DefaultClient.Do(get)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, wav) {
		t.Fatalf("client sound download: %d, %d bytes", resp.StatusCode, len(got))
	}

	// the API description is valid JSON
	resp, err = http.Get(srv.URL + "/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&spec); err != nil || spec["openapi"] == nil {
		t.Fatalf("openapi.json: %v", err)
	}
	resp.Body.Close()

	// and everything survives a restart
	if err := app.store.flush(); err != nil {
		t.Fatal(err)
	}
	again, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Locks) != 1 || len(again.State.Schedules) != 1 || len(again.State.Sounds) != 1 {
		t.Fatalf("reload lost data: locks=%d schedules=%d sounds=%d", len(again.Locks), len(again.State.Schedules), len(again.State.Sounds))
	}
}
