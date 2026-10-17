// Package dateutil è il port di date_utils.py: utility per la gestione delle
// date delle offerte a prezzo fisso.
//
// Responsabilità:
//   - parsing della data di attivazione inserita dall'utente (vari formati italiani)
//   - calcolo della data di scadenza (attivazione + 12 mesi)
//   - formattazione date ISO ↔ display (DD/MM/YYYY)
//   - calcolo dei giorni mancanti a una scadenza
//
// Rappresentazione delle date: un datetime.date di Python è rappresentato da
// un time.Time a mezzanotte UTC (vedi NewDate). Le funzioni che ricevono un
// time.Time ne usano solo anno, mese e giorno nella sua location.
package dateutil

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// FixedOfferMonths è la durata standard di un'offerta a prezzo fisso Octopus
// Energy (mesi).
const FixedOfferMonths = 12

// ReminderDaysBefore è l'anticipo con cui inviare il reminder prima della
// scadenza (giorni).
const ReminderDaysBefore = 30

// minYear è l'inizio del range plausibile per la data di attivazione
// (protezione da typo evidenti), _MIN_YEAR in Python.
const minYear = 2015

// Limiti di datetime.date (MINYEAR / MAXYEAR).
const (
	pyMinYear = 1
	pyMaxYear = 9999
)

// acceptedFormats replica _ACCEPTED_FORMATS ("%d/%m/%Y", "%d-%m-%Y",
// "%d.%m.%Y") con le espressioni regolari che _strptime di Python genera per
// %d, %m e %Y. \d in Python (pattern str) accetta qualsiasi cifra Unicode
// (categoria Nd), quindi qui si usa \p{Nd}.
var acceptedFormats = []*regexp.Regexp{
	strptimeDMY("/"),
	strptimeDMY("-"),
	strptimeDMY("."),
}

func strptimeDMY(sep string) *regexp.Regexp {
	const (
		d = `(3[0-1]|[1-2]\p{Nd}|0[1-9]|[1-9]| [1-9])`
		m = `(1[0-2]|0[1-9]|[1-9])`
		y = `(\p{Nd}\p{Nd}\p{Nd}\p{Nd})`
	)
	s := regexp.QuoteMeta(sep)
	return regexp.MustCompile("^" + d + s + m + s + y + "$")
}

// Now è l'orologio usato da Today (sostituibile nei test).
var Now = time.Now

// NewDate costruisce la data indicata come time.Time a mezzanotte UTC.
func NewDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// Today restituisce la data odierna nel fuso locale (date.today() di Python)
// come time.Time a mezzanotte UTC.
func Today() time.Time {
	return dateOf(Now())
}

// dateOf restituisce la data (anno, mese, giorno nella location di t) a
// mezzanotte UTC.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return NewDate(y, m, d)
}

// ParseActivationDate converte il testo inserito dall'utente in una data.
//
// Accetta i formati DD/MM/YYYY, DD-MM-YYYY, DD.MM.YYYY. Restituisce la data e
// true se valida e plausibile, false altrimenti (None in Python).
func ParseActivationDate(text string) (time.Time, bool) {
	return ParseActivationDateAt(text, Today())
}

// ParseActivationDateAt è come ParseActivationDate ma con la data odierna
// esplicita (usata per il controllo "non troppo nel futuro").
func ParseActivationDateAt(text string, today time.Time) (time.Time, bool) {
	if text == "" {
		return time.Time{}, false
	}

	cleaned := pyStrip(text)

	for _, re := range acceptedFormats {
		parsed, ok := strptimeDate(re, cleaned)
		if !ok {
			continue
		}

		// Sanity check: anno plausibile e non troppo nel futuro
		if parsed.Year() < minYear {
			return time.Time{}, false
		}
		if parsed.Year() > today.Year()+1 {
			return time.Time{}, false
		}

		return parsed, true
	}

	return time.Time{}, false
}

// strptimeDate replica datetime.strptime(s, fmt).date() per i formati
// accettati: false equivale al ValueError di Python.
func strptimeDate(re *regexp.Regexp, s string) (time.Time, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	day := pyInt(m[1])
	month := pyInt(m[2])
	year := pyInt(m[3])
	if !validDate(year, month, day) {
		return time.Time{}, false
	}
	return NewDate(year, time.Month(month), day), true
}

