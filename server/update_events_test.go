package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestUpdateWebhookEvents goes through a release from announcement to the
// last PC installing it, and checks webhooks hear about each step once.
func TestUpdateWebhookEvents(t *testing.T) {
	files := map[string][]byte{msiAsset: []byte("pretend MSI")}
	files[sumsAsset] = sumsFor(files)
	gh := fakeGitHub(t, "9.0.0", files)
	defer gh.Close()
	oldAPI, oldHTTP := githubAPI, updateHTTP.Transport
	githubAPI, updateHTTP.Transport = gh.URL, gh.Client().Transport
	defer func() { githubAPI, updateHTTP.Transport = oldAPI, oldHTTP }()

	got := make(chan webhookPayload, 50)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p webhookPayload
		json.NewDecoder(r.Body).Decode(&p)
		got <- p
	}))
	defer hook.Close()

	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: st, hub: newHub(), sessions: newSessionStore(), throttle: newThrottle(), hooks: newWebhookSender(), idem: newIdemCache()}
	if err := app.bootstrap(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	app.runWebhooks(stop)

	st.mu.Lock()
	admin := st.userByName("admin")
	admin.MustChange = false
	key := st.State.Settings.ClientKey
	st.State.Webhooks = append(st.State.Webhooks, &Webhook{ID: "w1", Name: "all", URL: hook.URL, Secret: "s", Enabled: true})
	st.mu.Unlock()
	cookie := app.sessions.create(admin.ID)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	call := func(method, path string, body any) int {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rd)
		req.Header.Set("X-Foghorn", "1")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	updater := func(host, ver, state, detail string) {
		b, _ := json.Marshal(updaterHello{Hostname: host, Version: ver, State: state, Detail: detail})
		req, _ := http.NewRequest("POST", srv.URL+"/api/client/updater", bytes.NewReader(b))
		req.Header.Set("X-Foghorn-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	// expect waits for exactly these events (in any order) and nothing else.
	expect := func(step string, want ...string) map[string]map[string]any {
		t.Helper()
		seen := map[string]map[string]any{}
		var names []string
		deadline := time.After(5 * time.Second)
		for len(names) < len(want) {
			select {
			case p := <-got:
				names = append(names, p.Event)
				d, _ := json.Marshal(p.Data)
				var m map[string]any
				json.Unmarshal(d, &m)
				seen[p.Event] = m
			case <-deadline:
				t.Fatalf("%s: got %v, want %v", step, names, want)
			}
		}
		select {
		case p := <-got:
			t.Fatalf("%s: unexpected extra event %s", step, p.Event)
		case <-time.After(200 * time.Millisecond):
		}
		sort.Strings(names)
		sort.Strings(want)
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v, want %v", step, names, want)
		}
		return seen
	}

	// Two PCs on the old version.
	updater("IT-PC01", "1.2.0", "idle", "")
	updater("IT-PC02", "1.2.0", "idle", "")
	expect("PCs checking in")

	// A new release is announced once, however often anyone checks.
	call("POST", "/api/updates/check", nil)
	ev := expect("first check", "update.available")
	if ev["update.available"]["version"] != "9.0.0" || ev["update.available"]["current_version"] != version {
		t.Fatalf("update.available: %v", ev["update.available"])
	}
	call("POST", "/api/updates/check", nil)
	expect("second check")

	call("PUT", "/api/updates/rollout", map[string]any{"version": "9.0.0", "target": map[string]any{"all": true}})
	ev = expect("start", "update.rollout_started")
	if ev["update.rollout_started"]["by"] != "admin" {
		t.Fatalf("rollout_started: %v", ev["update.rollout_started"])
	}
	call("PUT", "/api/updates/rollout", map[string]any{"version": "9.0.0", "target": map[string]any{"all": true}, "paused": true})
	expect("pause", "update.rollout_paused")
	call("PUT", "/api/updates/rollout", map[string]any{"version": "9.0.0", "target": map[string]any{"all": true}})
	expect("resume", "update.rollout_resumed")

	// One PC fails (announced once, not on every check-in), then both update.
	updater("IT-PC02", "1.2.0", "failed", "msiexec returned 1603")
	ev = expect("failure", "update.pc_failed")
	if ev["update.pc_failed"]["detail"] != "msiexec returned 1603" || ev["update.pc_failed"]["target_version"] != "9.0.0" {
		t.Fatalf("pc_failed: %v", ev["update.pc_failed"])
	}
	updater("IT-PC02", "1.2.0", "failed", "msiexec returned 1603")
	expect("same failure again")

	updater("IT-PC01", "9.0.0", "idle", "")
	ev = expect("first PC", "update.pc_updated")
	if ev["update.pc_updated"]["from"] != "1.2.0" || ev["update.pc_updated"]["to"] != "9.0.0" {
		t.Fatalf("pc_updated: %v", ev["update.pc_updated"])
	}
	updater("IT-PC02", "9.0.0", "idle", "")
	ev = expect("last PC", "update.pc_updated", "update.rollout_complete")
	if ev["update.rollout_complete"]["pcs"] != float64(2) {
		t.Fatalf("rollout_complete: %v", ev["update.rollout_complete"])
	}
	updater("IT-PC02", "9.0.0", "idle", "")
	expect("checking in again")

	call("DELETE", "/api/updates/rollout", nil)
	expect("stop", "update.rollout_stopped")

	// A server starting on a new version says so, naming who updated it.
	st.mu.Lock()
	st.State.Updates.ServerVersion, st.State.Updates.ServerUpdateBy = "1.0.0", "admin"
	st.mu.Unlock()
	app.noteServerVersion()
	ev = expect("server start", "update.server_installed")
	if s := ev["update.server_installed"]; s["from"] != "1.0.0" || s["to"] != version || s["by"] != "admin" {
		t.Fatalf("server_installed: %v", s)
	}
	app.noteServerVersion()
	expect("same version again")
}

func TestAPICallIsOptIn(t *testing.T) {
	everything := &Webhook{}
	if wantsEvent(everything, "api.call") {
		t.Fatal("a webhook with no events ticked must not get api.call")
	}
	if !wantsEvent(everything, "update.available") {
		t.Fatal("a webhook with no events ticked should get update events")
	}
	if !wantsEvent(&Webhook{Events: []string{"api.call"}}, "api.call") {
		t.Fatal("ticking api.call should send it")
	}
}

// TestAPICallEvent sends an alert with a token and checks api.call says so,
// without leaking the token or the request body.
func TestAPICallEvent(t *testing.T) {
	got := make(chan []byte, 10)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	defer hook.Close()
	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: st, hub: newHub(), sessions: newSessionStore(), throttle: newThrottle(), hooks: newWebhookSender(), idem: newIdemCache()}
	if err := app.bootstrap(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	app.runWebhooks(stop)
	tok := "fh_test_secret_token"
	st.mu.Lock()
	st.State.Tokens = append(st.State.Tokens, &Token{ID: "t1", Name: "Website", Hash: hashToken(tok), Scopes: []string{"send", "read"}})
	st.State.Webhooks = append(st.State.Webhooks, &Webhook{ID: "w1", Name: "audit", URL: hook.URL, Secret: "s", Enabled: true, Events: []string{"api.call"}})
	st.mu.Unlock()
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	send := func(method, path, body string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	send("GET", "/api/alerts", "") // reads are not reported
	send("POST", "/api/alerts", `{"title":"Secret plans","target":{"all":true}}`)

	select {
	case b := <-got:
		var p webhookPayload
		json.Unmarshal(b, &p)
		d, _ := p.Data.(map[string]any)
		if p.Event != "api.call" || d["token"] != "Website" || d["method"] != "POST" || d["path"] != "/api/alerts" || d["status"] != float64(201) {
			t.Fatalf("api.call: %s", b)
		}
		if strings.Contains(string(b), tok) || strings.Contains(string(b), "Secret plans") {
			t.Fatalf("api.call must not carry the token or the request body: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no api.call event")
	}
	select {
	case b := <-got:
		t.Fatalf("only one event expected (the GET must not be reported), also got %s", b)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestDiscordUpdateEvents(t *testing.T) {
	cases := map[string]struct {
		data  map[string]any
		title string
	}{
		"update.available":        {map[string]any{"version": "1.3.0", "current_version": "1.2.1", "url": "https://github.com/x", "notes": "New things"}, "Foghorn 1.3.0 is available"},
		"update.server_installed": {map[string]any{"from": "1.2.1", "to": "1.3.0", "by": "admin"}, "Server updated to 1.3.0"},
		"update.rollout_started":  {map[string]any{"version": "1.3.0", "target_summary": "Every computer", "by": "admin"}, "Rollout of 1.3.0 started"},
		"update.rollout_paused":   {map[string]any{"version": "1.3.0"}, "Rollout of 1.3.0 paused"},
		"update.rollout_complete": {map[string]any{"version": "1.3.0", "target_summary": "Every computer", "pcs": 12}, "Rollout of 1.3.0 complete"},
		"update.pc_updated":       {map[string]any{"hostname": "SQLPC", "from": "1.2.1", "to": "1.3.0"}, "SQLPC updated to 1.3.0"},
		"update.pc_failed":        {map[string]any{"hostname": "SQLPC", "version": "1.2.1", "target_version": "1.3.0", "detail": "1603"}, "SQLPC could not update"},
		"api.call":                {map[string]any{"token": "Website", "method": "POST", "path": "/api/alerts", "status": 201}, "API: POST /api/alerts"},
	}
	for ev, c := range cases {
		m := discordFor(t, ev, c.data)
		if m.Embeds[0].Title != c.title {
			t.Errorf("%s: title %q, want %q", ev, m.Embeds[0].Title, c.title)
		}
	}
}
