package feedback

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Port di datetime.fromisoformat di CPython 3.13 (implementazione C di
// Modules/_datetimemodule.c: _sanitize_isoformat_str,
// _find_isoformat_datetime_separator, parse_isoformat_date,
// parse_hh_mm_ss_ff, parse_isoformat_time, tzinfo_from_isoformat_results e
// i controlli di new_datetime / new_timezone), usato da _can_give_feedback
// per leggere users.last_feedback_at. Il parser C lavora sui byte UTF-8 di
// una stringa terminata da NUL: at() restituisce 0 oltre la fine, così gli
// accessi e le condizioni di arresto sono gli stessi.

// isoDatetime è il risultato di datetime.fromisoformat: data e ora
// (naive) e se il datetime ha un tzinfo (aware).
type isoDatetime struct {
	year, month, day                  int
	hour, minute, second, microsecond int
	aware                             bool
}

// pyError è un'eccezione Python: Error() restituisce str(e), come la
// interpola f"{e}" nel log di _can_give_feedback.
type pyError struct {
	typ string // nome della classe, es. "ValueError"
	msg string // str(e)
}

func (e *pyError) Error() string { return e.msg }

func valueError(msg string) error { return &pyError{typ: "ValueError", msg: msg} }

// isoParser contiene la stringa da analizzare (byte UTF-8).
type isoParser struct {
	s string
}

// at replica la lettura di dtstr[i] nel buffer C terminato da NUL.
func (p isoParser) at(i int) byte {
	if i >= 0 && i < len(p.s) {
		return p.s[i]
	}
	return 0
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

// parseDigits replica parse_digits: legge n cifre ASCII da i accumulandole
// in *v; restituisce la nuova posizione e false se trova una non cifra.
func (p isoParser) parseDigits(i int, v *int, n int) (int, bool) {
	for k := 0; k < n; k++ {
		c := p.at(i)
		i++
		if !isASCIIDigit(c) {
			return i, false
		}
		*v = *v*10 + int(c-'0')
	}
	return i, true
}

// findSeparator replica _find_isoformat_datetime_separator (n = lunghezza
// in byte); -1 corrisponde a una stringa non valida.
func (p isoParser) findSeparator(n int) int {
	const dateSeparator, weekIndicator = '-', 'W'
	if n == 7 {
		return 7
	}
	if p.at(4) == dateSeparator {
		// YYYY-???
		if p.at(5) == weekIndicator {
			// YYYY-W??
			if n < 8 {
				return -1
			}
			if n > 8 && p.at(8) == dateSeparator {
				// YYYY-Www-D (10) o YYYY-Www-HH (8)
				if n == 9 {
					return -1
				}
				if n > 10 && isASCIIDigit(p.at(10)) {
					return 8
				}
				return 10
			}
			// YYYY-Www (8)
			return 8
		}
		// YYYY-MM-DD (10)
		return 10
	}
	if p.at(4) == weekIndicator {
		// YYYYWww (7) o YYYYWwwd (8)
		idx := 7
		for ; idx < n; idx++ {
			if !isASCIIDigit(p.at(idx)) {
				break
			}
		}
		if idx < 9 {
			return idx
		}
		if idx%2 == 0 {
			return 7
		}
		return 8
	}
	// YYYYMMDD (8)
	return 8
}

// parseDate replica parse_isoformat_date sui primi length byte (che però,
// come in C, non limitano la lettura delle cifre). Restituisce 0 o un
// codice negativo.
func (p isoParser) parseDate(length int, year, month, day *int) int {
	i, ok := p.parseDigits(0, year, 4)
	if !ok {
		return -1
	}
	usesSeparator := p.at(i) == '-'
	if usesSeparator {
		i++
	}

	if p.at(i) == 'W' {
		// Data in stile isocalendar
		i++
		isoWeek, isoDay := 0, 0
		if i, ok = p.parseDigits(i, &isoWeek, 2); !ok {
			return -3
		}
		if i < length {
			if usesSeparator {
				c := p.at(i)
				i++
				if c != '-' {
					return -2
				}
			}
			if _, ok = p.parseDigits(i, &isoDay, 1); !ok {
				return -4
			}
		} else {
			isoDay = 1
		}
		if rv := isoToYMD(*year, isoWeek, isoDay, year, month, day); rv != 0 {
			return -3 + rv
		}
		return 0
	}

	if i, ok = p.parseDigits(i, month, 2); !ok {
		return -1
	}
	if usesSeparator {
		c := p.at(i)
		i++
		if c != '-' {
			return -2
		}
	}
	if _, ok = p.parseDigits(i, day, 2); !ok {
		return -1
	}
	return 0
}

// parseHHMMSSFF replica parse_hh_mm_ss_ff sull'intervallo [start, end):
// 0 = fine stringa, 1 = altro testo dopo l'orario, <0 = errore.
func (p isoParser) parseHHMMSSFF(start, end int, hour, minute, second, microsecond *int) int {
	*hour, *minute, *second, *microsecond = 0, 0, 0, 0
	i := start
	vals := [3]*int{hour, minute, second}
	hasSeparator := true

	// Parse [HH[:?MM[:?SS]]]
	for k := 0; k < 3; k++ {
		var ok bool
		if i, ok = p.parseDigits(i, vals[k], 2); !ok {
			return -3
		}
		c := p.at(i)
		i++
		if k == 0 {
			hasSeparator = c == ':'
		}

		if c == '.' || c == ',' {
			if i >= end {
				return -3 // separatore decimale senza cifre
			}
			break
		} else if i >= end {
			if c != 0 {
				return 1
			}
			return 0
		} else if hasSeparator && c == ':' {
			continue
		} else if !hasSeparator {
			i--
		} else {
			return -4 // separatore orario malformato
		}
	}

	// Parse fractional components
	toParse := end - i
	if toParse >= 6 {
		toParse = 6
	}
	var ok bool
	if i, ok = p.parseDigits(i, microsecond, toParse); !ok {
		return -3
	}
	correction := [5]int{100000, 10000, 1000, 100, 10}
	if toParse < 6 {
		*microsecond *= correction[toParse-1]
	}
	for isASCIIDigit(p.at(i)) {
		i++ // salta le cifre troncate
	}
	if p.at(i) != 0 {
		return 1
	}
	return 0
}

// parseTime replica parse_isoformat_time da start alla fine della stringa:
// 0 = senza fuso, 1 = con fuso (tzOffset secondi, tzMicro microsecondi),
// <0 = errore.
func (p isoParser) parseTime(start, end int, hour, minute, second, microsecond, tzOffset, tzMicro *int) int {
	tzPos := start
	for {
		if c := p.at(tzPos); c == 'Z' || c == '+' || c == '-' {
			break
		}
		tzPos++
		if tzPos >= end {
			break
		}
	}

	rv := p.parseHHMMSSFF(start, tzPos, hour, minute, second, microsecond)
	if rv < 0 {
		return rv
	} else if tzPos == end {
		// Nessun fuso: altro testo in coda è un errore
		if rv == 1 {
			return -5
		}
		return 0
	}

	// Caso speciale UTC / Zulu
	if p.at(tzPos) == 'Z' {
		*tzOffset, *tzMicro = 0, 0
		if p.at(tzPos+1) != 0 {
			return -5
		}
		return 1
	}

	tzSign := 1
	if p.at(tzPos) == '-' {
		tzSign = -1
	}
	tzPos++
	var tzHour, tzMinute, tzSecond int
	rv = p.parseHHMMSSFF(tzPos, end, &tzHour, &tzMinute, &tzSecond, tzMicro)
	*tzOffset = tzSign * (tzHour*3600 + tzMinute*60 + tzSecond)
	*tzMicro *= tzSign
	if rv != 0 {
		return -5
	}
	return 1
}

// fromISOFormat replica datetime.fromisoformat(s). Gli errori sono
// ValueError con il messaggio di CPython.
func fromISOFormat(s string) (isoDatetime, error) {
	invalid := func() (isoDatetime, error) {
		return isoDatetime{}, valueError("Invalid isoformat string: " + pyStrRepr(s))
	}
	// Tutte le stringhe ISO 8601 valide hanno almeno 7 caratteri (code point)
	if utf8.RuneCountInString(s) < 7 || !utf8.ValidString(s) {
		return invalid()
	}

	p := isoParser{s: s}
	n := len(s)
	sep := p.findSeparator(n)
	if sep < 0 {
		return invalid()
	}

	var dt isoDatetime
	var tzOffset, tzMicro int
	rv := p.parseDate(sep, &dt.year, &dt.month, &dt.day)
	if rv == 0 && n > sep {
		// In UTF-8 la lunghezza del carattere separatore è nel primo byte
		i := sep
		switch c := p.at(i); {
		case c&0x80 == 0:
			i++
		case c&0xf0 == 0xe0:
			i += 3
		case c&0xf0 == 0xf0:
			i += 4
		default:
			i += 2
		}
		if i > n {
			return invalid()
		}
		rv = p.parseTime(i, n, &dt.hour, &dt.minute, &dt.second, &dt.microsecond, &tzOffset, &tzMicro)
	}
	if rv < 0 {
		return invalid()
	}

	// tzinfo_from_isoformat_results + new_timezone
	if rv == 1 {
		dt.aware = true
		if days, secs, micros := normalizeDelta(0, tzOffset, tzMicro); (days != 0 || secs != 0 || micros != 0) &&
			((days == -1 && secs == 0 && micros < 1) || days < -1 || days >= 1) {
			return isoDatetime{}, valueError("offset must be a timedelta strictly between -timedelta(hours=24) and " +
				"timedelta(hours=24), not " + deltaRepr(days, secs, micros) + ".")
		}
	}

	// new_datetime: check_date_args e check_time_args
	if err := checkDateArgs(dt.year, dt.month, dt.day); err != nil {
		return isoDatetime{}, err
	}
	if err := checkTimeArgs(dt.hour, dt.minute, dt.second, dt.microsecond); err != nil {
		return isoDatetime{}, err
	}
	return dt, nil
}

// ========== Calendario (proleptico gregoriano, come datetime) ==========

const (
	minYear = 1
	maxYear = 9999
)

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

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

// daysBeforeYear è il numero di giorni prima del 1° gennaio di year
// (days_before_year).
func daysBeforeYear(year int) int {
	y := year - 1
	return y*365 + y/4 - y/100 + y/400
}

// ymdToOrd replica ymd_to_ord (ordinale proleptico, 0001-01-01 = 1).
func ymdToOrd(year, month, day int) int {
	days := daysBeforeYear(year)
	for m := 1; m < month; m++ {
		days += daysInMonth(year, m)
	}
	return days + day
}

// ordToYMD replica ord_to_ymd.
func ordToYMD(ordinal int) (year, month, day int) {
	// Cerca l'anno partendo da una stima e correggendo
	year = ordinal/366 + 1
	for daysBeforeYear(year+1) < ordinal {
		year++
	}
	rest := ordinal - daysBeforeYear(year)
	month = 1
	for rest > daysInMonth(year, month) {
		rest -= daysInMonth(year, month)
		month++
	}
	return year, month, rest
}

// weekday replica weekday(y, m, d) di CPython (lunedì = 0).
func weekday(year, month, day int) int {
	return (ymdToOrd(year, month, day) + 6) % 7
}

// isoWeek1Monday replica iso_week1_monday.
func isoWeek1Monday(year int) int {
	firstDay := ymdToOrd(year, 1, 1)
	firstWeekday := (firstDay + 6) % 7
	week1Monday := firstDay - firstWeekday
	if firstWeekday > 3 {
		week1Monday += 7
	}
	return week1Monday
}

// isoToYMD replica iso_to_ymd: 0 se valida, altrimenti -2/-3/-4.
func isoToYMD(isoYear, isoWeek, isoDay int, year, month, day *int) int {
	if isoYear < minYear || isoYear > maxYear {
		return -4
	}
	if isoWeek <= 0 || isoWeek >= 53 {
		outOfRange := true
		if isoWeek == 53 {
			// Gli anni ISO hanno 53 settimane se iniziano di giovedì o, se
			// bisestili, di mercoledì
			firstWeekday := weekday(isoYear, 1, 1)
			if firstWeekday == 3 || (firstWeekday == 2 && isLeap(isoYear)) {
				outOfRange = false
			}
		}
		if outOfRange {
			return -2
		}
	}
	if isoDay <= 0 || isoDay >= 8 {
		return -3
	}
	day1 := isoWeek1Monday(isoYear)
	dayOffset := (isoWeek-1)*7 + isoDay - 1
	*year, *month, *day = ordToYMD(day1 + dayOffset)
	return 0
}

func checkDateArgs(year, month, day int) error {
	if year < minYear || year > maxYear {
		return valueError(fmt.Sprintf("year %d is out of range", year))
	}
	if month < 1 || month > 12 {
		return valueError("month must be in 1..12")
	}
	if day < 1 || day > daysInMonth(year, month) {
		return valueError("day is out of range for month")
	}
	return nil
}

func checkTimeArgs(h, m, s, us int) error {
	switch {
	case h < 0 || h > 23:
		return valueError("hour must be in 0..23")
	case m < 0 || m > 59:
		return valueError("minute must be in 0..59")
	case s < 0 || s > 59:
		return valueError("second must be in 0..59")
	case us < 0 || us > 999999:
		return valueError("microsecond must be in 0..999999")
	}
	return nil
}

// floorDivMod restituisce divisione e resto con arrotondamento verso -inf
// (divmod di CPython).
func floorDivMod(a, b int) (q, r int) {
	q, r = a/b, a%b
	if r < 0 {
		q--
		r += b
	}
	return q, r
}

// normalizeDelta replica normalize_d_s_us (giorni, secondi, microsecondi).
func normalizeDelta(d, s, us int) (int, int, int) {
	if us < 0 || us >= 1000000 {
		var q int
		q, us = floorDivMod(us, 1000000)
		s += q
	}
	if s < 0 || s >= 24*3600 {
		var q int
		q, s = floorDivMod(s, 24*3600)
		d += q
	}
	return d, s, us
}

// deltaRepr replica repr(timedelta) (delta_repr).
func deltaRepr(days, secs, micros int) string {
	var parts []string
	if days != 0 {
		parts = append(parts, "days="+strconv.Itoa(days))
	}
	if secs != 0 {
		parts = append(parts, "seconds="+strconv.Itoa(secs))
	}
	if micros != 0 {
		parts = append(parts, "microseconds="+strconv.Itoa(micros))
	}
	if len(parts) == 0 {
		parts = []string{"0"}
	}
	return "datetime.timedelta(" + strings.Join(parts, ", ") + ")"
}

// pyStrRepr replica repr(str) di Python (per i messaggi di ValueError).
func pyStrRepr(s string) string {
	quote := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var sb strings.Builder
	sb.WriteRune(quote)
	for _, r := range s {
		switch {
		case r == quote || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case unicode.IsPrint(r): // stessi caratteri di str.isprintable()
			sb.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			fmt.Fprintf(&sb, `\U%08x`, r)
		}
	}
	sb.WriteRune(quote)
	return sb.String()
}
