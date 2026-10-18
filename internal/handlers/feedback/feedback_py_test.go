package feedback

import (
	"strings"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

// Port di tests/test_feedback.py: un test Go per ogni test Python, con lo
// stesso nome in CamelCase. Le funzioni database sono quelle di
// internal/database (lo Store); "get_connection che solleva sqlite3.Error"
// diventa uno store chiuso, il monkeypatch di save_feedback un fakeStore.

// brokenStore restituisce uno store il cui database è chiuso: ogni
// operazione fallisce con un errore database.
func brokenStore(t *testing.T) *database.Store {
	t.Helper()
	s := newTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertContains(t *testing.T, text string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("il testo non contiene %q:\n%s", w, text)
		}
	}
}

// fixture comune: store temporaneo, handler, bot finto.
type env struct {
	t  *testing.T
	s  *database.Store
	h  *Handlers
	fc *testutil.FakeClient
}

func newEnv(t *testing.T) *env {
	s := newTestStore(t)
	return &env{t: t, s: s, h: &Handlers{Store: s, UserData: tg.NewUserData()}}
}

// ========== TEST DATABASE FEEDBACK ==========

// test_schema_has_feedback_columns
func TestSchemaHasFeedbackColumns(t *testing.T) {
	s := newTestStore(t)
	db := rawDB(t, s)
	rows, err := db.Query("PRAGMA table_info(users)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		found = found || name == "last_feedback_at"
	}
	if !found {
		t.Error("colonna last_feedback_at mancante")
	}
	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='feedback'").Scan(&name); err != nil {
		t.Errorf("tabella feedback mancante: %v", err)
	}
}

// test_save_feedback_success
func TestSaveFeedbackSuccess(t *testing.T) {
	s := newTestStore(t)
	saveSampleUser(t, s, testUserID)
	if !s.SaveFeedback(testUserID, "command", ip(5), sp("Ottimo bot!")) {
		t.Fatal("SaveFeedback = false")
	}
	if n := s.GetFeedbackCount(); n != 1 {
		t.Errorf("count = %d", n)
	}
}

// test_save_feedback_no_comment
func TestSaveFeedbackNoComment(t *testing.T) {
	s := newTestStore(t)
	saveSampleUser(t, s, testUserID)
	if !s.SaveFeedback(testUserID, "command", ip(4), nil) {
		t.Fatal("SaveFeedback = false")
	}
	if n := s.GetFeedbackCount(); n != 1 {
		t.Errorf("count = %d", n)
	}
}

// test_get_last_feedback_time_none
func TestGetLastFeedbackTimeNone(t *testing.T) {
	s := newTestStore(t)
	saveSampleUser(t, s, testUserID)
	if v := s.GetLastFeedbackTime(testUserID); v != nil {
		t.Errorf("got %q", *v)
	}
}

// test_get_last_feedback_time_after_feedback
func TestGetLastFeedbackTimeAfterFeedback(t *testing.T) {
	s := newTestStore(t)
	saveSampleUser(t, s, testUserID)
	s.SaveFeedback(testUserID, "command", ip(5), sp("Test"))
	last := s.GetLastFeedbackTime(testUserID)
	if last == nil {
		t.Fatal("last_feedback_at nil")
	}
	// datetime.fromisoformat solleva eccezione se non valido
	if _, err := fromISOFormat(*last); err != nil {
		t.Errorf("timestamp non valido %q: %v", *last, err)
	}
}

// test_get_recent_feedbacks
func TestGetRecentFeedbacks(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"111", "222", "333"} {
		saveSampleUser(t, s, id)
	}
	s.SaveFeedback("111", "command", ip(5), sp("Ottimo"))
	s.SaveFeedback("222", "command", ip(3), sp("Buono"))
	s.SaveFeedback("333", "command", ip(1), sp("Pessimo"))

	feedbacks := s.GetRecentFeedbacks(2)
	if len(feedbacks) != 2 {
		t.Fatalf("len = %d", len(feedbacks))
	}
	// Ordine decrescente per data
	if feedbacks[0].UserID != "333" || deref(feedbacks[0].Rating) != 1 || feedbacks[1].UserID != "222" {
		t.Errorf("ordine inatteso: %+v", feedbacks)
	}
}

// test_get_recent_feedbacks_empty
func TestGetRecentFeedbacksEmpty(t *testing.T) {
	if f := newTestStore(t).GetRecentFeedbacks(10); len(f) != 0 {
		t.Errorf("got %+v", f)
	}
}

