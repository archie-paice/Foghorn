package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"1.1.0", "1.2.0", true}, {"1.2.0", "1.1.0", false}, {"1.9.2", "1.10.0", true},
		{"1.2", "1.2.0", false}, {"1.2.0", "1.2.1", true}, {"2.0.0", "1.99.99", false}, {"1.2.0", "1.2.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%s, %s) = %v", c.a, c.b, got)
		}
	}
	for _, bad := range []string{"", "1", "v1.2.0", "1.2.x", "1..2", "1.2.3.4.5"} {
		if validVersion(bad) {
			t.Errorf("%q should not be a valid version", bad)
		}
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	got := parseSums([]byte(h + "  FoghornClient.msi\r\n" + strings.ToUpper(h) + " *foghorn-server.exe\nnot a line\n"))
	if got["FoghornClient.msi"] != h || got["foghorn-server.exe"] != h || len(got) != 2 {
		t.Fatalf("parseSums: %v", got)
	}
}

// fakeGitHub serves a release the way GitHub does: the API answer, then the
// files it links to.
func fakeGitHub(t *testing.T, ver string, files map[string][]byte) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/"+defaultUpdateRepo+"/releases/latest" {
			var assets []map[string]string
			for name := range files {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + "/dl/" + name})
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v" + ver, "name": "Foghorn " + ver, "body": "Notes",
				"html_url": "https://github.com/x", "published_at": time.Now(), "assets": assets})
			return
		}
		if b, ok := files[strings.TrimPrefix(r.URL.Path, "/dl/")]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	return srv
}

func sumsFor(files map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range files {
		s := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(s[:]), name)
	}
	return []byte(b.String())
}

func TestRolloutFlow(t *testing.T) {
	msi := []byte("pretend MSI")
	files := map[string][]byte{msiAsset: msi}
	files[sumsAsset] = sumsFor(files)
	gh := fakeGitHub(t, "9.0.0", files)
	defer gh.Close()
	oldAPI, oldHTTP := githubAPI, updateHTTP.Transport
	githubAPI, updateHTTP.Transport = gh.URL, gh.Client().Transport
	defer func() { githubAPI, updateHTTP.Transport = oldAPI, oldHTTP }()

	st, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &App{store: st, hub: newHub(), sessions: newSessionStore(), throttle: newThrottle(), hooks: newWebhookSender(), idem: newIdemCache()}
	if err := app.bootstrap(); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	admin := st.userByName("admin")
	admin.MustChange = false
	key := st.State.Settings.ClientKey
	st.State.Groups = append(st.State.Groups, &Group{ID: "g1", Name: "IT test PCs", Rules: []Rule{{"hostname", "IT-*"}}})
	st.mu.Unlock()
	cookie := app.sessions.create(admin.ID)
	srv := httptest.NewServer(app.routes())
	defer srv.Close()

	adminCall := func(method, path string, body any) (int, map[string]any) {
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
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	updater := func(host, ver string) map[string]any {
		b, _ := json.Marshal(updaterHello{Hostname: host, Version: ver, State: "idle"})
		req, _ := http.NewRequest("POST", srv.URL+"/api/client/updater", bytes.NewReader(b))
		req.Header.Set("X-Foghorn-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return out
	}

	if out := updater("IT-PC01", "1.2.0"); out["update"] != nil {
		t.Fatalf("no rollout yet, but got %v", out)
	}
	code, out := adminCall("POST", "/api/updates/check", nil)
	if code != 200 || out["latest"].(map[string]any)["version"] != "9.0.0" {
		t.Fatalf("check: %d %v", code, out)
	}
	if code, _ := adminCall("PUT", "/api/updates/rollout", map[string]any{"version": "8.0.0", "target": map[string]any{"all": true}}); code != 400 {
		t.Fatalf("only the latest checked release may be rolled out, got %d", code)
	}
	code, out = adminCall("PUT", "/api/updates/rollout", map[string]any{"version": "9.0.0", "target": map[string]any{"group_ids": []string{"g1"}}})
	if code != 200 || out["msi_sha256"] == "" {
		t.Fatalf("rollout: %d %v", code, out)
	}

	got := updater("IT-PC01", "1.2.0")
	upd, _ := got["update"].(map[string]any)
	s := sha256.Sum256(msi)
	if upd == nil || upd["version"] != "9.0.0" || upd["sha256"] != hex.EncodeToString(s[:]) {
		t.Fatalf("a PC in the test group should be told to update: %v", got)
	}
	if got := updater("LAB1-PC01", "1.2.0"); got["update"] != nil {
		t.Fatalf("a PC outside the rollout must not update: %v", got)
	}
	if got := updater("IT-PC02", "9.0.0"); got["update"] != nil {
		t.Fatalf("a PC already on the version must not update: %v", got)
	}
	if got := updater("IT-PC03", "9.1.0"); got["update"] != nil {
		t.Fatalf("a rollout must never downgrade: %v", got)
	}

	// The server keeps its own copy of the MSI for PCs without GitHub access.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(app.cachedMSIPath("9.0.0")); err == nil && bytes.Equal(b, msi) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the MSI was never cached")
		}
		time.Sleep(50 * time.Millisecond)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/client/updater/msi?version=9.0.0", nil)
	req.Header.Set("X-Foghorn-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(body, msi) {
		t.Fatalf("server copy of the MSI: %d bytes", len(body))
	}

	code, out = adminCall("GET", "/api/updates", nil)
	prog := out["progress"].(map[string]any)
	if code != 200 || prog["target"].(float64) != 3 || prog["done"].(float64) != 2 || prog["pending"].(float64) != 1 {
		t.Fatalf("progress: %v", prog)
	}

	adminCall("PUT", "/api/updates/rollout", map[string]any{"version": "9.0.0", "target": map[string]any{"all": true}, "paused": true})
	if got := updater("IT-PC01", "1.2.0"); got["update"] != nil {
		t.Fatalf("a paused rollout must not update anything: %v", got)
	}
	if code, _ := adminCall("DELETE", "/api/updates/rollout", nil); code != 200 {
		t.Fatal("stop rollout")
	}

	// A release whose file does not match its checksum is refused.
	bad := &Release{Tag: "v9.0.0", Assets: map[string]string{msiAsset: gh.URL + "/dl/" + msiAsset}, Sums: map[string]string{msiAsset: strings.Repeat("0", 64)}}
	if _, err := downloadAsset(context.Background(), bad, msiAsset); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a checksum mismatch must be refused, got %v", err)
	}
	if _, err := httpsGet(context.Background(), "http://example.com/x", 10); err == nil {
		t.Fatal("plain HTTP downloads must be refused")
	}
}

