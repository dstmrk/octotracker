package database

// Compatibilità con il DB scritto da Python: testdata/python_fixture.db è
// stato generato con il vero database.py e python_fixture_expected.json
// contiene ciò che Python legge da quel file (load_users, load_user,
// load_pending_rates, get_current_rates, get_rate_history, ... e lo stato
// dopo apply_pending_rates). Il package Go deve leggere esattamente gli
// stessi valori e riscrivere i blob JSON con gli stessi byte.

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

type fixtureExpected struct {
	LoadUsers           [][2]any                  `json:"load_users"`
	LoadUser            map[string]any            `json:"load_user"`
	LoadPendingRates    map[string]any            `json:"load_pending_rates"`
	UserExists          map[string]bool           `json:"user_exists"`
	UserCount           int                       `json:"user_count"`
	ScadenzaRemindedMap any                       `json:"scadenza_reminded_map"`
	LastFeedbackTime    map[string]*string        `json:"last_feedback_time"`
	RecentFeedbacks     []map[string]any          `json:"recent_feedbacks"`
	RecentFeedbacks2    []map[string]any          `json:"recent_feedbacks_limit2"`
	FeedbackCount       int                       `json:"feedback_count"`
	CurrentRates        any                       `json:"current_rates"`
	LatestRateDate      *string                   `json:"latest_rate_date"`
	RateHistoryDates    [][4]string               `json:"rate_history_dates"`
	RateHistory         []fixtureHistoryCase      `json:"rate_history"`
	Raw                 map[string]map[string]any `json:"raw"`
	AfterApply          []fixtureApplyCase        `json:"after_apply"`
}

type fixtureHistoryCase struct {
	Args   [6]any `json:"args"`
	Result any    `json:"result"`
}

type fixtureApplyCase struct {
	UserID   string         `json:"user_id"`
	Result   [2]any         `json:"result"`
	User     any            `json:"user"`
	Reminded any            `json:"reminded"`
	Pending  any            `json:"pending"`
	Raw      map[string]any `json:"raw"`
}

var fixtureUserIDs = []string{"1001", "1002", "1003", "1004", "1005", "1006", "-1001234567890", "nonexistent"}