// test_get_feedback_count
func TestGetFeedbackCount(t *testing.T) {
	s := newTestStore(t)
	if n := s.GetFeedbackCount(); n != 0 {
		t.Fatalf("count iniziale = %d", n)
	}
	saveSampleUser(t, s, "111")
	saveSampleUser(t, s, "222")
	s.SaveFeedback("111", "command", ip(5), nil)
	if n := s.GetFeedbackCount(); n != 1 {
		t.Errorf("count = %d", n)
	}
	s.SaveFeedback("222", "command", ip(4), nil)
	if n := s.GetFeedbackCount(); n != 2 {
		t.Errorf("count = %d", n)
	}
}

// test_save_feedback_without_user: con le foreign key abilitate il
// salvataggio per un utente inesistente fallisce.
func TestSaveFeedbackWithoutUser(t *testing.T) {
	if newTestStore(t).SaveFeedback("999999", "command", ip(5), sp("Test utente inesistente")) {
		t.Error("atteso false (FK constraint)")
	}
}

// test_get_last_feedback_time_user_not_exists
func TestGetLastFeedbackTimeUserNotExists(t *testing.T) {
	if v := newTestStore(t).GetLastFeedbackTime("999999"); v != nil {
		t.Errorf("got %q", *v)
	}
}

// test_save_feedback_database_error
func TestSaveFeedbackDatabaseError(t *testing.T) {
	if brokenStore(t).SaveFeedback("123", "command", ip(5), sp("Test")) {
		t.Error("atteso false")
	}
}

// test_get_last_feedback_time_database_error
func TestGetLastFeedbackTimeDatabaseError(t *testing.T) {
	if v := brokenStore(t).GetLastFeedbackTime("123"); v != nil {
		t.Errorf("got %q", *v)
	}
}

// test_get_recent_feedbacks_database_error
func TestGetRecentFeedbacksDatabaseError(t *testing.T) {
	if f := brokenStore(t).GetRecentFeedbacks(10); len(f) != 0 {
		t.Errorf("got %+v", f)
	}
}

// test_get_feedback_count_database_error
func TestGetFeedbackCountDatabaseError(t *testing.T) {
	if n := brokenStore(t).GetFeedbackCount(); n != 0 {
		t.Errorf("got %d", n)
	}
}

// ========== TEST CONVERSAZIONE FEEDBACK ==========

func (e *env) message(text string) result {
	e.t.Helper()
	b, fc := testutil.NewBot(e.t)
	e.fc = fc
	return call(e.t, fc, e.h.UserData, e.handlerFor(text), b, testutil.MessageContext(b, testutil.DefaultUserID, text))
}

// handlerFor sceglie l'handler per un messaggio: /feedback → feedback_command,
// altro testo → feedback_comment.
func (e *env) handlerFor(text string) handlers.Response {
	if text == "/feedback" {
		return e.h.FeedbackCommand
	}
	return e.h.FeedbackComment
}

func (e *env) callback(data string, skip bool) result {
	e.t.Helper()
	b, fc := testutil.NewBot(e.t)
	e.fc = fc
	h := e.h.FeedbackRating
	if skip {
		h = e.h.FeedbackSkipComment
	}
	return call(e.t, fc, e.h.UserData, h, b, callbackContext(b, data))
}

// onlyCall restituisce l'unica chiamata a method e verifica che sia l'unica
// di quel tipo (assert_called_once).
func onlyCall(t *testing.T, res result, method string) testutil.Call {
	t.Helper()
	var found []testutil.Call
	for _, c := range res.calls {
		if c.Method == method {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("attesa una chiamata %s, chiamate: %+v (err=%v)", method, res.calls, res.err)
	}
	return found[0]
}

// test_feedback_command_success
func TestFeedbackCommandSuccess(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	res := e.message("/feedback")
	if res.state != StateRating {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "sendMessage").Text(), "Come valuteresti OctoTracker?")
}

// test_feedback_command_rate_limited: feedback già dato meno di 24h fa.
func TestFeedbackCommandRateLimited(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	e.s.SaveFeedback(testUserID, "command", ip(5), sp("Test"))
	res := e.message("/feedback")
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "sendMessage").Text(), "già inviato un feedback")
}

// test_feedback_rating
func TestFeedbackRating(t *testing.T) {
	e := newEnv(t)
	res := e.callback("rating_4", false)
	if res.state != StateComment {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	if res.userAfter[keyRating] != 4 {
		t.Errorf("user_data[rating] = %v", res.userAfter[keyRating])
	}
	onlyCall(t, res, "answerCallbackQuery")
	assertContains(t, onlyCall(t, res, "editMessageText").Text(), "⭐⭐⭐⭐")
}

// test_feedback_comment
func TestFeedbackComment(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 5)
	res := e.message("Ottimo bot, molto utile!")
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "sendMessage").Text(), "Feedback ricevuto")
	if n := e.s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
	f := e.s.GetRecentFeedbacks(1)[0]
	if deref(f.Rating) != 5 || deref(f.Comment) != "Ottimo bot, molto utile!" {
		t.Errorf("feedback salvato: %v %v", deref(f.Rating), deref(f.Comment))
	}
}

