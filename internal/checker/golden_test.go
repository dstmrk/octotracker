package checker

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
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
// codici offerta, varianti di last_notified_rates). Ogni output Go deve
// coincidere esattamente (stringhe byte per byte, float bit per bit, blob
// JSON identici a json.dumps).

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

// expectKeyError verifica che il panic corrisponda all'eccezione Python attesa.
func expectKeyError(t *testing.T, what string, want *goldenError, got any) {
	t.Helper()
	if want.Type != "KeyError" {
		t.Fatalf("%s: eccezione Python inattesa nel corpus: %+v", what, want)
	}
	ke, ok := got.(KeyError)
	if !ok {
		t.Errorf("%s: atteso panic KeyError(%q), ottenuto %#v", what, want.Key, got)
		return
	}
	if string(ke) != want.Key || ke.Error() != want.Str {
		t.Errorf("%s: KeyError = %q (%s), atteso %q (%s)", what, string(ke), ke.Error(), want.Key, want.Str)
	}
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
	wantTipo, wantFascia := "", ""
	if want.Tipo != nil {
		wantTipo = *want.Tipo
	}
	if want.Fascia != nil {
		wantFascia = *want.Fascia
	}
	if got.Tipo != wantTipo || got.Fascia != wantFascia {
		t.Errorf("%s: tipo/fascia = %q/%q, attesi %q/%q", what, got.Tipo, got.Fascia, wantTipo, wantFascia)
	}
}

func checkMessage(t *testing.T, what string, messages []string, want goldenMsgResult, f func() string) {
	t.Helper()
	var got string
	r := catch(func() { got = f() })
	if want.Error != nil {
		expectKeyError(t, what, want.Error, r)
		return
	}
	if r != nil {
		t.Errorf("%s: panic inatteso: %v", what, r)
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

func checkJSONResult(t *testing.T, what string, want goldenJSONResult, f func() ([]byte, error)) {
	t.Helper()
	var got []byte
	var err error
	r := catch(func() { got, err = f() })
	if want.Error != nil {
		expectKeyError(t, what, want.Error, r)
		return
	}
	if r != nil {
		t.Errorf("%s: panic inatteso: %v", what, r)
		return
	}
	if err != nil {
		t.Errorf("%s: errore di serializzazione: %v", what, err)
		return
	}
	if want.JSON == nil {
		t.Fatalf("%s: caso golden senza JSON", what)
	}
	assertSameText(t, what, string(got), *want.JSON)
}

func TestGoldenChecker(t *testing.T) {
	g := loadCheckerGolden(t)

	for _, c := range g.Cases {
		user := c.User
		user.ID = "u1"
		rates := c.Rates

		t.Run(c.Tag, func(t *testing.T) {
			// check_better_rates
			savings := CheckBetterRates(&user, rates)
			if savings.HasSavings != c.Savings.HasSavings || savings.IsMixed != c.Savings.IsMixed {
				t.Errorf("has_savings/is_mixed = %v/%v, attesi %v/%v",
					savings.HasSavings, savings.IsMixed, c.Savings.HasSavings, c.Savings.IsMixed)
			}
			checkUtilitySavings(t, "luce", c.Savings.Luce, savings.Luce)
			checkUtilitySavings(t, "gas", c.Savings.Gas, savings.Gas)

			// _build_current_octopus_rates + _should_notify_user
			var cur database.LastNotifiedRates
			checkJSONResult(t, "current_octopus", c.CurrentOctopus, func() ([]byte, error) {
				cur = buildCurrentOctopusRates(&user, rates)
				return cur.MarshalJSON()
			})
			if c.ShouldNotify != nil {
				if got := shouldNotifyUser(&user, cur); got != *c.ShouldNotify {
					t.Errorf("should_notify = %v, atteso %v", got, *c.ShouldNotify)
				}
			}

			// _should_show_utility
			type shown struct {
				show bool
				est  *float64
			}
			shows := map[string]shown{}
			for _, u := range []string{"luce", "gas"} {
				want := c.Show[u]
				var s shown
				r := catch(func() { s.show, s.est = shouldShowUtility(u, savings, &user, rates) })
				if want.Error != nil {
					expectKeyError(t, "show "+u, want.Error, r)
					continue
				}
				if r != nil {
					t.Errorf("show %s: panic inatteso: %v", u, r)
					continue
				}
				if s.show != *want.Show || !sameOptFloat(s.est, want.Estimated) {
					t.Errorf("show %s = (%v, %v), atteso (%v, %v)", u, s.show, fptr(s.est), *want.Show, fptr(want.Estimated))
				}
				shows[u] = s
			}

			// format_notification con i default (True, True, None, None)
			checkMessage(t, "message_default", g.Messages, c.MessageDefault, func() string {
				return FormatNotification(savings, &user, rates, true, true, nil, nil)
			})

			// format_notification e _build_pending_rates con i flag calcolati
			if c.MessageShown != nil {
				l, gs := shows["luce"], shows["gas"]
				checkMessage(t, "message_shown", g.Messages, *c.MessageShown, func() string {
					return FormatNotification(savings, &user, rates, l.show, gs.show, l.est, gs.est)
				})
				checkJSONResult(t, "pending_shown", *c.PendingShown, func() ([]byte, error) {
					return buildPendingRates(&user, rates, l.show, gs.show).MarshalJSON()
				})
			}

			// Variante casuale di flag e stime
			v := c.MessageVariant
			checkMessage(t, "message_variant", g.Messages, v.goldenMsgResult, func() string {
				return FormatNotification(savings, &user, rates, v.ShowLuce, v.ShowGas, v.LuceEstimated, v.GasEstimated)
			})
			pv := c.PendingVariant
			checkJSONResult(t, "pending_variant", pv.goldenJSONResult, func() ([]byte, error) {
				return buildPendingRates(&user, rates, pv.ShowLuce, pv.ShowGas).MarshalJSON()
			})

			// _prepare_user_notification (pipeline completa)
			var prep *preparedNotification
			r := catch(func() { prep = prepareUserNotification("u1", &user, rates) })
			switch {
			case c.Prepare.Error != nil:
				expectKeyError(t, "prepare", c.Prepare.Error, r)
			case r != nil:
				t.Errorf("prepare: panic inatteso: %v", r)
			case c.Prepare.Value == nil:
				if prep != nil {
					t.Errorf("prepare: atteso None, ottenuta notifica %q", prep.message)
				}
			case prep == nil:
				t.Errorf("prepare: attesa notifica, ottenuto nil")
			default:
				want := c.Prepare.Value
				assertSameText(t, "prepare.message", prep.message, g.Messages[want.Message])
				co, err := prep.currentOctopus.MarshalJSON()
				if err != nil {
					t.Fatalf("prepare.current_octopus: %v", err)
				}
				assertSameText(t, "prepare.current_octopus", string(co), want.CurrentOctopus)
				pend, err := prep.pendingRates.MarshalJSON()
				if err != nil {
					t.Fatalf("prepare.pending: %v", err)
				}
				assertSameText(t, "prepare.pending", string(pend), want.Pending)
			}
		})
	}
}

func fptr(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}
