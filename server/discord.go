package main

// discord.go - posting events straight into a Discord channel.
//
// Discord webhooks don't take Foghorn's own JSON: they want a message, so
// when a webhook's address is a Discord webhook URL each event is turned
// into a message with one embed. Discord ignores the signature headers, so
// the signing secret is not needed for these.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// isDiscordWebhook reports whether u is a Discord webhook URL, e.g.
// https://discord.com/api/webhooks/123/abc.
func isDiscordWebhook(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" {
		return false
	}
	switch strings.ToLower(p.Hostname()) {
	case "discord.com", "discordapp.com", "ptb.discord.com", "canary.discord.com":
	default:
		return false
	}
	return strings.HasPrefix(p.Path, "/api/webhooks/") || strings.HasPrefix(p.Path, "/api/v10/webhooks/")
}

func webhookKind(u string) string {
	if isDiscordWebhook(u) {
		return "discord"
	}
	return "json"
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
	Timestamp string `json:"timestamp,omitempty"`
}

type discordMessage struct {
	Username        string         `json:"username"`
	Embeds          []discordEmbed `json:"embeds"`
	AllowedMentions struct {
		Parse []string `json:"parse"`
	} `json:"allowed_mentions"`
}

const (
	discordBlue  = 0x3B82F6
	discordAmber = 0xF59E0B
	discordRed   = 0xDC2626
	discordGreen = 0x16A34A
	discordGrey  = 0x6B7280
)

// discordBody turns a Foghorn webhook payload into a Discord message.
func discordBody(payload []byte) ([]byte, error) {
	var p struct {
		Event     string         `json:"event"`
		CreatedAt time.Time      `json:"created_at"`
		Org       string         `json:"org"`
		Data      map[string]any `json:"data"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, err
	}
	d := p.Data
	e := discordEmbed{Color: discordGrey}
	e.Footer.Text = "Foghorn"
	if p.Org != "" {
		e.Footer.Text = p.Org + " · Foghorn"
	}
	if !p.CreatedAt.IsZero() {
		e.Timestamp = p.CreatedAt.UTC().Format(time.RFC3339)
	}
	add := func(name, value string, inline bool) {
		if value = strings.TrimSpace(value); value != "" {
			e.Fields = append(e.Fields, discordField{clip(name, 256), clip(value, 1024), inline})
		}
	}

	switch p.Event {
	case "alert.sent":
		a := obj(d, "alert")
		spec := obj(a, "spec")
		e.Title = "Alert sent: " + str(spec, "title")
		e.Description = str(spec, "message")
		e.Color = levelColour(str(spec, "level"), str(spec, "accent"))
		if link := str(spec, "link"); strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			e.URL = link
		}
		add("Level", title(str(spec, "level")), true)
		add("Sent to", str(a, "target_summary"), true)
		add("Sent by", sender(a), true)
		add("Computers reached now", num(d, "online_reach"), true)
		if b, _ := spec["require_ack"].(bool); b {
			add("Must be acknowledged", "Yes", true)
		}
		if rs, ok := spec["responses"].([]any); ok && len(rs) > 0 {
			var names []string
			for _, r := range rs {
				if s, ok := r.(string); ok {
					names = append(names, s)
				}
			}
			add("Reply buttons", strings.Join(names, ", "), false)
		}
	case "alert.acknowledged":
		e.Title = "Read: " + str(d, "title")
		e.Color = discordGreen
		e.Description = who(d) + " pressed “I’ve read this”."
	case "alert.responded":
		e.Title = "Reply: " + str(d, "title")
		e.Color = discordBlue
		e.Description = who(d) + " answered **" + str(d, "response") + "**."
	case "alert.recalled":
		a := obj(d, "alert")
		e.Title = "Alert recalled: " + str(obj(a, "spec"), "title")
		e.Color = discordGrey
		add("Recalled by", str(d, "recalled_by"), true)
		add("Was sent to", str(a, "target_summary"), true)
	case "lock.started":
		l := obj(d, "lock")
		spec := obj(l, "spec")
		e.Title = "Screens locked"
		e.Description = str(spec, "message")
		e.Color = discordRed
		add("Locked", str(l, "target_summary"), true)
		add("Locked by", sender(l), true)
		if str(spec, "mode") == "freeze" {
			add("Mode", "Frozen (screen visible, input blocked)", true)
		} else {
			add("Mode", "Screen covered", true)
		}
		if m := num(spec, "duration_minutes"); m != "" && m != "0" {
			add("Unlocks itself after", m+" min", true)
		}
		add("Computers reached now", num(d, "online_reach"), true)
	case "lock.unlocked":
		l := obj(d, "lock")
		e.Title = "Screens unlocked"
		e.Color = discordGreen
		add("Unlocked", str(l, "target_summary"), true)
		add("Unlocked by", str(d, "unlocked_by"), true)
	case "ping":
		e.Title = "Test from Foghorn"
		e.Color = discordBlue
		e.Description = str(d, "message") + "\nThis channel will get Foghorn’s events from now on."
	default:
		e.Title = p.Event
	}
	e.Title = clip(e.Title, 256)
	e.Description = clip(e.Description, 4096)

	m := discordMessage{Username: "Foghorn", Embeds: []discordEmbed{e}}
	m.AllowedMentions.Parse = []string{} // alert text must never ping @everyone or a role
	return json.Marshal(m)
}

func obj(m map[string]any, k string) map[string]any {
	if v, ok := m[k].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(m map[string]any, k string) string {
	if f, ok := m[k].(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

func sender(m map[string]any) string {
	if n := str(m, "sender_name"); n != "" {
		return n
	}
	return str(m, "sender")
}

// who is "user on HOSTNAME", or whichever half is known.
func who(d map[string]any) string {
	u, h := str(d, "user"), str(d, "hostname")
	switch {
	case u != "" && h != "":
		return fmt.Sprintf("**%s** on %s", u, h)
	case u != "":
		return "**" + u + "**"
	case h != "":
		return h
	}
	return "Someone"
}

func title(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func levelColour(level, accent string) int {
	if len(accent) == 7 && accent[0] == '#' {
		if v, err := strconv.ParseInt(accent[1:], 16, 32); err == nil {
			return int(v)
		}
	}
	switch level {
	case "critical":
		return discordRed
	case "warning":
		return discordAmber
	}
	return discordBlue
}

// clip shortens s to at most n characters, as Discord rejects longer fields.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
