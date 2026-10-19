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
	"strings"
	"time"
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

// acceptedLayouts sono i formati accettati per la data di attivazione
// (_ACCEPTED_FORMATS "%d/%m/%Y", "%d-%m-%Y", "%d.%m.%Y"): giorno e mese con
// una o due cifre, anno con quattro.
var acceptedLayouts = []string{"2/1/2006", "2-1-2006", "2.1.2006"}

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
	cleaned := strings.TrimSpace(text)

	for _, layout := range acceptedLayouts {
		parsed, err := time.Parse(layout, cleaned)
		if err != nil {
			continue
		}

		// Sanity check: anno plausibile e non troppo nel futuro
		if parsed.Year() < minYear || parsed.Year() > today.Year()+1 {
			return time.Time{}, false
		}
		return parsed, true
	}

	return time.Time{}, false
}

// daysInMonth restituisce i giorni del mese.
func daysInMonth(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// validDate replica i controlli del costruttore datetime.date.
func validDate(year, month, day int) bool {
	return year >= pyMinYear && year <= pyMaxYear &&
		month >= 1 && month <= 12 &&
		day >= 1 && day <= daysInMonth(year, month)
}

// AddMonths aggiunge un numero di mesi a una data, gestendo il caso 29
// febbraio: il giorno viene troncato all'ultimo giorno valido del mese
// target (es. 29/02 → 28/02 in anni non bisestili).
func AddMonths(start time.Time, months int) time.Time {
	y, m, d := start.Date()
	// time.Date normalizza mesi fuori range (anche negativi) sull'anno
	first := NewDate(y, m+time.Month(months), 1)
	day := min(d, daysInMonth(first.Year(), int(first.Month())))
	return NewDate(first.Year(), first.Month(), day)
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

// ParseISODate replica date.fromisoformat() di CPython (Python ≥ 3.11).
// Accetta stringhe di 7, 8 o 10 byte nei formati YYYY-MM-DD, YYYYMMDD,
// YYYY-Www, YYYYWww, YYYY-Www-D e YYYYWwwD; come CPython non verifica
// eventuali caratteri finali dopo la data (es. "20260315xy" è accettata).
// Restituisce false dove Python solleva ValueError.
func ParseISODate(s string) (time.Time, bool) {
	if len(s) != 7 && len(s) != 8 && len(s) != 10 {
		return time.Time{}, false
	}
	year, ok := digitsAt(s, 0, 4)
	if !ok {
		return time.Time{}, false
	}
	p := 4
	sep := s[p] == '-'
	if sep {
		p++
	}

	var date time.Time
	if s[p] == 'W' {
		date, ok = parseISOWeek(s, p+1, year, sep)
	} else {
		date, ok = parseISOMonthDay(s, p, year, sep)
	}
	if !ok || date.Year() < pyMinYear || date.Year() > pyMaxYear {
		return time.Time{}, false
	}
	return date, true
}

// digitsAt legge n cifre ASCII da s[i:]; false se mancano o non sono cifre.
func digitsAt(s string, i, n int) (int, bool) {
	if i+n > len(s) {
		return 0, false
	}
	v := 0
	for _, c := range []byte(s[i : i+n]) {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	return v, true
}

// parseISOMonthDay legge MM-DD (sep) o MMDD a partire da s[p].
func parseISOMonthDay(s string, p, year int, sep bool) (time.Time, bool) {
	month, ok := digitsAt(s, p, 2)
	p += 2
	if !ok || (sep && (p >= len(s) || s[p] != '-')) {
		return time.Time{}, false
	}
	if sep {
		p++
	}
	day, ok := digitsAt(s, p, 2)
	if !ok || !validDate(year, month, day) {
		return time.Time{}, false
	}
	return NewDate(year, time.Month(month), day), true
}

// parseISOWeek legge ww, ww-D (sep) o wwD a partire da s[p] e converte la
// settimana ISO in data (iso_to_ymd). Senza giorno vale il lunedì.
func parseISOWeek(s string, p, year int, sep bool) (time.Time, bool) {
	week, ok := digitsAt(s, p, 2)
	if !ok {
		return time.Time{}, false
	}
	p += 2
	day := 1
	if p < len(s) {
		if sep {
			if s[p] != '-' {
				return time.Time{}, false
			}
			p++
		}
		if day, ok = digitsAt(s, p, 1); !ok {
			return time.Time{}, false
		}
	}
	return isoWeekDate(year, week, day)
}

// isoWeekDate restituisce il giorno day (1 = lunedì) della settimana ISO
// week dell'anno ISO year.
func isoWeekDate(year, week, day int) (time.Time, bool) {
	if year < pyMinYear || year > pyMaxYear || day < 1 || day > 7 {
		return time.Time{}, false
	}
	// Il 28 dicembre cade sempre nell'ultima settimana ISO dell'anno
	if _, weeks := NewDate(year, time.December, 28).ISOWeek(); week < 1 || week > weeks {
		return time.Time{}, false
	}
	// La settimana 1 è quella che contiene il 4 gennaio
	jan4 := NewDate(year, time.January, 4)
	monday := jan4.AddDate(0, 0, -((int(jan4.Weekday()) + 6) % 7))
	return monday.AddDate(0, 0, (week-1)*7+day-1), true
}
