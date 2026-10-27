package main

// sounds.go - custom sounds for alerts.
//
// Administrators upload WAV or MP3 files. Each is stored once under
// sounds/ in the data folder and named after a hash of its content, so a PC
// that has downloaded a sound can keep its copy for ever: the same id can
// never mean different audio.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxSoundBytes = 5 << 20

// soundKind recognises a sound file by its first bytes and returns its
// extension, or "" when it is not a WAV or an MP3.
func soundKind(b []byte) string {
	switch {
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE")):
		return ".wav"
	case len(b) >= 3 && bytes.Equal(b[0:3], []byte("ID3")):
		return ".mp3"
	case len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0: // an MPEG audio frame
		return ".mp3"
	}
	return ""
}

func soundMIME(ext string) string {
	if ext == ".mp3" {
		return "audio/mpeg"
	}
	return "audio/wav"
}

func (a *App) soundPath(snd *Sound) string {
	return filepath.Join(a.store.dir, "sounds", snd.ID+snd.Ext)
}

func (a *App) handleListSounds(w http.ResponseWriter, r *http.Request, id *identity) {
	a.store.mu.Lock()
	out := append([]*Sound{}, a.store.State.Sounds...)
	a.store.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// handleUploadSound takes the raw file as the request body, with its name in
// ?name=. A raw body rather than a form keeps it a one-liner from curl, PHP
// or PowerShell.
func (a *App) handleUploadSound(w http.ResponseWriter, r *http.Request, id *identity) {
	name := cleanText(r.URL.Query().Get("name"), 60)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "Give the sound a name.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSoundBytes+1))
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(body) > maxSoundBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "Sounds can be up to 5 MB. Trim it or save it as an MP3.")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "The upload did not arrive in one piece. Try again.")
		return
	}
	ext := soundKind(body)
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "That is not a WAV or MP3 file.")
		return
	}
	sum := sha256.Sum256(body)
	sid := hex.EncodeToString(sum[:8])

	a.store.mu.Lock()
	if existing := a.store.soundByID(sid); existing != nil {
		out := *existing
		a.store.mu.Unlock()
		writeJSON(w, http.StatusOK, out)
		return
	}
	a.store.mu.Unlock()

	snd := &Sound{ID: sid, Name: name, Ext: ext, Size: int64(len(body)), UploadedBy: id.User.Username, UploadedAt: time.Now()}
	if err := os.MkdirAll(filepath.Join(a.store.dir, "sounds"), 0o750); err == nil {
		err = writeFileAtomic(a.soundPath(snd), body)
	}
	if err != nil {
		log.Printf("ERROR saving sound: %v", err)
		writeErr(w, http.StatusInternalServerError, "server_error", "The server could not save the file. Check its data folder has free space.")
		return
	}
	a.store.mu.Lock()
	a.store.State.Sounds = append(a.store.State.Sounds, snd)
	sort.Slice(a.store.State.Sounds, func(i, j int) bool {
		return strings.ToLower(a.store.State.Sounds[i].Name) < strings.ToLower(a.store.State.Sounds[j].Name)
	})
	a.store.dirtyState = true
	out := *snd
	a.store.mu.Unlock()
	a.save()
	log.Printf("sound %q (%s, %d bytes) uploaded by %s", name, ext, len(body), id.User.Username)
	writeJSON(w, http.StatusCreated, out)
}

func (a *App) handleRenameSound(w http.ResponseWriter, r *http.Request, id *identity) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req, 8<<10) {
		return
	}
	req.Name = cleanText(req.Name, 60)
	if req.Name == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "Give the sound a name.")
		return
	}
	a.store.mu.Lock()
	snd := a.store.soundByID(r.PathValue("id"))
	if snd == nil {
		a.store.mu.Unlock()
		writeErr(w, http.StatusNotFound, "not_found", "That sound no longer exists.")
		return
	}
	snd.Name = req.Name
	a.store.dirtyState = true
	out := *snd
	a.store.mu.Unlock()
	a.save()
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleDeleteSound(w http.ResponseWriter, r *http.Request, id *identity) {
	sid := r.PathValue("id")
	a.store.mu.Lock()
	var gone *Sound
	keep := a.store.State.Sounds[:0]
	for _, x := range a.store.State.Sounds {
		if x.ID == sid {
			gone = x
		} else {
			keep = append(keep, x)
		}
	}
	a.store.State.Sounds = keep
	// Templates and schedules that used it fall back to the Windows sound.
	for _, t := range a.store.State.Templates {
		if t.Spec.SoundID == sid {
			t.Spec.SoundID = ""
		}
	}
	for _, sc := range a.store.State.Schedules {
		if sc.Spec.SoundID == sid {
			sc.Spec.SoundID = ""
		}
	}
	a.store.dirtyState = true
	a.store.mu.Unlock()
	if gone != nil {
		_ = os.Remove(a.soundPath(gone))
		log.Printf("sound %q deleted by %s", gone.Name, id.User.Username)
	}
	a.save()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) serveSound(w http.ResponseWriter, r *http.Request, sid string) {
	a.store.mu.Lock()
	snd := a.store.soundByID(sid)
	var cp Sound
	if snd != nil {
		cp = *snd
	}
	a.store.mu.Unlock()
	if snd == nil {
		writeErr(w, http.StatusNotFound, "not_found", "No such sound.")
		return
	}
	f, err := os.Open(a.soundPath(&cp))
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "The sound file is missing from the server's data folder.")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", soundMIME(cp.Ext))
	// The id is a content hash, so the file behind it never changes.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, cp.ID+cp.Ext, cp.UploadedAt, f)
}

func (a *App) handleSoundFile(w http.ResponseWriter, r *http.Request, id *identity) {
	a.serveSound(w, r, r.PathValue("id"))
}

// handleClientSound lets desktop clients fetch a sound with the client key.
func (a *App) handleClientSound(w http.ResponseWriter, r *http.Request) {
	if !a.checkClientKey(r) {
		writeErr(w, http.StatusUnauthorized, "bad_client_key", "The client key is missing or wrong.")
		return
	}
	a.serveSound(w, r, r.PathValue("id"))
}
