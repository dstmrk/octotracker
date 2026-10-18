package pycompat

import (
	"fmt"
	"regexp"
	"time"
)

// ISOFormat replica datetime.isoformat() di un datetime "naive" (senza
// fuso): "YYYY-MM-DDTHH:MM:SS", con ".ffffff" solo se i microsecondi non
// sono zero. Usa l'ora di parete di t nel suo fuso.
func ISOFormat(t time.Time) string {
	s := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d",
		t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second())
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s
}

// strptimeYMD è la regex che _strptime costruisce per "%Y-%m-%d" (\d di
// Python per le str = cifre Unicode, categoria Nd).
var strptimeYMD = regexp.MustCompile(`^(\p{Nd}\p{Nd}\p{Nd}\p{Nd})-(1[0-2]|0[1-9]|[1-9])-(3[01]|[12]\p{Nd}|0[1-9]|[1-9]| [1-9])`)

// daysIn restituisce il numero di giorni del mese (calendario gregoriano prolettico).
func daysIn(year, month int) int {
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// StrptimeYMD replica datetime.strptime(s, "%Y-%m-%d"): restituisce la
// mezzanotte (UTC, da usare come datetime "naive") oppure un ValueError con
// il messaggio Python ("time data '...' does not match format '%Y-%m-%d'",
// "unconverted data remains: ...", "day is out of range for month",
// "year 0 is out of range").
func StrptimeYMD(s string) (time.Time, error) {
	m := strptimeYMD.FindStringSubmatchIndex(s)
	if m == nil {
		return time.Time{}, NewError("ValueError",
			fmt.Sprintf("time data %s does not match format %s", StrRepr(s), StrRepr("%Y-%m-%d")))
	}
	if m[1] != len(s) {
		return time.Time{}, NewError("ValueError", "unconverted data remains: "+s[m[1]:])
	}
	num := func(g int) int {
		v, _ := ParseInt(s[m[2*g]:m[2*g+1]])
		return int(v.Int64())
	}
	year, month, day := num(1), num(2), num(3)
	if year < 1 {
		return time.Time{}, NewError("ValueError", fmt.Sprintf("year %d is out of range", year))
	}
	if day > daysIn(year, month) {
		return time.Time{}, NewError("ValueError", "day is out of range for month")
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC), nil
}

// NaiveWall restituisce l'ora di parete di t (nel suo fuso) come istante
// UTC, così che la differenza tra due valori replichi la sottrazione tra
// datetime "naive" di Python (senza effetti dell'ora legale).
func NaiveWall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// TimedeltaDays replica (a - b).days per due datetime naive: il numero di
// giorni arrotondato per difetto (floor), anche per differenze negative.
func TimedeltaDays(a, b time.Time) int {
	aw, bw := NaiveWall(a), NaiveWall(b)
	// secondi interi con resto in nanosecondi non negativo: floor(totale / 1 giorno)
	// coincide con floor(secondi / 86400). Unix() evita l'overflow di time.Duration.
	secs := aw.Unix() - bw.Unix()
	if aw.Nanosecond() < bw.Nanosecond() {
		secs--
	}
	days := secs / 86400
	if secs%86400 != 0 && secs < 0 {
		days--
	}
	return int(days)
}
