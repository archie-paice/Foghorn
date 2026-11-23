package main

// updates.go - updating the server and the PCs from the web console.
//
// Updates come only from the project's GitHub releases, fetched over HTTPS.
// That is where trust comes from: nothing an administrator types or uploads
// can become code that runs on the PCs.
//
//   - The server checks for the latest release when an administrator presses
//     "Check for updates". It never calls out on its own.
//   - It can replace its own program with the release's, after checking the
//     download against the release's SHA256SUMS.txt and running it once.
//   - An administrator starts a rollout of a release to some or all PCs. The
//     updater service on each PC (part of the 1.2 MSI, running as SYSTEM) asks
//     the server whether it should update. If so, it downloads that version's
//     MSI itself, from a GitHub URL it builds from its own fixed repository
//     name. The server only ever says which version. A server, or anyone
//     tampering with its traffic, cannot point PCs at a different file.
//   - PCs that cannot reach GitHub may fetch the MSI from this server instead,
//     but the client only does that over HTTPS. Over plain HTTP the file could
//     be swapped on the way.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultUpdateRepo  = "archie-paice/Foghorn"
	msiAsset           = "FoghornClient.msi"
	sumsAsset          = "SHA256SUMS.txt"
	updaterCheckEvery  = 20 * time.Minute
	maxDownloadBytes   = 100 << 20
	machineForgetAfter = 90 * 24 * time.Hour
)

// exitForUpdate is the exit status after the server has replaced its own
// program. The Windows service's recovery settings and systemd's
// Restart=on-failure both start it again, now running the new version.
const exitForUpdate = 3

type Release struct {
	Version     string            `json:"version"` // "1.2.0", from the tag "v1.2.0"
	Tag         string            `json:"tag"`
	Name        string            `json:"name"`
	Notes       string            `json:"notes"`
	URL         string            `json:"url"`
	PublishedAt time.Time         `json:"published_at"`
	Assets      map[string]string `json:"assets"` // file name -> download URL
	Sums        map[string]string `json:"sums"`   // file name -> sha256, from SHA256SUMS.txt
}

// Rollout is the administrator's decision to move PCs to a release.
type Rollout struct {
	Version       string    `json:"version"`
	MsiSHA256     string    `json:"msi_sha256"`
	Target        Target    `json:"target"`
	Rules         []Rule    `json:"rules"`
	TargetSummary string    `json:"target_summary"`
	Paused        bool      `json:"paused"`
	StartedBy     string    `json:"started_by"`
	StartedAt     time.Time `json:"started_at"`
}