// pyInt replica int() di Python su una stringa di cifre Unicode (Nd),
// eventualmente preceduta da spazi (il caso " 5" di %d).
func pyInt(s string) int {
	n := 0
	for _, r := range strings.TrimLeft(s, " ") {
		n = n*10 + digitValue(r)
	}
	return n
}

// digitValue restituisce il valore di una cifra Unicode (categoria Nd).
// Unicode garantisce che le cifre decimali siano assegnate in blocchi
// contigui da 0 a 9, quindi il valore è la posizione nel blocco modulo 10.
func digitValue(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	n := 0
	for unicode.IsDigit(r - rune(n) - 1) {
		n++
	}
	return n % 10
}

// pyIsSpace replica str.isspace() di Python per un singolo carattere.
// Rispetto a unicode.IsSpace include anche i separatori U+001C..U+001F.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// pyStrip replica str.strip() di Python senza argomenti.
func pyStrip(s string) string {
	return strings.TrimFunc(s, pyIsSpace)
}

// isLeap indica se l'anno è bisestile (calendario gregoriano prolettico).
func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// daysInMonth restituisce i giorni del mese.
func daysInMonth(year, month int) int {
	switch month {
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

// validDate replica i controlli del costruttore datetime.date.
func validDate(year, month, day int) bool {
	if year < pyMinYear || year > pyMaxYear {
		return false
	}
	if month < 1 || month > 12 {
		return false
	}
	return day >= 1 && day <= daysInMonth(year, month)
}

// AddMonths aggiunge un numero di mesi a una data, gestendo il caso 29
// febbraio: il giorno viene troncato all'ultimo giorno valido del mese
// target (es. 29/02 → 28/02 in anni non bisestili).
//
// Come in Python (dove date() solleva ValueError), va in panic se l'anno
// risultante è fuori dal range 1..9999.
func AddMonths(start time.Time, months int) time.Time {
	sy, sm, sd := start.Date()
	monthIndex := int(sm) - 1 + months
	year := sy + floorDiv(monthIndex, 12)
	month := floorMod(monthIndex, 12) + 1

	if year < pyMinYear || year > pyMaxYear {
		panic(fmt.Sprintf("ValueError: year %d is out of range", year))
	}

	// Giorni nel mese target (gestione mesi corti e anni bisestili)
	day := min(sd, daysInMonth(year, month))
	return NewDate(year, time.Month(month), day)
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func floorMod(a, b int) int {
	return a - floorDiv(a, b)*b
}

// ComputeExpiryDate calcola la data di scadenza di un'offerta fissa
// (attivazione + 12 mesi).
func ComputeExpiryDate(activation time.Time) time.Time {
	return AddMonths(activation, FixedOfferMonths)
}

// FormatDateDisplay formatta una data ISO (YYYY-MM-DD) o una data in
// DD/MM/YYYY.
//
// Accetta, come la versione Python (str | date): string, time.Time e i
// rispettivi puntatori. Restituisce la stringa vuota se la stringa non è una
// data ISO valida o se il valore è nil / di altro tipo.
func FormatDateDisplay(isoDate any) string {
	switch v := isoDate.(type) {
	case time.Time:
		return strftimeDMY(v)
	case *time.Time:
		if v == nil {
			return ""
		}
		return strftimeDMY(*v)
	case string:
		return formatISOString(v)
	case *string:
		if v == nil {
			return ""
		}
		return formatISOString(*v)
	default:
		return ""
	}
}

func formatISOString(s string) string {
	parsed, ok := ParseISODate(s)
	if !ok {
		return ""
	}
	return strftimeDMY(parsed)
}

// strftimeDMY replica date.strftime("%d/%m/%Y") su Linux (glibc), dove %Y
// non aggiunge zeri per anni < 1000.
func strftimeDMY(t time.Time) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%02d/%02d/%d", d, int(m), y)
}

// FormatISODate replica date.isoformat(): "YYYY-MM-DD".
func FormatISODate(t time.Time) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}

// DaysUntil calcola i giorni mancanti alla data indicata (formato ISO
// YYYY-MM-DD). today è la data odierna: se è nil viene usato Today()
// (date.today() in Python, utile per i test).
//
// Restituisce il numero di giorni (negativo se già passata) e true, oppure
// false se la data non è valida (None in Python).
func DaysUntil(isoDate string, today *time.Time) (int, bool) {
	target, ok := ParseISODate(isoDate)
	if !ok {
		return 0, false
	}

	reference := Today()
	if today != nil {
		reference = dateOf(*today)
	}
	// Differenza in giorni tra due mezzanotti UTC (Unix evita l'overflow di
	// time.Duration oltre i ~292 anni)
	return int((target.Unix() - reference.Unix()) / 86400), true
}

// ParseISODate replica date.fromisoformat() di CPython (implementazione C,
// Python ≥ 3.11). Accetta stringhe di 7, 8 o 10 byte nei formati
// YYYY-MM-DD, YYYYMMDD, YYYY-Www, YYYYWww, YYYY-Www-D e YYYYWwwD; come
// CPython non verifica eventuali caratteri finali dopo la data (es.
// "20260315xy" è accettata). Restituisce false dove Python solleva ValueError.
func ParseISODate(s string) (time.Time, bool) {
	if len(s) != 7 && len(s) != 8 && len(s) != 10 {
		return time.Time{}, false
	}
	year, month, day, ok := parseISOFormatDate(s)
	if !ok || !validDate(year, month, day) {
		return time.Time{}, false
	}
	return NewDate(year, time.Month(month), day), true
}

// isoReader legge i byte di una stringa come un puntatore C: oltre la fine
// restituisce il terminatore '\0'.
type isoReader struct {
	s   string
	pos int
}

func (r *isoReader) peek() byte {
	if r.pos < len(r.s) {
		return r.s[r.pos]
	}
	return 0
}

func (r *isoReader) next() byte {
	c := r.peek()
	r.pos++
	return c
}

// digits replica parse_digits di _datetimemodule.c (solo cifre ASCII).
func (r *isoReader) digits(n int) (int, bool) {
	v := 0
	for i := 0; i < n; i++ {
		c := r.next()
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	return v, true
}

// parseISOFormatDate replica parse_isoformat_date di _datetimemodule.c.
func parseISOFormatDate(s string) (year, month, day int, ok bool) {
	r := &isoReader{s: s}
	if year, ok = r.digits(4); !ok {
		return 0, 0, 0, false
	}

	usesSeparator := r.peek() == '-'
	if usesSeparator {
		r.pos++
	}

	if r.peek() == 'W' {
		// Data in formato settimana ISO (isocalendar)
		r.pos++
		isoWeek, ok := r.digits(2)
		if !ok {
			return 0, 0, 0, false
		}
		isoDay := 1
		if r.pos < len(s) {
			if usesSeparator && r.next() != '-' {
				return 0, 0, 0, false
			}
			if isoDay, ok = r.digits(1); !ok {
				return 0, 0, 0, false
			}
		}
		return isoToYMD(year, isoWeek, isoDay)
	}

	if month, ok = r.digits(2); !ok {
		return 0, 0, 0, false
	}
	if usesSeparator && r.next() != '-' {
		return 0, 0, 0, false
	}
	if day, ok = r.digits(2); !ok {
		return 0, 0, 0, false
	}
	return year, month, day, true
}

// isoToYMD replica iso_to_ymd di _datetimemodule.c.
func isoToYMD(isoYear, isoWeek, isoDay int) (year, month, day int, ok bool) {
	if isoYear < pyMinYear || isoYear > pyMaxYear {
		return 0, 0, 0, false
	}
	jan1 := NewDate(isoYear, time.January, 1)
	// 0 se l'1/1 è lunedì, 1 se martedì, ecc.
	firstWeekday := (int(jan1.Weekday()) + 6) % 7
	if isoWeek <= 0 || isoWeek >= 53 {
		outOfRange := true
		if isoWeek == 53 {
			// Gli anni ISO hanno 53 settimane se iniziano di giovedì, o di
			// mercoledì se bisestili
			if firstWeekday == 3 || (firstWeekday == 2 && isLeap(isoYear)) {
				outOfRange = false
			}
		}
		if outOfRange {
			return 0, 0, 0, false
		}
	}
	if isoDay <= 0 || isoDay >= 8 {
		return 0, 0, 0, false
	}

	// Lunedì della settimana 1 (iso_week1_monday)
	week1Monday := -firstWeekday
	if firstWeekday > 3 {
		week1Monday += 7
	}
	offset := week1Monday + (isoWeek-1)*7 + isoDay - 1
	y, m, d := jan1.AddDate(0, 0, offset).Date()
	return y, int(m), d, true
}