// test_feedback_skip_comment
func TestFeedbackSkipComment(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 3)
	res := e.callback("skip_comment", true)
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	onlyCall(t, res, "answerCallbackQuery")
	onlyCall(t, res, "editMessageText")
	if n := e.s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
	f := e.s.GetRecentFeedbacks(1)[0]
	if deref(f.Rating) != 3 || f.Comment != nil {
		t.Errorf("feedback salvato: %v %v", deref(f.Rating), deref(f.Comment))
	}
}

// test_feedback_cancel
func TestFeedbackCancel(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	res := call(t, fc, e.h.UserData, e.h.FeedbackCancel, b, testutil.MessageContext(b, testutil.DefaultUserID, "/cancel"))
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, strings.ToLower(onlyCall(t, res, "sendMessage").Text()), "annullato")
}

// test_feedback_cancel_with_callback_query
func TestFeedbackCancelWithCallbackQuery(t *testing.T) {
	e := newEnv(t)
	b, fc := testutil.NewBot(t)
	res := call(t, fc, e.h.UserData, e.h.FeedbackCancel, b, callbackContext(b, "x"))
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	onlyCall(t, res, "editMessageText")
}

// ========== TEST RATE LIMITING ==========

// test_feedback_rate_limiting_integration: rate limiting completo 24h.
func TestFeedbackRateLimitingIntegration(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)

	// Primo feedback: deve funzionare
	if res := e.message("/feedback"); res.state != StateRating {
		t.Fatalf("primo: %q", res.state)
	}
	e.s.SaveFeedback(testUserID, "command", ip(5), nil)

	// Secondo feedback immediato: deve essere bloccato
	if res := e.message("/feedback"); res.state != "END" {
		t.Fatalf("secondo: %q", res.state)
	}

	// Simula passaggio di 24 ore: (datetime.now() - timedelta(hours=25)).isoformat()
	old := time.Now().Add(-25 * time.Hour).Format("2006-01-02T15:04:05.000000")
	setLastFeedback(t, e.s, testUserID, old)

	// Terzo feedback dopo 24h: deve funzionare
	if res := e.message("/feedback"); res.state != StateRating {
		t.Fatalf("terzo: %q", res.state)
	}
}

// test_feedback_command_invalid_timestamp: timestamp malformato → si procede.
func TestFeedbackCommandInvalidTimestamp(t *testing.T) {
	e := newEnv(t)
	saveSampleUser(t, e.s, testUserID)
	setLastFeedback(t, e.s, testUserID, "invalid")
	if res := e.message("/feedback"); res.state != StateRating {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
}

// test_feedback_comment_database_error: save_feedback restituisce False.
func TestFeedbackCommentDatabaseError(t *testing.T) {
	e := newEnv(t)
	e.h.Store = &fakeStore{save: false}
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 5)
	res := e.message("Test commento")
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, strings.ToLower(onlyCall(t, res, "sendMessage").Text()), "errore")
}

// test_feedback_skip_comment_database_error
func TestFeedbackSkipCommentDatabaseError(t *testing.T) {
	e := newEnv(t)
	e.h.Store = &fakeStore{save: false}
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 3)
	res := e.callback("skip_comment", true)
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, strings.ToLower(onlyCall(t, res, "editMessageText").Text()), "errore")
}

// ========== TEST VALIDAZIONE LUNGHEZZA COMMENTO ==========

// test_feedback_comment_too_long
func TestFeedbackCommentTooLong(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 5)
	res := e.message(strings.Repeat("A", MaxCommentLength+1))
	// Deve rimanere in stato COMMENT per permettere un nuovo tentativo
	if res.state != StateComment {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	text := onlyCall(t, res, "sendMessage").Text()
	assertContains(t, text, "troppo lungo", "1001", "1000")
	if n := e.s.GetFeedbackCount(); n != 0 {
		t.Errorf("feedback salvato: %d", n)
	}
}

