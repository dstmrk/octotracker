package checker

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dstmrk/octotracker/internal/database"
)

// Test differenziali contro checker.py: testdata/checker_golden.json è
// generato eseguendo le funzioni Python (check_better_rates,
// _build_current_octopus_rates, _should_notify_user, _should_show_utility,
// format_notification, _build_pending_rates, _prepare_user_notification) su
// centinaia di combinazioni di utenti e tariffe (fissa/variabile,
// mono/bi/trioraria, con/senza gas, con/senza consumi, miglioramenti,
// peggioramenti, casi mixed, valori uguali, commercializzazione mancante,
// codici offerta, varianti di last_notified_rates). I messaggi devono
// coincidere byte per byte, i float bit per bit e i blob JSON per valore
// (stesse chiavi e valori di json.dumps).

type goldenError struct {
	Type string `json:"type"`
	Key  string `json:"key"`
	Str  string `json:"str"`
}

type goldenSaving struct {
	Attuale   float64 `json:"attuale"`
	Nuova     float64 `json:"nuova"`
	Risparmio float64 `json:"risparmio"`
}

type goldenUtilitySavings struct {
	Energia      *goldenSaving `json:"energia"`
	Comm         *goldenSaving `json:"comm"`
	EnergiaWorse bool          `json:"energia_worse"`
	CommWorse    bool          `json:"comm_worse"`
	IsMixed      bool          `json:"is_mixed"`
	Tipo         *string       `json:"tipo"`
	Fascia       *string       `json:"fascia"`
}

type goldenJSONResult struct {
	JSON  *string      `json:"json"`
	Error *goldenError `json:"error"`
}

type goldenMsgResult struct {
	Msg   *int         `json:"msg"`
	Error *goldenError `json:"error"`
}

type goldenShow struct {
	Show      *bool        `json:"show"`
	Estimated *float64     `json:"estimated"`
	Error     *goldenError `json:"error"`
}

type goldenCheckerCase struct {
	Tag     string                `json:"tag"`
	User    database.User         `json:"user"`
	Rates   database.CurrentRates `json:"rates"`
	Savings struct {
		HasSavings bool                 `json:"has_savings"`
		IsMixed    bool                 `json:"is_mixed"`
		Luce       goldenUtilitySavings `json:"luce"`
		Gas        goldenUtilitySavings `json:"gas"`
	} `json:"savings"`
	CurrentOctopus goldenJSONResult      `json:"current_octopus"`
	ShouldNotify   *bool                 `json:"should_notify"`
	Show           map[string]goldenShow `json:"show"`
	MessageDefault goldenMsgResult       `json:"message_default"`
	MessageShown   *goldenMsgResult      `json:"message_shown"`
	MessageVariant struct {
		ShowLuce      bool     `json:"show_luce"`
		ShowGas       bool     `json:"show_gas"`
		LuceEstimated *float64 `json:"luce_estimated"`
		GasEstimated  *float64 `json:"gas_estimated"`
		goldenMsgResult
	} `json:"message_variant"`
	PendingShown   *goldenJSONResult `json:"pending_shown"`
	PendingVariant struct {
		ShowLuce bool `json:"show_luce"`
		ShowGas  bool `json:"show_gas"`
		goldenJSONResult
	} `json:"pending_variant"`
	Prepare struct {
		Value *struct {
			CurrentOctopus string `json:"current_octopus"`
			Message        int    `json:"message"`
			Pending        string `json:"pending"`
		} `json:"value"`
		Error *goldenError `json:"error"`
	} `json:"prepare"`
}

type goldenCheckerFile struct {
	Messages []string            `json:"messages"`
	Cases    []goldenCheckerCase `json:"cases"`
}

func loadCheckerGolden(t *testing.T) goldenCheckerFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "checker_golden.json"))
	if err != nil {
		t.Fatalf("lettura golden: %v", err)
	}
	var g goldenCheckerFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatalf("decodifica golden: %v", err)
	}
	if len(g.Cases) < 500 {
		t.Fatalf("corpus golden troppo piccolo: %d casi", len(g.Cases))
	}
	return g
}

// catch esegue f e restituisce il valore di un eventuale panic.
func catch(f func()) (recovered any) {
	defer func() { recovered = recover() }()
	f()
	return nil
}

