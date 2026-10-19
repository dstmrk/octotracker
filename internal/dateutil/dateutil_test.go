package dateutil

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time { return NewDate(y, m, d) }

func mustParse(t *testing.T, text string) (time.Time, bool) {
	t.Helper()
	return ParseActivationDate(text)
}

func assertParsed(t *testing.T, text string, want time.Time) {
	t.Helper()
	got, ok := mustParse(t, text)
	if !ok || !got.Equal(want) {
		t.Fatalf("ParseActivationDate(%q) = (%v, %v), want %v", text, got, ok, want)
	}
}

func assertNotParsed(t *testing.T, text string) {
	t.Helper()
	if got, ok := mustParse(t, text); ok {
		t.Fatalf("ParseActivationDate(%q) = %v, want None", text, got)
	}
}

// ========== parse_activation_date (tests/test_date_utils.py) ==========

func TestParseActivationDateSlash(t *testing.T) {
	assertParsed(t, "15/03/2025", date(2025, 3, 15))
}

func TestParseActivationDateDash(t *testing.T) {
	assertParsed(t, "15-03-2025", date(2025, 3, 15))
}

func TestParseActivationDateDot(t *testing.T) {
	assertParsed(t, "15.03.2025", date(2025, 3, 15))
}

func TestParseActivationDateStripsWhitespace(t *testing.T) {
	assertParsed(t, "  01/12/2024  ", date(2024, 12, 1))
}

func TestParseActivationDateInvalidFormat(t *testing.T) {
	assertNotParsed(t, "2025-03-15") // formato ISO non accettato
}

func TestParseActivationDateNotADate(t *testing.T) {
	assertNotParsed(t, "domani")
}

func TestParseActivationDateEmpty(t *testing.T) {
	assertNotParsed(t, "")
}

func TestParseActivationDateImpossibleDay(t *testing.T) {
	assertNotParsed(t, "31/02/2025")
}

func TestParseActivationDateYearTooOld(t *testing.T) {
	assertNotParsed(t, "15/03/1999")
}

func TestParseActivationDateYearTooFarFuture(t *testing.T) {
	assertNotParsed(t, fmt.Sprintf("15/03/%d", Today().Year()+5))
}

// ========== add_months ==========

func TestAddMonthsSimple(t *testing.T) {
	assertDate(t, AddMonths(date(2025, 1, 15), 12), date(2026, 1, 15))
}

func TestAddMonthsCrossingYear(t *testing.T) {
	assertDate(t, AddMonths(date(2025, 6, 10), 12), date(2026, 6, 10))
}

func TestAddMonthsPartial(t *testing.T) {
	assertDate(t, AddMonths(date(2025, 1, 31), 1), date(2025, 2, 28)) // troncato
}

func TestAddMonthsLeapYear(t *testing.T) {
	// 29/02/2024 (bisestile) + 12 mesi → 28/02/2025 (non bisestile)
	assertDate(t, AddMonths(date(2024, 2, 29), 12), date(2025, 2, 28))
}

func TestAddMonthsDecember(t *testing.T) {
	assertDate(t, AddMonths(date(2025, 12, 15), 1), date(2026, 1, 15))
}

// ========== compute_expiry_date ==========

func TestComputeExpiryDate(t *testing.T) {
	assertDate(t, ComputeExpiryDate(date(2025, 3, 15)), date(2026, 3, 15))
}

func TestComputeExpiryDateUsesFixedMonths(t *testing.T) {
	activation := date(2025, 1, 1)
	assertDate(t, ComputeExpiryDate(activation), AddMonths(activation, FixedOfferMonths))
}

// ========== format_date_display ==========

func TestFormatDateDisplayFromISO(t *testing.T) {
	assertString(t, FormatDateDisplay("2026-03-15"), "15/03/2026")
}

func TestFormatDateDisplayFromDate(t *testing.T) {
	assertString(t, FormatDateDisplay(date(2026, 3, 15)), "15/03/2026")
}

func TestFormatDateDisplayInvalid(t *testing.T) {
	assertString(t, FormatDateDisplay("not-a-date"), "")
}

