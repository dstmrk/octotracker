package app

import (
	"strings"
	"time"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
)

// Port di email.utils.parsedate_to_datetime (CPython 3.13,
// email/_parseaddr.py), usato da StaticFileHandler.should_return_304 per
// l'header If-Modified-Since. Il parser di Python è molto più tollerante di
// http.ParseTime: accetta date senza giorno della settimana, anni a due
// cifre, fusi numerici o con nome (EST, PDT, ...), il formato asctime e
// separatori "." nell'ora.

// pyMonthNames è _monthnames.
var pyMonthNames = []string{
	"jan", "feb", "mar", "apr", "may", "jun", "jul",
	"aug", "sep", "oct", "nov", "dec",
	"january", "february", "march", "april", "may", "june", "july",
	"august", "september", "october", "november", "december",
}

// pyDayNames è _daynames.
var pyDayNames = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// pyTimezones è _timezones (offset in formato ±HHMM).
var pyTimezones = map[string]int64{
	"UT": 0, "UTC": 0, "GMT": 0, "Z": 0,
	"AST": -400, "ADT": -300,
	"EST": -500, "EDT": -400,
	"CST": -600, "CDT": -500,
	"MST": -700, "MDT": -600,
	"PST": -800, "PDT": -700,
}

// pySplit replica str.split() senza argomenti.
func pySplit(s string) []string {
	return strings.FieldsFunc(s, pycompat.IsSpace)
}

// pyIsDigitLatin1 replica str.isdigit() sul primo carattere di una stringa
// decodificata latin-1 (le uniche cifre sono 0-9 e gli apici ¹ ² ³).
func pyIsDigitLatin1(s string) bool {
	for _, r := range s {
		return r >= '0' && r <= '9' || r == '¹' || r == '²' || r == '³'
	}
	return false
}

// pyInt64 replica int(s) di Python: ok=false per un ValueError; big=true se
// il valore non sta in un int64 (in Python poi datetime solleverebbe
// OverflowError).
func pyInt64(s string) (v int64, big, ok bool) {
	n, err := pycompat.ParseInt(s)
	if err != nil {
		return 0, false, false
	}
	if !n.IsInt64() {
		return 0, true, true
	}
	return n.Int64(), false, true
}

