// Foghorn server - sends pop-up alerts to Windows desktops.
//
// One self-contained executable: it serves the web console, the API the
// desktop clients check in to, and keeps its data as JSON files in one folder.
package main

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var version = "1.1.0"

const firstRunFile = "FIRST-RUN.txt"

//go:embed web
var webFS embed.FS

type Config struct {
	Listen            string `json:"listen"`
	TLSCert           string `json:"tls_cert"`
	TLSKey            string `json:"tls_key"`
	TrustProxyHeaders bool   `json:"trust_proxy_headers"`
}

type App struct {
	cfg      Config
	store    *Store
	hub      *hub
	sessions *sessionStore
	throttle *throttle
	hooks    *webhookSender
	idem     *idemCache
	closing  atomic.Bool
}

func defaultDataDir() string {
	if d := os.Getenv("FOGHORN_DATA"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "Foghorn")
	}
	return "foghorn-data"
}

const usage = `Foghorn server %s

Usage:
  foghorn-server [run] [-data DIR] [-listen ADDR]   Run in this window (Ctrl+C to stop)
  foghorn-server service install [-data DIR]        Install and start the Windows service
  foghorn-server service uninstall                  Stop and remove the Windows service
  foghorn-server service start | stop | status
  foghorn-server reset-password [-data DIR] USER    Give USER a new temporary password
  foghorn-server version

Data folder: %s
Settings such as the port and HTTPS certificate live in config.json inside it.
`

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "run":
		fl := flag.NewFlagSet("run", flag.ExitOnError)
		dataDir := fl.String("data", defaultDataDir(), "folder for data, config and logs")
		listen := fl.String("listen", "", "address to listen on, e.g. :8080 (overrides config.json)")
		fl.Parse(args)
		if isWindowsService() {
			runAsService(*dataDir, *listen)
			return
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := runServer(ctx, *dataDir, *listen, true); err != nil {
			fmt.Fprintln(os.Stderr, "foghorn-server:", err)
			os.Exit(1)
		}
	case "service":
		if err := serviceCommand(args); err != nil {
			fmt.Fprintln(os.Stderr, "foghorn-server:", err)
			os.Exit(1)
		}
	case "reset-password":
		fl := flag.NewFlagSet("reset-password", flag.ExitOnError)
		dataDir := fl.String("data", defaultDataDir(), "data folder")
		fl.Parse(args)
		if fl.NArg() != 1 {
			fmt.Fprintln(os.Stderr, "usage: foghorn-server reset-password [-data DIR] USERNAME")
			os.Exit(2)
		}
		if err := resetPassword(*dataDir, fl.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "foghorn-server:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	default:
		fmt.Printf(usage, version, defaultDataDir())
		if cmd != "help" {
			os.Exit(2)
		}
	}
}

func loadConfig(dir string) (Config, error) {
	cfg := Config{Listen: ":8080"}
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		return cfg, os.WriteFile(path, append(out, '\n'), 0o640)
	}
	if err != nil {
		return cfg, err
	}
	b = []byte(strings.TrimPrefix(string(b), "\ufeff")) // Notepad likes to add a BOM
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("config.json is not valid JSON: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return cfg, errors.New("config.json: set both tls_cert and tls_key, or neither")
	}
	return cfg, nil
}

func setupLogging(dir string, alsoStdout bool) io.Closer {
	path := filepath.Join(dir, "foghorn.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 10<<20 {
		os.Remove(path + ".1")
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		log.Printf("cannot open log file: %v", err)
		return io.NopCloser(nil)
	}
	if alsoStdout {
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	} else {
		log.SetOutput(f)
	}
	return f
}