func loadFixtureExpected(t *testing.T) fixtureExpected {
	t.Helper()
	b, err := os.ReadFile("testdata/python_fixture_expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var exp fixtureExpected
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	return exp
}

// openFixtureCopy apre una copia del DB scritto da Python.
func openFixtureCopy(t *testing.T) *Store {
	t.Helper()
	src, err := os.Open("testdata/python_fixture.db")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	path := filepath.Join(t.TempDir(), "octotracker.db")
	dst, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open fixture: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func assertSameJSON(t *testing.T, what string, got, want any) {
	t.Helper()
	g := toGeneric(t, got)
	w := toGeneric(t, want)
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %s\n want %s", what, jsonString(t, g), jsonString(t, w))
	}
}

// userOrNil evita che un *User nil diventi un'interfaccia non-nil.
func userOrNil(u *User) any {
	if u == nil {
		return nil
	}
	return u
}

func pendingOrNil(p *PendingRates) any {
	if p == nil {
		return nil
	}
	return p
}

func TestPythonFixtureUsers(t *testing.T) {
	exp := loadFixtureExpected(t)
	s := openFixtureCopy(t)

	users := s.LoadUsers()
	if len(users) != len(exp.LoadUsers) {
		t.Fatalf("load_users: %d utenti, attesi %d", len(users), len(exp.LoadUsers))
	}
	for i, u := range users {
		if u.ID != exp.LoadUsers[i][0] {
			t.Errorf("load_users[%d]: id %s, atteso %v (ordine del dict Python)", i, u.ID, exp.LoadUsers[i][0])
		}
		assertSameJSON(t, "load_users "+u.ID, u, exp.LoadUsers[i][1])
	}

	for _, id := range fixtureUserIDs {
		assertSameJSON(t, "load_user "+id, userOrNil(s.LoadUser(id)), exp.LoadUser[id])
		assertSameJSON(t, "load_pending_rates "+id, pendingOrNil(s.LoadPendingRates(id)), exp.LoadPendingRates[id])
		if got := s.UserExists(id); got != exp.UserExists[id] {
			t.Errorf("user_exists %s = %v", id, got)
		}
		if got, want := s.GetLastFeedbackTime(id), exp.LastFeedbackTime[id]; !reflect.DeepEqual(got, want) {
			t.Errorf("get_last_feedback_time %s = %v, want %v", id, deref(got), deref(want))
		}
	}
	if got := s.GetUserCount(); got != exp.UserCount {
		t.Errorf("get_user_count = %d, want %d", got, exp.UserCount)
	}
	assertSameJSON(t, "get_scadenza_reminded_map", s.GetScadenzaRemindedMap(), exp.ScadenzaRemindedMap)
}

// TestPythonFixtureLastNotifiedEquality: il dict letto da Python e quello
// letto da Go sono uguali secondo l'uguaglianza Python (anche con int 72).
func TestPythonFixtureLastNotifiedEquality(t *testing.T) {
	s := openFixtureCopy(t)
	u := s.LoadUser("1003")
	cur := LastNotifiedRates{
		"luce": NotifiedRate(0.1078, 72.0),
		"gas":  NotifiedRate(0.39, 84.0),
	}
	if !u.LastNotifiedRates.Equal(cur) {
		t.Fatal("last_notified_rates di 1003 deve essere uguale alle tariffe correnti")
	}
	cur["gas"] = NotifiedRate(0.39, 84.5)
	if u.LastNotifiedRates.Equal(cur) {
		t.Fatal("tariffe diverse non devono risultare uguali")
	}
	legacy := s.LoadUser("1005").LastNotifiedRates
	if v, ok := legacy["luce_comm"].(int64); !ok || v != 72 {
		t.Fatalf("int Python deve restare intero: %#v", legacy["luce_comm"])
	}
	if !legacy.Equal(LastNotifiedRates{"luce_energia": 0.12, "luce_comm": 72.0, "timestamp": "2024-01-15T10:00:00", "note": "città €"}) {
		t.Fatal("72 == 72.0 come in Python")
	}
	// Utente senza last_notified_rates: equivale a {} (get con default {})
	if !s.LoadUser("1001").LastNotifiedRates.Equal(LastNotifiedRates{}) {
		t.Fatal("assente deve equivalere a {}")
	}
}

// TestPythonFixtureRawBlobs: Go riscrive i blob JSON con gli stessi byte di
// json.dumps di Python (ordine chiavi canonico del checker).
func TestPythonFixtureRawBlobs(t *testing.T) {
	exp := loadFixtureExpected(t)
	s := openFixtureCopy(t)
	for id, raw := range exp.Raw {
		if p, ok := raw["pending_rates"].(string); ok {
			got, err := pyDumps(s.LoadPendingRates(id).toODict())
			if err != nil || got != p {
				t.Errorf("pending_rates %s:\n got  %s\n want %s", id, got, p)
			}
		}
		if l, ok := raw["last_notified_rates"].(string); ok {
			got, err := pyDumps(map[string]any(s.LoadUser(id).LastNotifiedRates))
			if err != nil {
				t.Fatal(err)
			}
			if id == "1005" {
				// Formato legacy con chiavi in ordine non canonico: contenuto
				// identico, ordine delle chiavi diverso.
				a, _ := pyLoads(got)
				b, _ := pyLoads(l)
				if !pyEqual(a, b) {
					t.Errorf("last_notified_rates %s: %s != %s", id, got, l)
				}
				continue
			}
			if got != l {
				t.Errorf("last_notified_rates %s:\n got  %s\n want %s", id, got, l)
			}
		}
	}

	// SaveUser di un utente letto da Python produce lo stesso blob.
	u := s.LoadUser("1003")
	if !s.SaveUser("copy", *u) {
		t.Fatal("SaveUser")
	}
	if got, want := rawColumn(t, s, "copy", "last_notified_rates"), exp.Raw["1003"]["last_notified_rates"]; got != want {
		t.Errorf("blob riscritto:\n got  %v\n want %v", got, want)
	}
}

func TestPythonFixtureFeedbacks(t *testing.T) {
	exp := loadFixtureExpected(t)
	s := openFixtureCopy(t)

	// Il golden è generato con TZ=UTC: converti created_at nell'ora locale
	// del processo, come fa datetime(created_at, 'localtime').
	toLocal := func(list []map[string]any) []map[string]any {
		out := make([]map[string]any, len(list))
		for i, f := range list {
			c := map[string]any{}
			for k, v := range f {
				c[k] = v
			}
			if ts, ok := c["created_at"].(string); ok {
				tm, err := time.ParseInLocation("2006-01-02 15:04:05", ts, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				c["created_at"] = tm.In(time.Local).Format("2006-01-02 15:04:05")
			}
			out[i] = c
		}
		return out
	}
	assertSameJSON(t, "get_recent_feedbacks(100)", s.GetRecentFeedbacks(100), toLocal(exp.RecentFeedbacks))
	assertSameJSON(t, "get_recent_feedbacks(2)", s.GetRecentFeedbacks(2), toLocal(exp.RecentFeedbacks2))
	if got := s.GetFeedbackCount(); got != exp.FeedbackCount {
		t.Errorf("get_feedback_count = %d, want %d", got, exp.FeedbackCount)
	}
}

func TestPythonFixtureRates(t *testing.T) {
	exp := loadFixtureExpected(t)
	s := openFixtureCopy(t)

	assertSameJSON(t, "get_current_rates", s.GetCurrentRates(), exp.CurrentRates)
	if got := s.GetLatestRateDate(); !reflect.DeepEqual(got, exp.LatestRateDate) {
		t.Errorf("get_latest_rate_date = %v", deref(got))
	}

	var dates [][4]string
	for k := range s.GetRateHistoryDates() {
		dates = append(dates, [4]string{k.DataFonte, k.Servizio, k.Tipo, k.Fascia})
	}
	sort.Slice(dates, func(i, j int) bool {
		for k := 0; k < 4; k++ {
			if dates[i][k] != dates[j][k] {
				return dates[i][k] < dates[j][k]
			}
		}
		return false
	})
	if !reflect.DeepEqual(dates, exp.RateHistoryDates) {
		t.Errorf("get_rate_history_dates:\n got  %v\n want %v", dates, exp.RateHistoryDates)
	}

	for _, c := range exp.RateHistory {
		a := c.Args
		got := s.GetRateHistory(a[0].(string), a[1].(string), a[2].(string), int(a[3].(float64)), a[4].(bool), a[5].(bool))
		assertSameJSON(t, "get_rate_history "+jsonString(t, a), got, c.Result)
	}
}

// TestPythonFixtureApplyPendingRates: apply_pending_rates in Go sulla copia
// del DB Python produce gli stessi esiti e lo stesso stato finale.
func TestPythonFixtureApplyPendingRates(t *testing.T) {
	exp := loadFixtureExpected(t)
	s := openFixtureCopy(t)
	for _, c := range exp.AfterApply {
		ok, reason := s.ApplyPendingRates(c.UserID)
		if ok != c.Result[0].(bool) || reason != c.Result[1].(string) {
			t.Errorf("apply_pending_rates %s = (%v, %s), want %v", c.UserID, ok, reason, c.Result)
		}
		assertSameJSON(t, "user dopo apply "+c.UserID, userOrNil(s.LoadUser(c.UserID)), c.User)
		assertSameJSON(t, "pending dopo apply "+c.UserID, pendingOrNil(s.LoadPendingRates(c.UserID)), c.Pending)
		var reminded any
		if r, ok := s.GetScadenzaRemindedMap()[c.UserID]; ok {
			reminded = r
		}
		assertSameJSON(t, "reminded dopo apply "+c.UserID, reminded, c.Reminded)
		if c.Raw != nil {
			for col, want := range c.Raw {
				if got := rawColumn(t, s, c.UserID, col); !reflect.DeepEqual(got, want) {
					t.Errorf("%s.%s dopo apply = %v, want %v", c.UserID, col, got, want)
				}
			}
		}
	}
}
