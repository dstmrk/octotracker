package registration

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dstmrk/octotracker/internal/formatters"
)

// ValidateNumericInput valida un numero inserito dall'utente.
//
// Validazioni eseguite, in ordine:
//  1. lunghezza massima in caratteri (protezione da input molto lunghi);
//  2. conversione in numero, con la virgola come separatore decimale;
//  3. valore non negativo;
//  4. valore finito (niente NaN né infiniti).
//
// Restituisce (value, ok, errMsg):
//   - ok true: valore valido, errMsg vuoto;
//   - ok false ed errMsg vuoto: non è un numero (compresi "nan", "inf" e i
//     valori che superano il massimo float), il messaggio lo sceglie il
//     chiamante;
//   - ok false ed errMsg non vuoto: input troppo lungo o negativo ("-inf"
//     e "-1e400" sono negativi).
func ValidateNumericInput(text string) (value float64, ok bool, errMsg string) {
	// 1. Verifica lunghezza (protezione attacchi)
	if utf8.RuneCountInString(text) > formatters.MaxNumericInputLength {
		return 0, false, formatters.ErrorInputTooLong
	}

	// 2. Prova a convertire in numero: sono ammessi "+nan" e "-nan",
	// non gli esadecimali
	s := strings.TrimSpace(strings.ReplaceAll(text, ",", "."))
	if len(s) == 4 && (s[0] == '+' || s[0] == '-') && strings.EqualFold(s[1:], "nan") {
		s = "nan"
	}
	value, err := strconv.ParseFloat(s, 64)
	if (err != nil && !errors.Is(err, strconv.ErrRange)) || strings.ContainsAny(s, "xX") {
		return 0, false, "" // Errore di conversione, gestito dal chiamante
	}

	// 3. Verifica che non sia negativo
	if value < 0 {
		return 0, false, formatters.ErrorValueNegative
	}

	// 4. Verifica che sia finito: NaN e +Inf non sono salvabili e
	// bloccherebbero checker e reminder
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false, "" // Come un input non numerico
	}

	return value, true, ""
}