// bootstrap creates the first administrator and the client key on a brand-new
// install, and leaves the details in FIRST-RUN.txt for whoever is installing.
func (a *App) bootstrap() error {
	st := a.store
	st.mu.Lock()
	needKey := st.State.Settings.ClientKey == ""
	needAdmin := len(st.State.Users) == 0
	if needKey {
		st.State.Settings.ClientKey = randomSecret(24)
		st.dirtyState = true
	}
	st.mu.Unlock()

	if needAdmin {
		pw := generatePassword()
		h := hashPassword(pw)
		st.mu.Lock()
		st.State.Users = append(st.State.Users, &User{
			ID: newID(), Username: "admin", DisplayName: "Administrator", Role: "admin",
			PassHash: h, MustChange: true, CreatedAt: time.Now(),
		})
		if len(st.State.Templates) == 0 {
			st.State.Templates = starterTemplates()
		}
		key := st.State.Settings.ClientKey
		st.dirtyState = true
		st.mu.Unlock()

		text := fmt.Sprintf("Foghorn first-run details\r\n"+
			"=========================\r\n\r\n"+
			"Web console username : admin\r\n"+
			"Temporary password   : %s\r\n"+
			"  (you will be asked to choose your own the first time you sign in;\r\n"+
			"   this file is deleted automatically when you do)\r\n\r\n"+
			"Client key           : %s\r\n"+
			"  (the desktop clients need this; it is also shown in the web console\r\n"+
			"   under Settings, so you do not need to keep this file)\r\n", pw, key)
		if err := os.WriteFile(filepath.Join(st.dir, firstRunFile), []byte(text), 0o600); err != nil {
			return err
		}
		log.Printf("first run: created administrator \"admin\"; temporary password written to %s",
			filepath.Join(st.dir, firstRunFile))
	}
	return st.flush()
}

func starterTemplates() []*Template {
	mk := func(name, title, msg, level, display string, ack, sound bool, secs int) *Template {
		return &Template{ID: newID(), Name: name, Spec: AlertSpec{
			Title: title, Message: msg, Level: level, Display: display, RequireAck: ack,
			Sound: sound, DisplaySeconds: secs, ExpiresMinutes: 10,
			Target: Target{GroupIDs: []string{}, Rules: []Rule{}},
		}}
	}
	return []*Template{
		mk("Save your work", "Save your work", "This session ends in 5 minutes. Save your work and log off when you are done.", "info", "corner", false, false, 60),
		mk("Eyes to the front", "Eyes to the front, please", "Stop what you are doing and look at the front of the room.", "warning", "center", false, true, 0),
		mk("Planned restart", "This computer will restart soon", "IT are restarting computers in this room in 10 minutes to install updates. Save your work now.", "warning", "center", true, true, 0),
		mk("Evacuate", "Leave the building now", "Leave by the nearest exit and go to your assembly point. Do not stop to collect belongings.", "critical", "fullscreen", true, true, 0),
	}
}

func resetPassword(dir, username string) error {
	// A running server keeps accounts in memory and would overwrite our change,
	// so insist that it is stopped first. We detect that by trying its port.
	if cfg, err := loadConfig(dir); err == nil {
		ln, lerr := net.Listen("tcp", cfg.Listen)
		if lerr != nil {
			return fmt.Errorf("the Foghorn server looks like it is running (port %s is in use).\nStop it first:  %s", cfg.Listen, serviceHint("stop"))
		}
		ln.Close()
	}
	st, err := openStore(dir)
	if err != nil {
		return err
	}
	u := st.userByName(username)
	if u == nil {
		return fmt.Errorf("there is no account called %q in %s", username, dir)
	}
	pw := generatePassword()
	u.PassHash, u.MustChange, u.Disabled = hashPassword(pw), true, false
	st.dirtyState = true
	if err := st.flush(); err != nil {
		return err
	}
	fmt.Printf("Temporary password for %s: %s\n", u.Username, pw)
	fmt.Println("Now start the server again:  " + serviceHint("start"))
	return nil
}