// TestInstallServerBinary swaps one real program for another, the way the
// server replaces itself.
func TestInstallServerBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two small programs")
	}
	dir := t.TempDir()
	build := func(name, ver string) string {
		src := filepath.Join(dir, name+".go")
		os.WriteFile(src, []byte(`package main
import "fmt"
func main() { fmt.Println("`+ver+`") }
`), 0o644)
		out := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", out, src)
		cmd.Env = append(os.Environ(), "GO111MODULE=off")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, b)
		}
		return out
	}
	current := build("current", "1.0.0")
	next, _ := os.ReadFile(build("next", "2.0.0"))

	if err := installServerBinary(current, next, "3.0.0"); err == nil || !strings.Contains(err.Error(), "says it is") {
		t.Fatalf("a program reporting the wrong version must be refused, got %v", err)
	}
	if err := installServerBinary(current, []byte("not a program"), "2.0.0"); err == nil {
		t.Fatal("something that will not run must be refused")
	}
	if out, _ := exec.Command(current).Output(); strings.TrimSpace(string(out)) != "1.0.0" {
		t.Fatal("a refused update must leave the old program in place")
	}
	if err := installServerBinary(current, next, "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command(current).Output(); strings.TrimSpace(string(out)) != "2.0.0" {
		t.Fatalf("after the update the program should be 2.0.0, got %q", out)
	}
	if out, _ := exec.Command(current + ".old").Output(); strings.TrimSpace(string(out)) != "1.0.0" {
		t.Fatal("the previous program should be kept as .old for rollback")
	}
}

func TestSelfUpdateOffInContainer(t *testing.T) {
	t.Setenv("FOGHORN_IN_CONTAINER", "1")
	a := &App{}
	if _, managed, reason := a.selfUpdate(); reason == "" || managed {
		t.Fatalf("in a container: managed=%v reason=%q, want a reason and not managed", managed, reason)
	}
}
