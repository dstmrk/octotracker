package database

// Compatibilità inversa (rollback alla versione Python): righe scritte dallo
// Store Go vengono lette dal vero database.py tramite .venv/bin/python e
// devono produrre gli stessi dict che legge Go; i blob JSON devono essere
// byte-identici a json.dumps di Python. Il test viene saltato se Python non
// è disponibile (es. CI solo Go).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

const pythonReaderScript = `
import json, sqlite3, sys
from pathlib import Path
db_path, repo = sys.argv[1], sys.argv[2]
sys.path.insert(0, repo)
import database as D
D.DB_FILE = Path(db_path)
D.DATA_DIR = Path(db_path).parent

ids = [r[0] for r in sqlite3.connect(db_path).execute("SELECT user_id FROM users")]
conn = sqlite3.connect(db_path)
raw_ok = {}
for uid in ids:
    ln, pr = conn.execute("SELECT last_notified_rates, pending_rates FROM users WHERE user_id = ?", (uid,)).fetchone()
    raw_ok[uid] = {
        "last_notified_rates": None if ln is None else json.dumps(json.loads(ln)) == ln,
        "pending_rates": None if pr is None else json.dumps(json.loads(pr)) == pr,
    }
conn.close()

hist_args = [
    ["luce", "fissa", "monoraria", 36500, True, True],
    ["gas", "variabile", "monoraria", 36500, True, True],
    ["luce", "variabile", "bioraria", 36500, False, True],
]
out = {
    "load_users": [[k, v] for k, v in D.load_users().items()],
    "load_user": {uid: D.load_user(uid) for uid in ids + ["nope"]},
    "load_pending_rates": {uid: D.load_pending_rates(uid) for uid in ids},
    "scadenza_reminded_map": D.get_scadenza_reminded_map(),
    "last_feedback_time": {uid: D.get_last_feedback_time(uid) for uid in ids},
    "recent_feedbacks": D.get_recent_feedbacks(100),
    "feedback_count": D.get_feedback_count(),
    "current_rates": D.get_current_rates(),
    "latest_rate_date": D.get_latest_rate_date(),
    "rate_history_dates": sorted(list(k) for k in D.get_rate_history_dates()),
    "rate_history": [D.get_rate_history(*a) for a in hist_args],
    "raw_roundtrip": raw_ok,
}
# Python applica le pending scritte da Go (formato vecchio senza updated_services)
out["apply_2001"] = list(D.apply_pending_rates("2001"))
print(json.dumps(out))
`

// findPython cerca .venv/bin/python e database.py risalendo dalla directory del package.
func findPython(t *testing.T) (python, repo string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		py := filepath.Join(dir, ".venv", "bin", "python")
		if _, err := os.Stat(py); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "database.py")); err == nil {
				return py, dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("Python (.venv/bin/python + database.py) non disponibile: test di compatibilità saltato")
		}
		dir = parent
	}
}

// writeGoRows popola il DB usando solo lo Store Go.
func writeGoRows(t *testing.T, s *Store) {
	t.Helper()
	must := func(ok bool, what string) {
		t.Helper()
		if !ok {
			t.Fatalf("%s fallito", what)
		}
	}
	must(s.SaveUser("2001", luceOnly("fissa", "monoraria", 0.145, 72.0)), "save 2001")

	u2 := User{
		Luce: Utility{Tipo: "variabile", Fascia: "trioraria", Energia: 0.1 + 0.2, Commercializzazione: 96.0,
			ConsumoF1: fp(900.5), ConsumoF2: fp(800.25), ConsumoF3: fp(700), Scadenza: "2026-11-30"},
		Gas: &Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.456, Commercializzazione: 84.0,
			ConsumoAnnuo: fp(1200), Scadenza: "2027-02-01"},
		LastNotifiedRates: LastNotifiedRates{"luce": NotifiedRate(0.1078, 72), "gas": NotifiedRate(0.39, 84)},
	}
	must(s.SaveUser("2002", u2), "save 2002")
	must(s.MarkScadenzaReminded("2002", "luce", "2026-11-30"), "mark 2002")

	u3 := luceOnly("fissa", "bioraria", 1e-05, 1e16)
	u3.LastNotifiedRates = LastNotifiedRates{
		"note": "città € 💡 \"q\" \\ \n", "luce_comm": 72.0, "list": []any{1.0, "x", nil, true, int64(3)},
	}
	must(s.SaveUser("-2003", u3), "save -2003")
	must(s.UpdateUserScadenza("-2003", "luce", "2027-05-05"), "update scadenza -2003")

	must(s.SavePendingRates("2001", PendingRates{Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.13, Commercializzazione: 70}}), "pending 2001")
	must(s.SavePendingRates("2002", PendingRates{
		Luce:            Utility{Tipo: "variabile", Fascia: "trioraria", ConsumoF1: fp(900.5), ConsumoF2: fp(800.25), ConsumoF3: fp(700), Energia: 0.011, Commercializzazione: 90},
		Gas:             &Utility{Tipo: "fissa", Fascia: "monoraria", ConsumoAnnuo: fp(1200), Energia: 0.4, Commercializzazione: 80},
		UpdatedServices: []string{"luce", "gas"},
	}), "pending 2002")
	must(s.SavePendingRates("-2003", PendingRates{Luce: u3.Luce, UpdatedServices: []string{}}), "pending -2003")

	must(s.SaveFeedback("2001", "command", nil, sp("ciao 👋 'x'")), "feedback 1")
	must(s.SaveFeedback("2002", "notification_rating", ip(1), nil), "feedback 2")

	must(s.SaveRatesBatch("2026-01-01", []RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.115, Commercializzazione: fp(72), CodOfferta: sp("LF")},
		{Servizio: "gas", Tipo: "variabile", Fascia: "monoraria", Energia: 0.05},
	}) == 2, "batch 1")
	must(s.SaveRatesBatch("2026-01-02", []RateRecord{
		{Servizio: "luce", Tipo: "fissa", Fascia: "monoraria", Energia: 0.1 + 0.2, Commercializzazione: fp(72)},
		{Servizio: "gas", Tipo: "variabile", Fascia: "monoraria", Energia: 0.052, Commercializzazione: fp(84), CodOfferta: sp("GV")},
	}) == 2, "batch 2")
	must(s.SaveRate("2026-01-01", "luce", "variabile", "bioraria", 0.0101, nil, nil), "save_rate")
}

