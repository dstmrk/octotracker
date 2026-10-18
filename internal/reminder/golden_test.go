package reminder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/dateutil"
)

// Test differenziali contro reminder.py: testdata/reminder_golden.json è
// generato eseguendo _get_expiring_services, format_reminder_message,
// _prepare_reminder e _format_days_left su centinaia di combinazioni
// (fissa/variabile, con/senza gas, scadenze dentro/fuori finestra, già
// notificate, malformate o in formati ISO alternativi, tariffe Octopus
// presenti/assenti, commercializzazione mancante, codici offerta).

type goldenExpiring struct {
	Servizio string `json:"servizio"`
	Scadenza string `json:"scadenza"`
	DaysLeft int    `json:"days_left"`
}

type goldenReminderCase struct {
	Today    string                `json:"today"`
	User     database.User         `json:"user"`
	Reminded map[string]*string    `json:"reminded"`
	Rates    database.CurrentRates `json:"rates"`
	Expiring []goldenExpiring      `json:"expiring"`
	Message  *string               `json:"message"`
	Prepare  *struct {
		Message  string           `json:"message"`
		Expiring []goldenExpiring `json:"expiring"`
	} `json:"prepare"`
}

type goldenFormatCase struct {
	User     database.User         `json:"user"`
	Rates    database.CurrentRates `json:"rates"`
	Expiring []goldenExpiring      `json:"expiring"`
	Message  string                `json:"message"`
}

type goldenReminderFile struct {
	Cases       []goldenReminderCase `json:"cases"`
	FormatCases []goldenFormatCase   `json:"format_cases"`
	DaysLeft    map[string]string    `json:"days_left"`
}

func loadReminderGolden(t *testing.T) goldenReminderFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "reminder_golden.json"))
	if err != nil {
		t.Fatalf("lettura golden: %v", err)
	}
	var g goldenReminderFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decodifica golden: %v", err)
	}
	if len(g.Cases) < 300 || len(g.FormatCases) == 0 || len(g.DaysLeft) == 0 {
		t.Fatalf("corpus golden incompleto: %d casi, %d format, %d days_left", len(g.Cases), len(g.FormatCases), len(g.DaysLeft))
	}
	return g
}

func toExpiring(in []goldenExpiring) []ExpiringService {
	out := make([]ExpiringService, len(in))
	for i, e := range in {
		out[i] = ExpiringService(e)
	}
	return out
}

func sameExpiring(got []ExpiringService, want []goldenExpiring) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != ExpiringService(want[i]) {
			return false
		}
	}
	return true
}

func assertSameText(t *testing.T, what, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	t.Errorf("%s: testo diverso dal byte %d\n got: %q\nwant: %q", what, i, got, want)
}

func TestGoldenReminder(t *testing.T) {
	g := loadReminderGolden(t)

	for i, c := range g.Cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			today, ok := dateutil.ParseISODate(c.Today)
			if !ok {
				t.Fatalf("today non valido: %q", c.Today)
			}
			user := c.User
			user.ID = "u1"
			reminded := database.ScadenzaReminded{Luce: c.Reminded["luce"], Gas: c.Reminded["gas"]}

			expiring := getExpiringServices(&user, reminded, today)
			if !sameExpiring(expiring, c.Expiring) {
				t.Fatalf("expiring = %+v, atteso %+v", expiring, c.Expiring)
			}
			if c.Message != nil {
				assertSameText(t, "message", FormatReminderMessage(expiring, &user, c.Rates), *c.Message)
			}

			remindedMap := map[string]database.ScadenzaReminded{"u1": reminded}
			msg, exp, ok := prepareReminder("u1", &user, remindedMap, c.Rates, today)
			if (c.Prepare != nil) != ok {
				t.Fatalf("prepare ok = %v, atteso %v", ok, c.Prepare != nil)
			}
			if ok {
				assertSameText(t, "prepare.message", msg, c.Prepare.Message)
				if !sameExpiring(exp, c.Prepare.Expiring) {
					t.Errorf("prepare.expiring = %+v, atteso %+v", exp, c.Prepare.Expiring)
				}
			}
		})
	}

	for i, c := range g.FormatCases {
		t.Run("format/"+strconv.Itoa(i), func(t *testing.T) {
			user := c.User
			assertSameText(t, "message", FormatReminderMessage(toExpiring(c.Expiring), &user, c.Rates), c.Message)
		})
	}

	for k, want := range g.DaysLeft {
		d, err := strconv.Atoi(k)
		if err != nil {
			t.Fatalf("chiave days_left non valida: %q", k)
		}
		if got := formatDaysLeft(d); got != want {
			t.Errorf("formatDaysLeft(%d) = %q, atteso %q", d, got, want)
		}
	}
}