// pyIndex replica list.index per una lista di stringhe (-1 se assente).
func pyIndex(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// parsedateTZ replica _parsedate_tz: restituisce anno, mese, giorno, ore,
// minuti e secondi e l'offset del fuso in secondi (nil per una data senza
// fuso o con -0000). ok=false quando Python restituisce None o solleva
// un'eccezione (IndexError per alcune stringhe malformate).
func parsedateTZ(data string) (fields [6]int64, tzoffset *int64, ok bool) {
	if data == "" {
		return fields, nil, false
	}
	parts := pySplit(data)
	if len(parts) == 0 {
		return fields, nil, false
	}
	// La FWS dopo la virgola del giorno della settimana è facoltativa
	if strings.HasSuffix(parts[0], ",") || pyIndex(pyDayNames, strings.ToLower(parts[0])) >= 0 {
		parts = parts[1:]
	} else if i := strings.LastIndex(parts[0], ","); i >= 0 {
		parts[0] = parts[0][i+1:]
	}
	if len(parts) == 3 { // data RFC 850, deprecata
		if stuff := strings.Split(parts[0], "-"); len(stuff) == 3 {
			parts = append(stuff, parts[1:]...)
		}
	}
	if len(parts) == 4 {
		s := parts[3]
		i := strings.Index(s, "+")
		if i == -1 {
			i = strings.Index(s, "-")
		}
		if i > 0 {
			parts = append(parts[:3:3], s[:i], s[i:])
		} else {
			parts = append(parts, "") // fuso fittizio
		}
	}
	if len(parts) < 5 {
		return fields, nil, false
	}
	dd, mm, yy, tm, tz := parts[0], parts[1], parts[2], parts[3], parts[4]
	if dd == "" || mm == "" || yy == "" {
		return fields, nil, false
	}
	mm = strings.ToLower(mm)
	if pyIndex(pyMonthNames, mm) < 0 {
		dd, mm = mm, strings.ToLower(dd)
		if pyIndex(pyMonthNames, mm) < 0 {
			return fields, nil, false
		}
	}
	month := int64(pyIndex(pyMonthNames, mm) + 1)
	if month > 12 {
		month -= 12
	}
	dd = strings.TrimSuffix(dd, ",")
	if i := strings.Index(yy, ":"); i > 0 {
		yy, tm = tm, yy
	}
	if strings.HasSuffix(yy, ",") {
		yy = yy[:len(yy)-1]
		if yy == "" {
			return fields, nil, false
		}
	}
	if yy == "" {
		return fields, nil, false // IndexError su yy[0]
	}
	if !pyIsDigitLatin1(yy) {
		yy, tz = tz, yy
	}
	if tm == "" {
		return fields, nil, false // IndexError su tm[-1]
	}
	tm = strings.TrimSuffix(tm, ",")
	var thh, tmm, tss string
	switch hms := strings.Split(tm, ":"); {
	case len(hms) == 2:
		thh, tmm, tss = hms[0], hms[1], "0"
	case len(hms) == 3:
		thh, tmm, tss = hms[0], hms[1], hms[2]
	case len(hms) == 1 && strings.Contains(hms[0], "."):
		// Alcuni client non conformi separano l'ora con "."
		switch dots := strings.Split(hms[0], "."); len(dots) {
		case 2:
			thh, tmm, tss = dots[0], dots[1], "0"
		case 3:
			thh, tmm, tss = dots[0], dots[1], dots[2]
		default:
			return fields, nil, false
		}
	default:
		return fields, nil, false
	}
	var values [5]int64
	tooBig := false
	for i, s := range []string{yy, dd, thh, tmm, tss} {
		v, isBig, ok := pyInt64(s)
		if !ok {
			return fields, nil, false
		}
		tooBig = tooBig || isBig
		values[i] = v
	}
	if tooBig {
		// datetime() solleverebbe OverflowError
		return fields, nil, false
	}
	year := values[0]
	if year < 100 {
		if year > 68 {
			year += 1900
		} else {
			year += 2000
		}
	}

	tzUpper := strings.ToUpper(tz)
	var offset *int64
	if v, ok := pyTimezones[tzUpper]; ok {
		offset = &v
	} else if v, isBig, ok := pyInt64(tz); ok {
		if isBig {
			return fields, nil, false // timedelta(seconds=...) → OverflowError
		}
		if !(v == 0 && strings.HasPrefix(tz, "-")) {
			offset = &v
		}
	}
	if offset != nil && *offset != 0 {
		v := *offset
		sign := int64(1)
		if v < 0 {
			sign, v = -1, -v
		}
		if v > 1_000_000 || v < 0 {
			// ben oltre ±24 ore: timezone() solleverebbe ValueError
			return fields, nil, false
		}
		seconds := sign * ((v/100)*3600 + (v%100)*60)
		offset = &seconds
	}
	return [6]int64{year, month, values[1], values[2], values[3], values[4]}, offset, true
}

// parsedateToDatetime replica parsedate_to_datetime seguito dal
// replace(tzinfo=utc) di should_return_304 per le date senza fuso. ok=false
// per qualunque eccezione di Python (formato non valido, valori fuori
// intervallo, offset di 24 ore o più).
func parsedateToDatetime(data string) (time.Time, bool) {
	f, offset, ok := parsedateTZ(data)
	if !ok {
		return time.Time{}, false
	}
	year, month, day, hour, minute, second := f[0], f[1], f[2], f[3], f[4], f[5]
	if year < 1 || year > 9999 || hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return time.Time{}, false
	}
	if day < 1 || day > int64(daysIn(time.Month(month), int(year))) {
		return time.Time{}, false
	}
	loc := time.UTC
	if offset != nil {
		if *offset <= -86400 || *offset >= 86400 {
			return time.Time{}, false
		}
		loc = time.FixedZone("", int(*offset))
	}
	return time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, loc), true
}

// daysIn restituisce i giorni del mese nel calendario gregoriano prolettico.
func daysIn(m time.Month, year int) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
