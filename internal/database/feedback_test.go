package database

import (
	"regexp"
	"testing"
	"time"
)

var sqliteTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

func TestSaveFeedbackAndLastFeedbackTime(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.1, 70))
	if got := s.GetLastFeedbackTime("1"); got != nil {
		t.Fatalf("mai dato feedback: atteso nil, got %v", *got)
	}
	if !s.SaveFeedback("1", "command", nil, sp("Ottimo bot!")) {
		t.Fatal("SaveFeedback fallito")
	}
	assertLogContains(t, logs, "- database - INFO - 💬 Feedback salvato per utente 1 (tipo: command)")

	ts := s.GetLastFeedbackTime("1")
	if ts == nil || !sqliteTimestamp.MatchString(*ts) {
		t.Fatalf("formato timestamp SQLite atteso, got %v", deref(ts))
	}
	// CURRENT_TIMESTAMP è UTC
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", *ts, time.UTC)
	if err != nil || time.Since(parsed).Abs() > time.Minute {
		t.Fatalf("timestamp non UTC/attuale: %s (%v)", *ts, err)
	}
	if s.GetLastFeedbackTime("nonexistent") != nil {
		t.Fatal("utente inesistente: atteso nil")
	}
	if n := s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
}

// TestSaveFeedbackForeignKey: feedback per utente inesistente → errore
// FOREIGN KEY (foreign_keys ON) e nessuna riga inserita.
func TestSaveFeedbackForeignKey(t *testing.T) {
	logs := captureLogs(t)
	s := newTestStore(t)
	if s.SaveFeedback("ghost", "command", nil, nil) {
		t.Fatal("atteso false per utente inesistente")
	}
	assertLogContains(t, logs, "❌ Errore database:", "❌ Errore salvataggio feedback:", "FOREIGN KEY")
	if n := s.GetFeedbackCount(); n != 0 {
		t.Fatalf("righe inserite nonostante l'errore: %d", n)
	}
}

// TestSaveFeedbackAtomic: se l'UPDATE di last_feedback_at fallisce, anche
// l'INSERT viene annullato (stessa transazione, come in Python).
func TestSaveFeedbackAtomic(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.1, 70))
	if _, err := s.db.Exec(`CREATE TRIGGER no_upd BEFORE UPDATE OF last_feedback_at ON users
		BEGIN SELECT RAISE(ABORT, 'update bloccato'); END`); err != nil {
		t.Fatal(err)
	}
	if s.SaveFeedback("1", "command", nil, nil) {
		t.Fatal("atteso false")
	}
	if n := s.GetFeedbackCount(); n != 0 {
		t.Fatalf("INSERT non annullato: %d righe", n)
	}
}

func TestGetRecentFeedbacks(t *testing.T) {
	s := newTestStore(t)
	s.SaveUser("1", luceOnly("fissa", "monoraria", 0.1, 70))
	s.SaveUser("2", luceOnly("fissa", "monoraria", 0.1, 70))
	s.SaveFeedback("1", "command", nil, sp("primo"))
	s.SaveFeedback("2", "notification_rating", ip(1), nil)
	s.SaveFeedback("1", "periodic_survey", ip(5), sp("terzo"))
	for id, ts := range map[int]string{1: "2026-01-01 10:00:00", 2: "2026-01-01 10:00:00", 3: "2025-06-01 09:00:00"} {
		if _, err := s.db.Exec("UPDATE feedback SET created_at = ? WHERE id = ?", ts, id); err != nil {
			t.Fatal(err)
		}
	}
	got := s.GetRecentFeedbacks(10)
	if len(got) != 3 {
		t.Fatalf("attesi 3 feedback, got %d", len(got))
	}
	// created_at DESC, id DESC
	if got[0].ID != 2 || got[1].ID != 1 || got[2].ID != 3 {
		t.Fatalf("ordine errato: %d %d %d", got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].Rating == nil || *got[0].Rating != 1 || got[0].Comment != nil || got[0].UserID != "2" || got[0].FeedbackType != "notification_rating" {
		t.Fatalf("feedback 2 errato: %+v", got[0])
	}
	if got[1].Rating != nil || *got[1].Comment != "primo" {
		t.Fatalf("feedback 1 errato: %+v", got[1])
	}
	local := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC).In(time.Local).Format("2006-01-02 15:04:05")
	if got[0].CreatedAt == nil || *got[0].CreatedAt != local {
		t.Fatalf("created_at in ora locale atteso %s, got %v", local, deref(got[0].CreatedAt))
	}
	want := `{"id":2,"user_id":"2","feedback_type":"notification_rating","rating":1,"comment":null,"created_at":"` + local + `"}`
	if j := jsonString(t, got[0]); j != want {
		t.Fatalf("JSON feedback:\n got  %s\n want %s", j, want)
	}
	if lim := s.GetRecentFeedbacks(2); len(lim) != 2 || lim[0].ID != 2 {
		t.Fatalf("limit 2: %+v", lim)
	}
	// Rimozione utente → cascade sui feedback
	s.RemoveUser("1")
	if n := s.GetFeedbackCount(); n != 1 {
		t.Fatalf("cascade non applicato: %d feedback", n)
	}
}

func TestFeedbackDbErrors(t *testing.T) {
	logs := captureLogs(t)
	b := brokenStore(t)
	if b.SaveFeedback("1", "command", ip(3), sp("x")) {
		t.Fatal("SaveFeedback: atteso false")
	}
	if b.GetLastFeedbackTime("1") != nil {
		t.Fatal("GetLastFeedbackTime: atteso nil")
	}
	if got := b.GetRecentFeedbacks(10); got == nil || len(got) != 0 {
		t.Fatal("GetRecentFeedbacks: atteso []")
	}
	if b.GetFeedbackCount() != 0 {
		t.Fatal("GetFeedbackCount: atteso 0")
	}
	assertLogContains(t, logs, "❌ Errore salvataggio feedback:", "❌ Errore recupero last_feedback_time:",
		"❌ Errore recupero feedback recenti:", "❌ Errore conteggio feedback:")
}