// test_feedback_comment_exact_max_length
func TestFeedbackCommentExactMaxLength(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 4)
	saveSampleUser(t, e.s, testUserID)
	comment := strings.Repeat("B", MaxCommentLength)
	res := e.message(comment)
	if res.state != "END" {
		t.Fatalf("stato %q err=%v", res.state, res.err)
	}
	assertContains(t, onlyCall(t, res, "sendMessage").Text(), "Feedback ricevuto")
	if n := e.s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
	f := e.s.GetRecentFeedbacks(1)[0]
	if deref(f.Rating) != 4 || deref(f.Comment) != comment || len(*f.Comment) != MaxCommentLength {
		t.Errorf("feedback salvato: %v len=%d", deref(f.Rating), len(deref(f.Comment).(string)))
	}
}

// test_feedback_comment_retry_after_too_long
func TestFeedbackCommentRetryAfterTooLong(t *testing.T) {
	e := newEnv(t)
	e.h.UserData.Set(testutil.DefaultUserID, keyRating, 5)
	saveSampleUser(t, e.s, testUserID)

	// Primo tentativo: commento troppo lungo
	if res := e.message(strings.Repeat("X", MaxCommentLength+500)); res.state != StateComment {
		t.Fatalf("primo: %q", res.state)
	}
	if n := e.s.GetFeedbackCount(); n != 0 {
		t.Fatalf("count = %d", n)
	}

	// Secondo tentativo: commento valido
	if res := e.message("Ottimo bot!"); res.state != "END" {
		t.Fatalf("secondo: %q", res.state)
	}
	if n := e.s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
	if c := deref(e.s.GetRecentFeedbacks(1)[0].Comment); c != "Ottimo bot!" {
		t.Errorf("comment = %v", c)
	}
}

// ========== TEST ON DELETE CASCADE ==========

// test_cascade_delete_removes_feedback
func TestCascadeDeleteRemovesFeedback(t *testing.T) {
	s := newTestStore(t)
	saveSampleUser(t, s, testUserID)
	s.SaveFeedback(testUserID, "command", ip(5), sp("Primo feedback"))
	s.SaveFeedback(testUserID, "command", ip(4), sp("Secondo feedback"))
	s.SaveFeedback(testUserID, "command", ip(3), sp("Terzo feedback"))
	if n := s.GetFeedbackCount(); n != 3 {
		t.Fatalf("count = %d", n)
	}
	if !s.RemoveUser(testUserID) {
		t.Fatal("RemoveUser = false")
	}
	if n := s.GetFeedbackCount(); n != 0 {
		t.Errorf("feedback non cancellati: %d", n)
	}
}

// test_cascade_delete_preserves_other_users_feedback
func TestCascadeDeletePreservesOtherUsersFeedback(t *testing.T) {
	s := newTestStore(t)
	user1, user2 := "111111111", "222222222"
	saveSampleUser(t, s, user1)
	saveSampleUser(t, s, user2)
	s.SaveFeedback(user1, "command", ip(5), sp("User 1 feedback 1"))
	s.SaveFeedback(user1, "command", ip(4), sp("User 1 feedback 2"))
	s.SaveFeedback(user2, "command", ip(3), sp("User 2 feedback 1"))
	if n := s.GetFeedbackCount(); n != 3 {
		t.Fatalf("count = %d", n)
	}
	s.RemoveUser(user1)
	if n := s.GetFeedbackCount(); n != 1 {
		t.Fatalf("count = %d", n)
	}
	f := s.GetRecentFeedbacks(10)
	if len(f) != 1 || f[0].UserID != user2 || deref(f[0].Comment) != "User 2 feedback 1" {
		t.Errorf("feedback rimasti: %+v", f)
	}
}

// test_schema_has_cascade_delete
func TestSchemaHasCascadeDelete(t *testing.T) {
	var sqlText string
	if err := rawDB(t, newTestStore(t)).QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='feedback'").Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	assertContains(t, sqlText, "ON DELETE CASCADE")
}

// test_foreign_keys_enabled: in Python PRAGMA foreign_keys su
// get_connection() vale 1. La connessione dello Store non è esposta: si
// verifica che le foreign key siano applicate dallo Store (vincolo e
// cascade), che è l'effetto osservabile del PRAGMA.
func TestForeignKeysEnabled(t *testing.T) {
	s := newTestStore(t)
	if s.SaveFeedback("utente-inesistente", "command", ip(1), nil) {
		t.Error("FK non applicata: feedback salvato per utente inesistente")
	}
	saveSampleUser(t, s, testUserID)
	s.SaveFeedback(testUserID, "command", ip(1), nil)
	s.RemoveUser(testUserID)
	if n := s.GetFeedbackCount(); n != 0 {
		t.Error("ON DELETE CASCADE non applicato")
	}
	// Anche una connessione aperta con le stesse opzioni dello Store ha
	// foreign_keys = 1.
	var fk int
	if err := rawDB(t, s).QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("PRAGMA foreign_keys = %d (%v)", fk, err)
	}
}
