package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsDiscordWebhook(t *testing.T) {
	yes := []string{
		"https://discord.com/api/webhooks/123/abc",
		"https://discordapp.com/api/webhooks/123/abc",
		"https://ptb.discord.com/api/webhooks/123/abc",
		"https://canary.discord.com/api/webhooks/123/abc?thread_id=9",
		"https://DISCORD.com/api/v10/webhooks/123/abc",
	}
	no := []string{
		"http://discord.com/api/webhooks/123/abc", // Discord is https only
		"https://discord.com/channels/1/2",
		"https://evil.example/api/webhooks/123/abc",
		"https://discord.com.evil.example/api/webhooks/1/a",
		"https://intranet.example.sch.uk/foghorn/webhook",
	}
	for _, u := range yes {
		if !isDiscordWebhook(u) {
			t.Errorf("%s should count as Discord", u)
		}
	}
	for _, u := range no {
		if isDiscordWebhook(u) {
			t.Errorf("%s should not count as Discord", u)
		}
	}
}

func discordFor(t *testing.T, event string, data any) discordMessage {
	t.Helper()
	p, _ := json.Marshal(webhookPayload{ID: "d1", Event: event, CreatedAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC), Org: "Hill School", Data: data})
	b, err := discordBody(p)
	if err != nil {
		t.Fatal(err)
	}
	var m discordMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Embeds) != 1 {
		t.Fatalf("want one embed, got %d", len(m.Embeds))
	}
	if !strings.Contains(string(b), `"allowed_mentions":{"parse":[]}`) {
		t.Fatalf("mentions must be switched off: %s", b)
	}
	return m
}

func TestDiscordAlertSent(t *testing.T) {
	v := alertView{ID: "a1", Spec: AlertSpec{Title: "Fire drill @everyone", Message: "Leave by the nearest exit", Level: "critical",
		RequireAck: true, Responses: []string{"Out", "Need help"}, Link: "https://example.org/plan"},
		TargetSummary: "Everyone", SenderName: "Ms Smith"}
	m := discordFor(t, "alert.sent", map[string]any{"alert": v, "online_reach": 42})
	e := m.Embeds[0]
	if e.Title != "Alert sent: Fire drill @everyone" || e.Description != "Leave by the nearest exit" {
		t.Fatalf("title/description wrong: %+v", e)
	}
	if e.Color != discordRed || e.URL != "https://example.org/plan" || e.Footer.Text != "Hill School · Foghorn" {
		t.Fatalf("colour/link/footer wrong: %+v", e)
	}
	got := map[string]string{}
	for _, f := range e.Fields {
		got[f.Name] = f.Value
	}
	if got["Sent to"] != "Everyone" || got["Sent by"] != "Ms Smith" || got["Computers reached now"] != "42" ||
		got["Reply buttons"] != "Out, Need help" || got["Must be acknowledged"] != "Yes" {
		t.Fatalf("fields wrong: %v", got)
	}
}

func TestDiscordOtherEvents(t *testing.T) {
	cases := map[string]struct {
		data  any
		title string
		has   string
	}{
		"alert.acknowledged": {map[string]any{"title": "Drill", "user": "jbloggs", "hostname": "LAB-01"}, "Read: Drill", "**jbloggs** on LAB-01"},
		"alert.responded":    {map[string]any{"title": "Drill", "user": "jbloggs", "response": "Out"}, "Reply: Drill", "**Out**"},
		"alert.recalled":     {map[string]any{"alert": alertView{Spec: AlertSpec{Title: "Drill"}}, "recalled_by": "admin"}, "Alert recalled: Drill", "admin"},
		"lock.started":       {map[string]any{"lock": lockView{Spec: LockSpec{Message: "Eyes front", DurationMinutes: 10}, TargetSummary: "Room 4"}, "online_reach": 3}, "Screens locked", "10 min"},
		"lock.unlocked":      {map[string]any{"lock": lockView{TargetSummary: "Room 4"}, "unlocked_by": "admin"}, "Screens unlocked", "Room 4"},
		"ping":               {map[string]string{"message": "Test from Foghorn, sent by admin"}, "Test from Foghorn", "sent by admin"},
	}
	for ev, c := range cases {
		m := discordFor(t, ev, c.data)
		raw, _ := json.Marshal(m)
		if m.Embeds[0].Title != c.title || !strings.Contains(string(raw), c.has) {
			t.Errorf("%s: got %s", ev, raw)
		}
	}
}

func TestDiscordClipsLongText(t *testing.T) {
	long := strings.Repeat("é", 5000)
	m := discordFor(t, "alert.sent", map[string]any{"alert": alertView{Spec: AlertSpec{Title: long, Message: long}}})
	if n := len([]rune(m.Embeds[0].Title)); n != 256 {
		t.Fatalf("title %d runes", n)
	}
	if n := len([]rune(m.Embeds[0].Description)); n != 4096 {
		t.Fatalf("description %d runes", n)
	}
}

func TestRedactURL(t *testing.T) {
	u := "https://discord.com/api/webhooks/123/SECRET"
	got := redactURL(`Post "`+u+`": dial tcp: timeout`, u)
	if strings.Contains(got, "SECRET") || !strings.Contains(got, "https://discord.com/…") {
		t.Fatalf("got %q", got)
	}
}