type UpdateState struct {
	Latest    *Release  `json:"latest,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	Rollout   *Rollout  `json:"rollout,omitempty"`
}

// Machine is a PC's updater service, as opposed to ClientInfo, which is a
// person's desktop client. There is one per computer, whoever is logged on.
type Machine struct {
	Hostname  string    `json:"hostname"`
	Domain    string    `json:"domain"`
	OU        string    `json:"ou"`
	IP        string    `json:"ip"`
	Version   string    `json:"version"`
	State     string    `json:"state"`  // idle | downloading | installing | failed
	Detail    string    `json:"detail"` // what went wrong, for failed
	LastSeen  time.Time `json:"last_seen"`
	UpdatedAt time.Time `json:"updated_at"` // when its version last changed
}

// ---------- versions ----------

// versionLess compares dotted version numbers: 1.10.0 is newer than 1.9.2.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func validVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil || p == "" || len(p) > 5 {
			return false
		}
	}
	return true
}

// ---------- GitHub ----------

func (a *App) updateRepo() string {
	if a.cfg.UpdateRepo != "" {
		return a.cfg.UpdateRepo
	}
	return defaultUpdateRepo
}

var updateHTTP = &http.Client{
	Timeout: 2 * time.Minute,
	// GitHub redirects downloads to its storage host. Refuse anything that
	// would leave HTTPS on the way.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("refusing a redirect away from HTTPS")
		}
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		return nil
	},
}

func httpsGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("refusing a non-HTTPS address: %s", url)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Foghorn-server/"+version)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered HTTP %d", req.URL.Host, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("the download is larger than expected")
	}
	return b, nil
}

var errNotFound = errors.New("not found")

// githubAPI is a variable so tests can point it at a fake GitHub.
var githubAPI = "https://api.github.com"

// parseSums reads a SHA256SUMS.txt: "<hex>  <file name>" per line.
func parseSums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// fetchLatestRelease asks GitHub for the newest published release.
func (a *App) fetchLatestRelease(ctx context.Context) (*Release, error) {
	b, err := httpsGet(ctx, githubAPI+"/repos/"+a.updateRepo()+"/releases/latest", 1<<20)
	if errors.Is(err, errNotFound) {
		return nil, fmt.Errorf("%s has no published releases yet", a.updateRepo())
	}
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	var gh struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &gh); err != nil {
		return nil, fmt.Errorf("GitHub's answer was not what was expected: %w", err)
	}
	r := &Release{
		Version: strings.TrimPrefix(gh.TagName, "v"), Tag: gh.TagName, Name: gh.Name,
		Notes: cleanTextOpt(gh.Body, 20000, true), URL: gh.HTMLURL, PublishedAt: gh.PublishedAt,
		Assets: map[string]string{}, Sums: map[string]string{},
	}
	if !validVersion(r.Version) {
		return nil, fmt.Errorf("the latest release is tagged %q, which is not a version number like v1.2.0", gh.TagName)
	}
	for _, as := range gh.Assets {
		r.Assets[as.Name] = as.URL
	}
	sumsURL, ok := r.Assets[sumsAsset]
	if !ok {
		return nil, fmt.Errorf("release %s has no %s, so its files cannot be checked", gh.TagName, sumsAsset)
	}
	sb, err := httpsGet(ctx, sumsURL, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", sumsAsset, err)
	}
	r.Sums = parseSums(sb)
	return r, nil
}

// downloadAsset fetches one release file and checks it against SHA256SUMS.txt.
func downloadAsset(ctx context.Context, r *Release, name string) ([]byte, error) {
	url, ok := r.Assets[name]
	want := r.Sums[name]
	if !ok || want == "" {
		return nil, fmt.Errorf("release %s has no %s", r.Tag, name)
	}
	b, err := httpsGet(ctx, url, maxDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("could not download %s: %w", name, err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("%s did not match its checksum (got %s…, expected %s…)", name, got[:12], want[:12])
	}
	return b, nil
}

// ---------- the server updating itself ----------

func serverAssetName() string {
	switch {
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "foghorn-server.exe"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "foghorn-server-linux-amd64"
	}
	return ""
}

// selfUpdate reports whether this server can replace its own program, and if
// not, why not.
func (a *App) selfUpdate() (exe string, managed bool, reason string) {
	// In a container the program is part of the image: a replaced binary would
	// vanish when the container is recreated, so update the image instead.
	if os.Getenv("FOGHORN_IN_CONTAINER") != "" {
		return "", false, "This server runs in a container. Update it by pulling or rebuilding the image " +
			"(for example: git pull, then docker compose up -d --build). Your data is kept."
	}
	if serverAssetName() == "" {
		return "", false, "There is no published build for " + runtime.GOOS + "/" + runtime.GOARCH + "."
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return "", false, "Cannot tell where the server program is: " + err.Error()
	}
	probe := filepath.Join(filepath.Dir(exe), ".foghorn-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		hint := ""
		if runtime.GOOS == "linux" {
			hint = " On Linux, run it from the data folder as described in docs/UPDATING.md."
		}
		return exe, false, "The server cannot write to " + filepath.Dir(exe) + "." + hint
	}
	f.Close()
	os.Remove(probe)
	// Under the Windows service or systemd, exiting brings it straight back.
	managed = isWindowsService() || os.Getenv("INVOCATION_ID") != ""
	return exe, managed, ""
}

// installServerBinary puts new in place of exe, keeping the old one as .old.
func installServerBinary(exe string, data []byte, wantVersion string) error {
	staged := exe + ".new"
	if err := os.WriteFile(staged, data, 0o755); err != nil {
		return fmt.Errorf("could not write the new program next to the old one: %w", err)
	}
	// Run it once: it has to start on this machine and say it is the version
	// we downloaded, before it replaces anything.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, staged, "version").Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != wantVersion {
		os.Remove(staged)
		if err != nil {
			return fmt.Errorf("the new program would not run here: %w", err)
		}
		return fmt.Errorf("the new program says it is %q, not %s", got, wantVersion)
	}
	old := exe + ".old"
	os.Remove(old)
	// Windows lets a running program be renamed, though not deleted.
	if err := os.Rename(exe, old); err != nil {
		os.Remove(staged)
		return fmt.Errorf("could not move the running program aside: %w", err)
	}
	if err := os.Rename(staged, exe); err != nil {
		os.Rename(old, exe)
		return fmt.Errorf("could not put the new program in place: %w", err)
	}
	return nil
}

func (a *App) handleUpdateServer(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	rel := a.store.State.Updates.Latest
	a.store.mu.Unlock()
	if rel == nil || !versionLess(version, rel.Version) {
		writeErr(w, http.StatusConflict, "up_to_date", "There is no newer release to install. Check for updates first.")
		return
	}
	exe, managed, reason := a.selfUpdate()
	if reason != "" {
		writeErr(w, http.StatusConflict, "cannot_self_update", reason)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	data, err := downloadAsset(ctx, rel, serverAssetName())
	if err == nil {
		err = installServerBinary(exe, data, rel.Version)
	}
	if err != nil {
		log.Printf("server update to %s FAILED: %v", rel.Version, err)
		writeErr(w, http.StatusBadGateway, "update_failed", err.Error())
		return
	}
	log.Printf("SERVER UPDATED from %s to %s by %s (previous program kept as %s.old)", version, rel.Version, id.User.Username, filepath.Base(exe))
	if !managed {
		writeJSON(w, http.StatusOK, map[string]any{"restarting": false, "version": rel.Version,
			"message": "Installed. This server is running in a window, so stop it and start it again to run " + rel.Version + "."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"restarting": true, "version": rel.Version})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(time.Second) // let the answer reach the browser
		a.store.mu.Lock()
		a.store.dirtyClients, a.store.dirtyState = true, true
		a.store.mu.Unlock()
		a.save()
		log.Printf("restarting to run %s", rel.Version)
		os.Exit(exitForUpdate)
	}()
}

// ---------- checking for updates ----------

func (a *App) handleCheckUpdates(w http.ResponseWriter, r *http.Request, id *identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rel, err := a.fetchLatestRelease(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "check_failed", err.Error())
		return
	}
	a.store.mu.Lock()
	a.store.State.Updates.Latest = rel
	a.store.State.Updates.CheckedAt = time.Now()
	a.store.dirtyState = true
	a.store.mu.Unlock()
	a.save()
	a.handleUpdateStatus(w, r, id)
}

type versionCount struct {
	Version string `json:"version"`
	PCs     int    `json:"pcs"`
}

func (a *App) handleUpdateStatus(w http.ResponseWriter, r *http.Request, id *identity) {
	exe, managed, reason := a.selfUpdate()
	now := time.Now()
	a.store.mu.Lock()
	u := a.store.State.Updates
	machines := make([]Machine, 0, len(a.store.Machines))
	counts := map[string]int{}
	for _, m := range a.store.Machines {
		cp := *m
		machines = append(machines, cp)
		counts[m.Version]++
	}
	// PCs without the updater (clients before 1.2) only show up as desktop clients.
	legacy := map[string]bool{}
	for _, c := range a.store.Clients {
		if _, ok := a.store.Machines[strings.ToLower(c.Hostname)]; !ok && now.Sub(c.LastSeen) < 14*24*time.Hour {
			if !legacy[strings.ToLower(c.Hostname)] {
				legacy[strings.ToLower(c.Hostname)] = true
				counts[c.Version+" (no updater)"]++
			}
		}
	}
	a.store.mu.Unlock()

	sort.Slice(machines, func(i, j int) bool {
		return strings.ToLower(machines[i].Hostname) < strings.ToLower(machines[j].Hostname)
	})
	vc := []versionCount{}
	for v, n := range counts {
		vc = append(vc, versionCount{v, n})
	}
	sort.Slice(vc, func(i, j int) bool { return vc[i].PCs > vc[j].PCs })

	var target, pending, done, failed int
	if u.Rollout != nil {
		for _, m := range machines {
			if !a.machineInRollout(u.Rollout, &m) {
				continue
			}
			target++
			switch {
			case !versionLess(m.Version, u.Rollout.Version):
				done++
			case m.State == "failed":
				failed++
			default:
				pending++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_version": version, "repo": a.updateRepo(),
		"latest": u.Latest, "checked_at": u.CheckedAt,
		"server_update": map[string]any{"possible": reason == "", "reason": reason, "restarts_itself": managed, "program": exe},
		"rollout":       u.Rollout,
		"progress":      map[string]int{"target": target, "done": done, "pending": pending, "failed": failed},
		"versions":      vc, "machines": machines, "legacy_pcs": len(legacy),
		"https": a.cfg.TLSCert != "" || a.cfg.TrustProxyHeaders,
	})
}

// ---------- rolling out to PCs ----------

func (a *App) machineInRollout(ro *Rollout, m *Machine) bool {
	if ro.Target.All {
		return true
	}
	return rulesMatch(ro.Rules, &ClientInfo{Hostname: m.Hostname, Domain: m.Domain, OU: m.OU, IP: m.IP})
}

func (a *App) handleStartRollout(w http.ResponseWriter, r *http.Request, id *identity) {
	var req struct {
		Version string `json:"version"`
		Target  Target `json:"target"`
		Paused  bool   `json:"paused"`
	}
	if !readJSON(w, r, &req, 16<<10) {
		return
	}
	req.Target.Rules = cleanRules(req.Target.Rules)
	if !req.Target.All && len(req.Target.GroupIDs) == 0 && len(req.Target.Rules) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid", "Choose which computers to update.")
		return
	}
	a.store.mu.Lock()
	rel := a.store.State.Updates.Latest
	if rel == nil || rel.Version != req.Version {
		a.store.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "invalid", "Only the latest release can be rolled out. Check for updates and try again.")
		return
	}
	sum := rel.Sums[msiAsset]
	if sum == "" || rel.Assets[msiAsset] == "" {
		a.store.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "invalid", "Release "+rel.Tag+" has no "+msiAsset+" to install.")
		return
	}
	for _, gid := range req.Target.GroupIDs {
		if a.store.groupByID(gid) == nil {
			a.store.mu.Unlock()
			writeErr(w, http.StatusBadRequest, "invalid", "One of the chosen groups no longer exists. Reload the page.")
			return
		}
	}
	rules, summary := a.store.flattenTarget(req.Target)
	ro := &Rollout{Version: rel.Version, MsiSHA256: sum, Target: req.Target, Rules: rules, TargetSummary: summary,
		Paused: req.Paused, StartedBy: id.User.Username, StartedAt: time.Now()}
	a.store.State.Updates.Rollout = ro
	a.store.dirtyState = true
	relCopy := *rel
	a.store.mu.Unlock()
	a.save()
	log.Printf("ROLLOUT of client %s to [%s] set by %s (paused=%v)", ro.Version, summary, id.User.Username, ro.Paused)
	// Keep a copy of the MSI here too, for PCs that cannot reach GitHub.
	go a.cacheMSI(&relCopy)
	writeJSON(w, http.StatusOK, ro)
}

func (a *App) handleStopRollout(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	a.store.State.Updates.Rollout = nil
	a.store.dirtyState = true
	a.store.mu.Unlock()
	a.save()
	log.Printf("ROLLOUT stopped by %s", id.User.Username)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) cachedMSIPath(ver string) string {
	return filepath.Join(a.store.dir, "updates", "FoghornClient-"+ver+".msi")
}

func (a *App) cacheMSI(rel *Release) {
	path := a.cachedMSIPath(rel.Version)
	if _, err := os.Stat(path); err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	b, err := downloadAsset(ctx, rel, msiAsset)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
			err = writeFileAtomic(path, b)
		}
	}
	if err != nil {
		log.Printf("could not keep a copy of the %s MSI (PCs can still fetch it from GitHub): %v", rel.Version, err)
	}
}

// ---------- the PCs' updater service ----------

type updaterHello struct {
	Hostname string `json:"hostname"`
	Domain   string `json:"domain"`
	OU       string `json:"ou"`
	Version  string `json:"version"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
}