func TestPythonReadsGoWrittenRows(t *testing.T) {
	python, repo := findPython(t)
	path := filepath.Join(t.TempDir(), "octotracker.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	writeGoRows(t, s)

	// Valori attesi: ciò che legge Go (prima che Python applichi le pending).
	ids := []string{}
	for _, u := range s.LoadUsers() {
		ids = append(ids, u.ID)
	}
	goUsers := [][2]any{}
	for _, u := range s.LoadUsers() {
		goUsers = append(goUsers, [2]any{u.ID, u})
	}
	goUser := map[string]any{"nope": nil}
	goPending := map[string]any{}
	goFeedbackTime := map[string]any{}
	for _, id := range ids {
		goUser[id] = userOrNil(s.LoadUser(id))
		goPending[id] = pendingOrNil(s.LoadPendingRates(id))
		goFeedbackTime[id] = s.GetLastFeedbackTime(id)
	}
	var dates [][4]string
	for k := range s.GetRateHistoryDates() {
		dates = append(dates, [4]string{k.DataFonte, k.Servizio, k.Tipo, k.Fascia})
	}
	sort.Slice(dates, func(i, j int) bool { return jsonString(t, dates[i]) < jsonString(t, dates[j]) })
	want := map[string]any{
		"load_users":            goUsers,
		"load_user":             goUser,
		"load_pending_rates":    goPending,
		"scadenza_reminded_map": s.GetScadenzaRemindedMap(),
		"last_feedback_time":    goFeedbackTime,
		"recent_feedbacks":      s.GetRecentFeedbacks(100),
		"feedback_count":        s.GetFeedbackCount(),
		"current_rates":         s.GetCurrentRates(),
		"latest_rate_date":      s.GetLatestRateDate(),
		"rate_history_dates":    dates,
		"rate_history": []RateHistory{
			s.GetRateHistory("luce", "fissa", "monoraria", 36500, true, true),
			s.GetRateHistory("gas", "variabile", "monoraria", 36500, true, true),
			s.GetRateHistory("luce", "variabile", "bioraria", 36500, false, true),
		},
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(python, "-c", pythonReaderScript, path, repo)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("python: %v\n%s", err, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output python non valido: %v\n%s", err, out)
	}

	for key, w := range want {
		assertSameJSON(t, "python "+key, got[key], w)
	}

	// Blob JSON scritti da Go identici byte per byte a json.dumps di Python.
	for id, cols := range got["raw_roundtrip"].(map[string]any) {
		for col, ok := range cols.(map[string]any) {
			if ok != nil && ok != true {
				t.Errorf("%s.%s scritto da Go non è identico a json.dumps di Python", id, col)
			}
		}
	}

	// Python ha applicato le pending scritte da Go: Go legge il risultato.
	if r := got["apply_2001"].([]any); r[0] != true || r[1] != "ok" {
		t.Fatalf("apply_pending_rates Python su pending Go = %v", r)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	u := s2.LoadUser("2001")
	if u.Luce.Energia != 0.13 || u.Luce.Commercializzazione != 70 || s2.LoadPendingRates("2001") != nil {
		t.Fatalf("stato dopo apply Python: %+v", u)
	}
}