// expectKeyError verifica che il panic corrisponda al KeyError Python atteso.
func expectKeyError(t *testing.T, what string, want *goldenError, got any) {
	t.Helper()
	if want.Type != "KeyError" {
		t.Fatalf("%s: eccezione Python inattesa nel corpus: %+v", what, want)
	}
	if ke, ok := got.(KeyError); !ok || string(ke) != want.Key {
		t.Errorf("%s: atteso panic KeyError(%q), ottenuto %#v", what, want.Key, got)
	}
}

// expectNoPanic restituisce true se f non è andata in panic e il caso
// golden non prevede un'eccezione; altrimenti verifica il KeyError atteso.
func expectNoPanic(t *testing.T, what string, want *goldenError, recovered any) bool {
	t.Helper()
	if want != nil {
		expectKeyError(t, what, want, recovered)
		return false
	}
	if recovered != nil {
		t.Errorf("%s: panic inatteso: %v", what, recovered)
		return false
	}
	return true
}

func sameFloat(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) }

func sameOptFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return sameFloat(*a, *b)
}

func checkSaving(t *testing.T, what string, want *goldenSaving, got *RateSaving) {
	t.Helper()
	if want == nil || got == nil {
		if want != nil || got != nil {
			t.Errorf("%s: saving = %+v, atteso %+v", what, got, want)
		}
		return
	}
	if !sameFloat(got.Attuale, want.Attuale) || !sameFloat(got.Nuova, want.Nuova) || !sameFloat(got.Risparmio, want.Risparmio) {
		t.Errorf("%s: saving = %+v, atteso %+v", what, *got, *want)
	}
}

