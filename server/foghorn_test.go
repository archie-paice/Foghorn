package main

import (
	"encoding/hex"
	"testing"
	"time"
)

func TestPBKDF2Vectors(t *testing.T) {
	// Published PBKDF2-HMAC-SHA256 test vectors.
	cases := []struct {
		iter int
		want string
	}{
		{1, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{2, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{4096, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(pbkdf2SHA256([]byte("password"), []byte("salt"), c.iter, 32))
		if got != c.want {
			t.Errorf("iter=%d got %s want %s", c.iter, got, c.want)
		}
	}
	long := hex.EncodeToString(pbkdf2SHA256([]byte("passwordPASSWORDpassword"), []byte("saltSALTsaltSALTsaltSALTsaltSALTsalt"), 4096, 40))
	if long != "348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9" {
		t.Errorf("multi-block vector wrong: %s", long)
	}
}

func TestPasswordRoundTrip(t *testing.T) {
	h := hashPassword("correct horse battery")
	if !verifyPassword(h, "correct horse battery") || verifyPassword(h, "wrong") || verifyPassword("garbage", "x") {
		t.Fatal("password verification is broken")
	}
}

func TestWildcard(t *testing.T) {
	yes := [][2]string{{"LAB1-*", "lab1-pc07"}, {"*", ""}, {"*", "x"}, {"a?c", "ABC"}, {"*-PC??", "LIB-PC12"}, {"lab*pc*7", "lab1-pc07"}, {"exact", "EXACT"}}
	no := [][2]string{{"LAB1-*", "lab2-pc07"}, {"a?c", "ac"}, {"exact", "exactly"}, {"", "x"}, {"*-PC??", "LIB-PC1"}}
	for _, c := range yes {
		if !wildcardMatch(c[0], c[1]) {
			t.Errorf("%q should match %q", c[0], c[1])
		}
	}
	for _, c := range no {
		if wildcardMatch(c[0], c[1]) {
			t.Errorf("%q should not match %q", c[0], c[1])
		}
	}
}

func TestRules(t *testing.T) {
	c := &ClientInfo{Hostname: "LAB1-PC07", User: "jsmith", Domain: "COLLEGE", IP: "10.20.30.41",
		OU:     "CN=LAB1-PC07,OU=Lab 1,OU=Computing,OU=Student PCs,DC=college,DC=local",
		Groups: []string{`COLLEGE\Students-Computing`, `COLLEGE\Year1`}}
	yes := []Rule{{"hostname", "lab1-*"}, {"user", "JSMITH"}, {"user", `college\js*`}, {"ou", "OU=Lab 1"}, {"ou", "*OU=Computing*"},
		{"group", "Students-Computing"}, {"group", `COLLEGE\Year*`}, {"ip", "10.20.30.0/24"}, {"ip", "10.20.*"}}
	no := []Rule{{"hostname", "lab1"}, {"user", "smith"}, {"ou", "OU=Lab 2"}, {"group", "Staff"}, {"ip", "10.20.31.0/24"}, {"bogus", "*"}, {"hostname", " "}}
	for _, r := range yes {
		if !ruleMatches(r, c) {
			t.Errorf("rule %+v should match", r)
		}
	}
	for _, r := range no {
		if ruleMatches(r, c) {
			t.Errorf("rule %+v should not match", r)
		}
	}
}

// TestDeliveryLifecycle walks one alert through send, display, restart and ack.
func TestDeliveryLifecycle(t *testing.T) {
	now := time.Now()
	s := &Store{Clients: map[string]*ClientInfo{}}
	s.State.Settings.OrgName = "Test College"
	ack := &Alert{ID: "a1", Spec: AlertSpec{Title: "Ack me", RequireAck: true, Target: Target{All: true}},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), Deliveries: map[string]*Delivery{}}
	plain := &Alert{ID: "a2", Spec: AlertSpec{Title: "Lab 2 only"}, Rules: []Rule{{"hostname", "LAB2-*"}},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), Deliveries: map[string]*Delivery{}}
	old := &Alert{ID: "a3", Spec: AlertSpec{Title: "Expired", Target: Target{All: true}},
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), Deliveries: map[string]*Delivery{}}
	s.Alerts = []*Alert{old, ack, plain}

	c := s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s1"}, "10.0.0.1", now)
	got, _ := s.pendingFor(c, now)
	if len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("first poll: want only a1, got %+v", got)
	}
	if got, _ = s.pendingFor(c, now.Add(time.Second)); len(got) != 0 {
		t.Fatalf("must not resend immediately, got %+v", got)
	}
	if got, _ = s.pendingFor(c, now.Add(2*time.Minute)); len(got) != 1 {
		t.Fatalf("unconfirmed alert should be resent after a minute, got %+v", got)
	}
	s.applyEvents(c, []clientEvent{{AlertID: "a1", Event: "displayed"}}, now)
	if got, _ = s.pendingFor(c, now.Add(5*time.Minute)); len(got) != 0 {
		t.Fatalf("displayed alert must not be resent to the same session, got %+v", got)
	}
	// user logs off and on again without acknowledging
	c = s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s2"}, "10.0.0.1", now)
	if got, _ = s.pendingFor(c, now); len(got) != 1 {
		t.Fatalf("unacknowledged alert should reappear after a restart, got %+v", got)
	}
	s.applyEvents(c, []clientEvent{{AlertID: "a1", Event: "displayed"}, {AlertID: "a1", Event: "acknowledged"}}, now)
	c = s.touchClient(clientHello{Hostname: "LAB1-PC01", User: "amy", SID: "s3"}, "10.0.0.1", now)
	if got, _ = s.pendingFor(c, now); len(got) != 0 {
		t.Fatalf("acknowledged alert must never come back, got %+v", got)
	}
	if tot := ack.totals(); tot.Sent != 1 || tot.Displayed != 1 || tot.Acked != 1 {
		t.Fatalf("totals wrong: %+v", tot)
	}

	// recall reaches a client that is still showing the alert, exactly once
	c2 := s.touchClient(clientHello{Hostname: "LAB2-PC09", User: "bob", SID: "x"}, "10.0.0.2", now)
	got, _ = s.pendingFor(c2, now)
	if len(got) != 2 {
		t.Fatalf("LAB2 client should get both live alerts, got %+v", got)
	}
	s.applyEvents(c2, []clientEvent{{AlertID: "a2", Event: "displayed"}}, now)
	t2 := now
	plain.RecalledAt = &t2
	if _, rec := s.pendingFor(c2, now); len(rec) != 1 || rec[0] != "a2" {
		t.Fatalf("recall should be delivered, got %+v", rec)
	}
	if _, rec := s.pendingFor(c2, now); len(rec) != 0 {
		t.Fatalf("recall should be delivered once, got %+v", rec)
	}
}