func TestFormatDateDisplayNone(t *testing.T) {
	assertString(t, FormatDateDisplay(nil), "")
}

// ========== days_until ==========

func TestDaysUntilFuture(t *testing.T) {
	assertDays(t, "2025-01-31", date(2025, 1, 1), 30)
}

func TestDaysUntilPast(t *testing.T) {
	assertDays(t, "2025-01-01", date(2025, 2, 1), -31)
}

func TestDaysUntilSameDay(t *testing.T) {
	assertDays(t, "2025-01-01", date(2025, 1, 1), 0)
}

func TestDaysUntilInvalid(t *testing.T) {
	if got, ok := DaysUntil("not-a-date", nil); ok {
		t.Fatalf("DaysUntil(not-a-date) = %d, want None", got)
	}
}

// ========== Test aggiuntivi ==========

func TestConstants(t *testing.T) {
	if FixedOfferMonths != 12 || ReminderDaysBefore != 30 || minYear != 2015 {
		t.Fatal("costanti diverse da date_utils.py")
	}
}

func TestTodayUsesNow(t *testing.T) {
	orig := Now
	t.Cleanup(func() { Now = orig })
	loc := time.FixedZone("CEST", 2*3600)
	Now = func() time.Time { return time.Date(2026, 10, 16, 23, 30, 0, 0, loc) }
	assertDate(t, Today(), date(2026, 10, 16))
	if Today().Location() != time.UTC || Today().Hour() != 0 {
		t.Error("Today deve restituire la mezzanotte UTC")
	}

	// days_until senza today usa date.today()
	got, ok := DaysUntil("2026-10-26", nil)
	if !ok || got != 10 {
		t.Errorf("DaysUntil senza today = (%d, %v), want 10", got, ok)
	}

	// parse_activation_date usa l'anno corrente: 2027 ok, 2028 no
	assertParsed(t, "31/12/2027", date(2027, 12, 31))
	assertNotParsed(t, "01/01/2028")
}

func TestDaysUntilNormalizesToday(t *testing.T) {
	loc := time.FixedZone("X", -5*3600)
	today := time.Date(2025, 1, 1, 22, 0, 0, 0, loc) // in UTC sarebbe già il 2/1
	assertDays(t, "2025-01-31", today, 30)
	// Differenze oltre i ~292 anni di time.Duration
	assertDays(t, "9999-12-31", date(1, 1, 1), 3652058)
	assertDays(t, "0001-01-01", date(9999, 12, 31), -3652058)
}

func TestFormatDateDisplayPointersAndOtherTypes(t *testing.T) {
	d := date(2026, 3, 15)
	s := "2026-03-15"
	var nilTime *time.Time
	var nilString *string
	assertString(t, FormatDateDisplay(&d), "15/03/2026")
	assertString(t, FormatDateDisplay(&s), "15/03/2026")
	assertString(t, FormatDateDisplay(nilTime), "")
	assertString(t, FormatDateDisplay(nilString), "")
	assertString(t, FormatDateDisplay(20260315), "")
	assertString(t, FormatDateDisplay(date(5, 1, 1)), "01/01/5")
}

func TestFormatISODate(t *testing.T) {
	assertString(t, FormatISODate(date(2026, 3, 5)), "2026-03-05")
	assertString(t, FormatISODate(date(5, 1, 1)), "0005-01-01")
}

// ========== Golden test (output generati dalla versione Python) ==========

func loadGolden(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("lettura golden: %v", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("parsing golden %s: %v", name, err)
	}
}

func parseISO(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("data golden non valida %q: %v", s, err)
	}
	return d
}

func TestParseActivationDateGolden(t *testing.T) {
	var g struct {
		Todays [][]string  `json:"todays"`
		Cases  [][]*string `json:"cases"`
	}
	loadGolden(t, "parse_activation_date_golden.json", &g)
	if len(g.Cases) < 500 {
		t.Fatalf("golden troppo piccolo: %d", len(g.Cases))
	}

	accepted := 0
	for _, row := range g.Cases {
		for i, ts := range g.Todays[0] {
			if checkActivationDate(t, *row[0], ts, row[i+1]) {
				accepted++
			}
		}
	}
	if accepted < 100 {
		t.Errorf("troppi pochi casi validi nel golden: %d", accepted)
	}
}