// handleUpdaterCheck is called by each PC's updater service every 20 minutes
// or so. It records the PC and says whether it should update, and to what.
func (a *App) handleUpdaterCheck(w http.ResponseWriter, r *http.Request) {
	if !a.checkClientKey(r) {
		writeErr(w, http.StatusUnauthorized, "bad_client_key", "The client key is missing or wrong.")
		return
	}
	var h updaterHello
	if !readJSON(w, r, &h, 16<<10) {
		return
	}
	h.Hostname = cleanText(h.Hostname, 64)
	h.Version = cleanText(h.Version, 32)
	if h.Hostname == "" || !validVersion(h.Version) {
		writeErr(w, http.StatusBadRequest, "bad_request", "hostname and version are required.")
		return
	}
	switch h.State {
	case "idle", "downloading", "installing", "failed":
	default:
		h.State = "idle"
	}
	now := time.Now()
	a.store.mu.Lock()
	key := strings.ToLower(h.Hostname)
	m := a.store.Machines[key]
	if m == nil {
		m = &Machine{}
		a.store.Machines[key] = m
	}
	if m.Version != h.Version {
		if m.Version != "" {
			log.Printf("PC %s updated from %s to %s", h.Hostname, m.Version, h.Version)
		}
		m.UpdatedAt = now
	}
	m.Hostname, m.Domain, m.OU, m.IP = h.Hostname, cleanText(h.Domain, 64), cleanText(h.OU, 512), a.remoteIP(r)
	m.Version, m.State, m.Detail, m.LastSeen = h.Version, h.State, cleanText(h.Detail, 500), now
	a.store.dirtyMachines = true

	resp := map[string]any{"check_seconds": int(updaterCheckEvery.Seconds()), "update": nil}
	if ro := a.store.State.Updates.Rollout; ro != nil && !ro.Paused && versionLess(h.Version, ro.Version) && a.machineInRollout(ro, m) {
		_, err := os.Stat(a.cachedMSIPath(ro.Version))
		resp["update"] = map[string]any{"version": ro.Version, "sha256": ro.MsiSHA256, "server_copy": err == nil}
	}
	a.store.mu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdaterMSI hands out this server's copy of the MSI being rolled out.
// Clients only use it over HTTPS (see the top of this file).
func (a *App) handleUpdaterMSI(w http.ResponseWriter, r *http.Request) {
	if !a.checkClientKey(r) {
		writeErr(w, http.StatusUnauthorized, "bad_client_key", "The client key is missing or wrong.")
		return
	}
	a.store.mu.Lock()
	ro := a.store.State.Updates.Rollout
	a.store.mu.Unlock()
	if ro == nil || r.URL.Query().Get("version") != ro.Version {
		writeErr(w, http.StatusNotFound, "not_found", "That version is not being rolled out.")
		return
	}
	f, err := os.Open(a.cachedMSIPath(ro.Version))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "This server has no copy of that MSI.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-msi")
	http.ServeContent(w, r, msiAsset, time.Time{}, f)
}