// serviceHint is the command that stops or starts the server on this OS.
func serviceHint(verb string) string {
	if runtime.GOOS == "windows" {
		return "foghorn-server service " + verb
	}
	return "sudo systemctl " + verb + " foghorn"
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// desktop clients
	mux.HandleFunc("POST /api/client/poll", a.handlePoll)
	mux.HandleFunc("GET /api/client/sounds/{id}", a.handleClientSound)

	// sign in / own account
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)
	mux.HandleFunc("GET /api/me", a.guard("self", "", a.handleMe))
	mux.HandleFunc("POST /api/me/password", a.guard("self", "", a.handleChangeOwnPassword))
	mux.HandleFunc("GET /api/meta", a.guard("any", "", a.handleMeta))

	// The second argument is the scope an API token needs for the call.
	// alerts
	mux.HandleFunc("GET /api/alerts", a.guard("any", "read", a.handleListAlerts))
	mux.HandleFunc("POST /api/alerts", a.guard("any", "send", a.handleSendAlert))
	mux.HandleFunc("GET /api/alerts/{id}", a.guard("any", "read", a.handleGetAlert))
	mux.HandleFunc("POST /api/alerts/{id}/recall", a.guard("any", "send", a.handleRecallAlert))
	mux.HandleFunc("POST /api/reach", a.guard("any", "read", a.handleReach))
	mux.HandleFunc("GET /api/clients", a.guard("any", "read", a.handleListClients))
	mux.HandleFunc("GET /api/groups", a.guard("any", "read", a.handleListGroups))
	mux.HandleFunc("GET /api/templates", a.guard("any", "read", a.handleListTemplates))
	mux.HandleFunc("POST /api/templates", a.guard("any", "send", a.handleSaveTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", a.guard("any", "send", a.handleDeleteTemplate))

	// screen locks
	mux.HandleFunc("GET /api/locks", a.guard("any", "read", a.handleListLocks))
	mux.HandleFunc("POST /api/locks", a.guard("any", "lock", a.handleCreateLock))
	mux.HandleFunc("POST /api/locks/unlock-all", a.guard("any", "lock", a.handleUnlockAll))
	mux.HandleFunc("GET /api/locks/{id}", a.guard("any", "read", a.handleGetLock))
	mux.HandleFunc("POST /api/locks/{id}/unlock", a.guard("any", "lock", a.handleUnlock))

	// schedules
	mux.HandleFunc("GET /api/schedules", a.guard("any", "read", a.handleListSchedules))
	mux.HandleFunc("POST /api/schedules", a.guard("any", "send", a.handleSaveSchedule))
	mux.HandleFunc("PUT /api/schedules/{id}", a.guard("any", "send", a.handleSaveSchedule))
	mux.HandleFunc("DELETE /api/schedules/{id}", a.guard("any", "send", a.handleDeleteSchedule))
	mux.HandleFunc("POST /api/schedules/{id}/run", a.guard("any", "send", a.handleRunSchedule))

	// sounds
	mux.HandleFunc("GET /api/sounds", a.guard("any", "read", a.handleListSounds))
	mux.HandleFunc("GET /api/sounds/{id}/file", a.guard("any", "read", a.handleSoundFile))
	mux.HandleFunc("POST /api/sounds", a.guard("admin", "", a.handleUploadSound))
	mux.HandleFunc("PUT /api/sounds/{id}", a.guard("admin", "", a.handleRenameSound))
	mux.HandleFunc("DELETE /api/sounds/{id}", a.guard("admin", "", a.handleDeleteSound))

	// administrators
	mux.HandleFunc("DELETE /api/clients/{id}", a.guard("admin", "", a.handleForgetClient))
	mux.HandleFunc("POST /api/groups", a.guard("admin", "", a.handleSaveGroup))
	mux.HandleFunc("PUT /api/groups/{id}", a.guard("admin", "", a.handleSaveGroup))
	mux.HandleFunc("DELETE /api/groups/{id}", a.guard("admin", "", a.handleDeleteGroup))
	mux.HandleFunc("GET /api/users", a.guard("admin", "", a.handleListUsers))
	mux.HandleFunc("POST /api/users", a.guard("admin", "", a.handleSaveUser))
	mux.HandleFunc("PUT /api/users/{id}", a.guard("admin", "", a.handleSaveUser))
	mux.HandleFunc("DELETE /api/users/{id}", a.guard("admin", "", a.handleDeleteUser))
	mux.HandleFunc("GET /api/settings", a.guard("admin", "", a.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", a.guard("admin", "", a.handleSaveSettings))
	mux.HandleFunc("POST /api/settings/rotate-client-key", a.guard("admin", "", a.handleRotateKey))
	mux.HandleFunc("POST /api/tokens", a.guard("admin", "", a.handleCreateToken))
	mux.HandleFunc("DELETE /api/tokens/{id}", a.guard("admin", "", a.handleDeleteToken))
	mux.HandleFunc("GET /api/webhooks", a.guard("admin", "", a.handleListWebhooks))
	mux.HandleFunc("POST /api/webhooks", a.guard("admin", "", a.handleSaveWebhook))
	mux.HandleFunc("PUT /api/webhooks/{id}", a.guard("admin", "", a.handleSaveWebhook))
	mux.HandleFunc("DELETE /api/webhooks/{id}", a.guard("admin", "", a.handleDeleteWebhook))
	mux.HandleFunc("POST /api/webhooks/{id}/test", a.guard("admin", "", a.handleTestWebhook))

	// The API description, for code generators and API tools. No sign-in needed:
	// it describes the API, it does not grant any access to it.
	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		b, err := webFS.ReadFile("web/openapi.json")
		if err != nil {
			writeErr(w, http.StatusNotFound, "not_found", "No API description in this build.")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(b)
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "No such API endpoint.")
	})

	// web console
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// runServer runs until ctx is cancelled.
func runServer(ctx context.Context, dataDir, listenOverride string, interactive bool) error {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("cannot create data folder %s: %w", dataDir, err)
	}
	logFile := setupLogging(dataDir, interactive)
	defer logFile.Close()

	cfg, err := loadConfig(dataDir)
	if err != nil {
		log.Printf("ERROR %v", err)
		return err
	}
	if listenOverride != "" {
		cfg.Listen = listenOverride
	}
	st, err := openStore(dataDir)
	if err != nil {
		log.Printf("ERROR opening data: %v", err)
		return fmt.Errorf("cannot read data in %s: %w", dataDir, err)
	}
	app := &App{cfg: cfg, store: st, hub: newHub(), sessions: newSessionStore(), throttle: newThrottle(),
		hooks: newWebhookSender(), idem: newIdemCache()}
	if err := app.bootstrap(); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		ErrorLog:          log.New(io.Discard, "", 0), // TLS handshake noise from port scanners
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Printf("ERROR cannot listen on %s: %v", cfg.Listen, err)
		return fmt.Errorf("cannot listen on %s (is something else using that port?): %w", cfg.Listen, err)
	}
	scheme := "http"
	if cfg.TLSCert != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			log.Printf("ERROR loading HTTPS certificate: %v", err)
			return fmt.Errorf("cannot load HTTPS certificate: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		scheme = "https"
	}
	host, _ := os.Hostname()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	log.Printf("Foghorn server %s listening on %s  ->  %s://%s:%s/   data: %s", version, cfg.Listen, scheme, host, port, dataDir)
	if interactive {
		if b, err := os.ReadFile(filepath.Join(dataDir, firstRunFile)); err == nil {
			fmt.Printf("\n%s\n", b)
		}
	}

	// Write changes (delivery receipts mostly) to disk every couple of seconds.
	saverDone := make(chan struct{})
	go func() {
		defer close(saverDone)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				app.save()
			case <-ctx.Done():
				return
			}
		}
	}()

	hooksStop := make(chan struct{})
	app.runWebhooks(hooksStop)
	go app.scheduler(ctx)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Printf("shutting down")
	app.closing.Store(true)
	app.hub.wake() // release every held long-poll
	shCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		srv.Close()
	}
	<-saverDone
	close(hooksStop)
	app.hooks.wg.Wait()
	app.store.mu.Lock()
	app.store.dirtyClients = true
	app.store.mu.Unlock()
	return app.store.flush()
}