// checkActivationDate confronta ParseActivationDateAt con il risultato
// Python (want nil = None) e restituisce se la data è stata accettata.
func checkActivationDate(t *testing.T, input, today string, want *string) bool {
	t.Helper()
	parsed, ok := ParseActivationDateAt(input, parseISO(t, today))
	got, exp := "None", "None"
	if ok {
		got = FormatISODate(parsed)
	}
	if want != nil {
		exp = *want
	}
	if got != exp {
		t.Errorf("ParseActivationDateAt(%q, %s) = %s, want %s", input, today, got, exp)
	}
	return ok
}

type datesGolden struct {
	AddMonths             [][3]any    `json:"add_months"`
	ComputeExpiryDate     [][2]string `json:"compute_expiry_date"`
	FormatDateDisplay     [][2]string `json:"format_date_display"`
	FormatDateDisplayNone []string    `json:"format_date_display_none"`
	FormatDateDisplayDate [][4]any    `json:"format_date_display_date"`
	DaysUntil             [][3]any    `json:"days_until"`
}

func loadDatesGolden(t *testing.T) datesGolden {
	t.Helper()
	var g datesGolden
	loadGolden(t, "dates_golden.json", &g)
	return g
}

func TestAddMonthsGolden(t *testing.T) {
	g := loadDatesGolden(t)
	for _, row := range g.AddMonths {
		months := int(row[1].(float64))
		if got := FormatISODate(AddMonths(parseISO(t, row[0].(string)), months)); got != row[2] {
			t.Errorf("AddMonths(%s, %d) = %q, want %q", row[0], months, got, row[2])
		}
	}
	for _, row := range g.ComputeExpiryDate {
		if got := FormatISODate(ComputeExpiryDate(parseISO(t, row[0]))); got != row[1] {
			t.Errorf("ComputeExpiryDate(%s) = %s, want %s", row[0], got, row[1])
		}
	}
}

func TestFormatDateDisplayGolden(t *testing.T) {
	g := loadDatesGolden(t)
	valid := 0
	for _, row := range g.FormatDateDisplay {
		got := FormatDateDisplay(row[0])
		if got != row[1] {
			t.Errorf("FormatDateDisplay(%q) = %q, want %q", row[0], got, row[1])
		}
		if got != "" {
			valid++
		}
	}
	if valid < 100 {
		t.Errorf("troppi pochi ISO validi nel golden: %d", valid)
	}
	if got := FormatDateDisplay(nil); got != g.FormatDateDisplayNone[0] {
		t.Errorf("FormatDateDisplay(nil) = %q, want %q", got, g.FormatDateDisplayNone[0])
	}
	for _, row := range g.FormatDateDisplayDate {
		d := date(int(row[0].(float64)), time.Month(int(row[1].(float64))), int(row[2].(float64)))
		if got := FormatDateDisplay(d); got != row[3].(string) {
			t.Errorf("FormatDateDisplay(%v) = %q, want %q", row[:3], got, row[3])
		}
	}
}

func TestDaysUntilGolden(t *testing.T) {
	for _, row := range loadDatesGolden(t).DaysUntil {
		iso := row[0].(string)
		today := parseISO(t, row[1].(string))
		got, ok := DaysUntil(iso, &today)
		want, wantOK := row[2].(float64)
		if ok != wantOK || (ok && got != int(want)) {
			t.Errorf("DaysUntil(%q, %s) = (%d, %v), want %v", iso, row[1], got, ok, row[2])
		}
	}
}

// ========== helper ==========

func assertDate(t *testing.T, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", FormatISODate(got), FormatISODate(want))
	}
}

func assertString(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func assertDays(t *testing.T, iso string, today time.Time, want int) {
	t.Helper()
	got, ok := DaysUntil(iso, &today)
	if !ok || got != want {
		t.Fatalf("DaysUntil(%q, %v) = (%d, %v), want %d", iso, today, got, ok, want)
	}
}