func checkUtilitySavings(t *testing.T, what string, want goldenUtilitySavings, got UtilitySavings) {
	t.Helper()
	checkSaving(t, what+".energia", want.Energia, got.Energia)
	checkSaving(t, what+".comm", want.Comm, got.Comm)
	if got.EnergiaWorse != want.EnergiaWorse || got.CommWorse != want.CommWorse || got.IsMixed != want.IsMixed {
		t.Errorf("%s: worse/mixed = %v/%v/%v, attesi %v/%v/%v", what,
			got.EnergiaWorse, got.CommWorse, got.IsMixed, want.EnergiaWorse, want.CommWorse, want.IsMixed)
	}
	if got.Tipo != deref(want.Tipo) || got.Fascia != deref(want.Fascia) {
		t.Errorf("%s: tipo/fascia = %q/%q, attesi %q/%q", what, got.Tipo, got.Fascia, deref(want.Tipo), deref(want.Fascia))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func checkMessage(t *testing.T, what string, messages []string, want goldenMsgResult, f func() string) {
	t.Helper()
	var got string
	if !expectNoPanic(t, what, want.Error, catch(func() { got = f() })) {
		return
	}
	if want.Msg == nil {
		t.Fatalf("%s: caso golden senza messaggio", what)
	}
	assertSameText(t, what, got, messages[*want.Msg])
}

// assertSameText confronta due testi e in caso di differenza mostra il primo
// punto divergente.
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

// assertSameJSON confronta per valore la serializzazione di got con il
// blob JSON scritto da Python.
func assertSameJSON(t *testing.T, what string, got any, want string) {
	t.Helper()
	b, err := json.Marshal(got)
	if err != nil {
		t.Errorf("%s: errore di serializzazione: %v", what, err)
		return
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("%s: JSON golden non valido: %v", what, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("%s:\n got  %s\n want %s", what, b, want)
	}
}

func checkJSONResult(t *testing.T, what string, want goldenJSONResult, f func() any) {
	t.Helper()
	var got any
	if !expectNoPanic(t, what, want.Error, catch(func() { got = f() })) {
		return
	}
	if want.JSON == nil {
		t.Fatalf("%s: caso golden senza JSON", what)
	}
	assertSameJSON(t, what, got, *want.JSON)
}

// shown è l'esito di shouldShowUtility per una fornitura.
type shown struct {
	show bool
	est  *float64
}

func TestGoldenChecker(t *testing.T) {
	g := loadCheckerGolden(t)

	for _, c := range g.Cases {
		user := c.User
		user.ID = "u1"

		t.Run(c.Tag, func(t *testing.T) {
			// check_better_rates
			savings := CheckBetterRates(&user, c.Rates)
			if savings.HasSavings != c.Savings.HasSavings || savings.IsMixed != c.Savings.IsMixed {
				t.Errorf("has_savings/is_mixed = %v/%v, attesi %v/%v",
					savings.HasSavings, savings.IsMixed, c.Savings.HasSavings, c.Savings.IsMixed)
			}
			checkUtilitySavings(t, "luce", c.Savings.Luce, savings.Luce)
			checkUtilitySavings(t, "gas", c.Savings.Gas, savings.Gas)

			checkGoldenCurrentOctopus(t, c, &user)
			shows := checkGoldenShow(t, c, savings, &user)
			checkGoldenMessages(t, g.Messages, c, savings, &user, shows)
			checkGoldenPrepare(t, g.Messages, c, &user)
		})
	}
}

// checkGoldenCurrentOctopus verifica _build_current_octopus_rates e
// _should_notify_user.
func checkGoldenCurrentOctopus(t *testing.T, c goldenCheckerCase, user *database.User) {
	t.Helper()
	var cur database.LastNotifiedRates
	checkJSONResult(t, "current_octopus", c.CurrentOctopus, func() any {
		cur = buildCurrentOctopusRates(user, c.Rates)
		return cur
	})
	if c.ShouldNotify != nil {
		if got := shouldNotifyUser(user, cur); got != *c.ShouldNotify {
			t.Errorf("should_notify = %v, atteso %v", got, *c.ShouldNotify)
		}
	}
}

// checkGoldenShow verifica _should_show_utility per luce e gas.
func checkGoldenShow(t *testing.T, c goldenCheckerCase, savings Savings, user *database.User) map[string]shown {
	t.Helper()
	shows := map[string]shown{}
	for _, u := range []string{"luce", "gas"} {
		want := c.Show[u]
		var s shown
		if !expectNoPanic(t, "show "+u, want.Error, catch(func() { s.show, s.est = shouldShowUtility(u, savings, user, c.Rates) })) {
			continue
		}
		if s.show != *want.Show || !sameOptFloat(s.est, want.Estimated) {
			t.Errorf("show %s = (%v, %v), atteso (%v, %v)", u, s.show, fptr(s.est), *want.Show, fptr(want.Estimated))
		}
		shows[u] = s
	}
	return shows
}

// checkGoldenMessages verifica format_notification e _build_pending_rates con
// i flag di default, con quelli calcolati e con una variante casuale.
func checkGoldenMessages(t *testing.T, messages []string, c goldenCheckerCase, savings Savings, user *database.User, shows map[string]shown) {
	t.Helper()
	checkMessage(t, "message_default", messages, c.MessageDefault, func() string {
		return FormatNotification(savings, user, c.Rates, true, true, nil, nil)
	})

	if c.MessageShown != nil {
		l, gs := shows["luce"], shows["gas"]
		checkMessage(t, "message_shown", messages, *c.MessageShown, func() string {
			return FormatNotification(savings, user, c.Rates, l.show, gs.show, l.est, gs.est)
		})
		checkJSONResult(t, "pending_shown", *c.PendingShown, func() any {
			return buildPendingRates(user, c.Rates, l.show, gs.show)
		})
	}

	v := c.MessageVariant
	checkMessage(t, "message_variant", messages, v.goldenMsgResult, func() string {
		return FormatNotification(savings, user, c.Rates, v.ShowLuce, v.ShowGas, v.LuceEstimated, v.GasEstimated)
	})
	pv := c.PendingVariant
	checkJSONResult(t, "pending_variant", pv.goldenJSONResult, func() any {
		return buildPendingRates(user, c.Rates, pv.ShowLuce, pv.ShowGas)
	})
}

// checkGoldenPrepare verifica _prepare_user_notification (pipeline completa).
func checkGoldenPrepare(t *testing.T, messages []string, c goldenCheckerCase, user *database.User) {
	t.Helper()
	var prep *preparedNotification
	if !expectNoPanic(t, "prepare", c.Prepare.Error, catch(func() { prep = prepareUserNotification("u1", user, c.Rates) })) {
		return
	}
	want := c.Prepare.Value
	switch {
	case want == nil && prep != nil:
		t.Errorf("prepare: atteso None, ottenuta notifica %q", prep.message)
	case want != nil && prep == nil:
		t.Errorf("prepare: attesa notifica, ottenuto nil")
	case want != nil:
		assertSameText(t, "prepare.message", prep.message, messages[want.Message])
		assertSameJSON(t, "prepare.current_octopus", prep.currentOctopus, want.CurrentOctopus)
		assertSameJSON(t, "prepare.pending", prep.pendingRates, want.Pending)
	}
}

func fptr(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
