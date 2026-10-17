package dateutil

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
	"unicode"
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

func TestAddMonthsPanicsOutOfRange(t *testing.T) {
	msg := panicMessage(func() { AddMonths(date(9999, 12, 31), 1) })
	if msg != "ValueError: year 10000 is out of range" {
		t.Fatalf("panic = %q", msg)
	}
}

func TestFloorDivMod(t *testing.T) {
	tests := []struct{ a, b, div, mod int }{
		{7, 12, 0, 7}, {-1, 12, -1, 11}, {-12, 12, -1, 0}, {-13, 12, -2, 11}, {24, 12, 2, 0},
		{7, -2, -4, -1},
	}
	for _, tt := range tests {
		if d, m := floorDiv(tt.a, tt.b), floorMod(tt.a, tt.b); d != tt.div || m != tt.mod {
			t.Errorf("floorDiv/Mod(%d, %d) = %d, %d; want %d, %d", tt.a, tt.b, d, m, tt.div, tt.mod)
		}
	}
}

// Le cifre Unicode (Nd) sono assegnate in blocchi contigui multipli di 10:
// è il presupposto di digitValue.
func TestDigitBlocksAligned(t *testing.T) {
	run := 0
	for r := rune(0); r <= unicode.MaxRune+1; r++ {
		if r <= unicode.MaxRune && unicode.IsDigit(r) {
			run++
			if got := digitValue(r); got != (run-1)%10 {
				t.Fatalf("digitValue(%U) = %d, want %d", r, got, (run-1)%10)
			}
			continue
		}
		if run%10 != 0 {
			t.Fatalf("blocco di cifre che termina in %U lungo %d", r-1, run)
		}
		run = 0
	}
	for r, want := range map[rune]int{'٥': 5, '२': 2, '０': 0, '９': 9, '7': 7} {
		if got := digitValue(r); got != want {
			t.Errorf("digitValue(%q) = %d, want %d", r, got, want)
		}
	}
}

func TestPyInt(t *testing.T) {
	if got := pyInt(" 5"); got != 5 {
		t.Errorf("pyInt(\" 5\") = %d", got)
	}
	if got := pyInt("٢٠٢٥"); got != 2025 {
		t.Errorf("pyInt(arabo) = %d", got)
	}
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
		Todays        [][]string  `json:"todays"`
		Cases         [][]*string `json:"cases"`
		PythonIsSpace [][]int     `json:"python_isspace"`
	}
	loadGolden(t, "parse_activation_date_golden.json", &g)
	todays := g.Todays[0]
	if len(g.Cases) < 500 {
		t.Fatalf("golden troppo piccolo: %d", len(g.Cases))
	}

	accepted := 0
	for _, row := range g.Cases {
		input := *row[0]
		for i, ts := range todays {
			today := parseISO(t, ts)
			got, ok := ParseActivationDateAt(input, today)
			want := row[i+1]
			switch {
			case want == nil && ok:
				t.Errorf("ParseActivationDateAt(%q, %s) = %s, want None", input, ts, FormatISODate(got))
			case want != nil && !ok:
				t.Errorf("ParseActivationDateAt(%q, %s) = None, want %s", input, ts, *want)
			case want != nil && FormatISODate(got) != *want:
				t.Errorf("ParseActivationDateAt(%q, %s) = %s, want %s", input, ts, FormatISODate(got), *want)
			}
			if ok {
				accepted++
			}
		}
	}
	if accepted < 100 {
		t.Errorf("troppi pochi casi validi nel golden: %d", accepted)
	}

	// str.isspace() di Python su tutto lo spazio Unicode
	pySpaces := map[rune]bool{}
	for _, c := range g.PythonIsSpace[0] {
		pySpaces[rune(c)] = true
	}
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if pyIsSpace(r) != pySpaces[r] {
			t.Errorf("pyIsSpace(%U) = %v, Python %v", r, pyIsSpace(r), pySpaces[r])
		}
	}
}

func TestDatesGolden(t *testing.T) {
	var g struct {
		AddMonths             [][3]any    `json:"add_months"`
		ComputeExpiryDate     [][2]string `json:"compute_expiry_date"`
		FormatDateDisplay     [][2]string `json:"format_date_display"`
		FormatDateDisplayNone []string    `json:"format_date_display_none"`
		FormatDateDisplayDate [][4]any    `json:"format_date_display_date"`
		DaysUntil             [][3]any    `json:"days_until"`
	}
	loadGolden(t, "dates_golden.json", &g)

	for _, row := range g.AddMonths {
		start := parseISO(t, row[0].(string))
		months := int(row[1].(float64))
		want := row[2].(string)
		var got string
		if msg := panicMessage(func() { got = FormatISODate(AddMonths(start, months)) }); msg != "" {
			got = msg
		}
		if got != want {
			t.Errorf("AddMonths(%s, %d) = %q, want %q", row[0], months, got, want)
		}
	}

	for _, row := range g.ComputeExpiryDate {
		if got := FormatISODate(ComputeExpiryDate(parseISO(t, row[0]))); got != row[1] {
			t.Errorf("ComputeExpiryDate(%s) = %s, want %s", row[0], got, row[1])
		}
	}

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

	for _, row := range g.DaysUntil {
		iso := row[0].(string)
		today := parseISO(t, row[1].(string))
		got, ok := DaysUntil(iso, &today)
		if row[2] == nil {
			if ok {
				t.Errorf("DaysUntil(%q, %s) = %d, want None", iso, row[1], got)
			}
			continue
		}
		if want := int(row[2].(float64)); !ok || got != want {
			t.Errorf("DaysUntil(%q, %s) = (%d, %v), want %d", iso, row[1], got, ok, want)
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

func panicMessage(fn func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	fn()
	return ""
}
