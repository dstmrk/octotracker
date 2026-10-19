package registration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
	"github.com/dstmrk/octotracker/internal/testutil"
	"github.com/dstmrk/octotracker/internal/tg"
)

const testUID = testutil.DefaultUserID

// openStore apre un database temporaneo per il test (temp_database di conftest.py).
func openStore(t *testing.T) *database.Store {
	t.Helper()
	s, err := database.Open(filepath.Join(t.TempDir(), "test_octotracker.db"))
	if err != nil {
		t.Fatalf("database.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newHandlers crea Handlers con database temporaneo e user_data vuoto.
func newHandlers(t *testing.T) *Handlers {
	t.Helper()
	return &Handlers{Store: openStore(t), UserData: tg.NewUserData()}
}

// setUserData replica `mock_context.user_data = {...}`.
func setUserData(h *Handlers, uid int64, data map[string]any) {
	h.UserData.Clear(uid)
	for k, v := range data {
		h.UserData.Set(uid, k, v)
	}
}

// stateOf decodifica il valore restituito da un handler: nome dello stato,
// "END" oppure "" con l'errore.
func stateOf(err error) (string, error) {
	var sc *handlers.ConversationStateChange
	if errors.As(err, &sc) {
		if sc.End {
			return "END", nil
		}
		if sc.NextState != nil {
			return *sc.NextState, nil
		}
	}
	return "", err
}

// mustState fallisce il test se l'handler non ha restituito uno stato.
func mustState(t *testing.T, err error) string {
	t.Helper()
	s, e := stateOf(err)
	if e != nil {
		t.Fatalf("handler error: %v", e)
	}
	return s
}

// ---------------------------------------------------------------- log capture

type logRecord struct {
	Logger, Level, Message string
}

type captureHandler struct {
	mu      *sync.Mutex
	records *[]logRecord
	name    string
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.records = append(*h.records, logRecord{h.name, logging.LevelName(r.Level), r.Message})
	return nil
}

func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	for _, a := range attrs {
		if a.Key == "logger" {
			h.name = a.Value.String()
		}
	}
	return h
}

func (h captureHandler) WithGroup(string) slog.Handler { return h }

// logCapture installa un logger di default che registra i messaggi; non va
// usato con t.Parallel.
type logCapture struct {
	mu      sync.Mutex
	records []logRecord
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(captureHandler{mu: &c.mu, records: &c.records}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// handlersRecords restituisce i record dei logger "handlers" e "handlers.*"
// (gli stessi catturati dal generatore Python).
func (c *logCapture) handlersRecords() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := [][]string{}
	for _, r := range c.records {
		if r.Logger == "handlers" || strings.HasPrefix(r.Logger, "handlers.") {
			out = append(out, []string{r.Logger, r.Level, r.Message})
		}
	}
	return out
}

// ---------------------------------------------------------------- golden helpers

func loadGolden(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode golden %s: %v", name, err)
	}
}

// pyFloatRepr decodifica {"$float": repr} (repr Python, anche nan/inf).
func pyFloatRepr(t testing.TB, v any) (float64, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return 0, false
	}
	s, ok := m["$float"].(string)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		t.Fatalf("bad float repr %q: %v", s, err)
	}
	return f, true
}

// denorm converte i valori normalizzati del generatore nei valori Go
// salvati in user_data (float64, bool, string).
func denorm(t testing.TB, v any) any {
	if f, ok := pyFloatRepr(t, v); ok {
		return f
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = denorm(t, e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = denorm(t, e)
		}
		return out
	}
	return v
}

func sameFloat(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Float64bits(a) == math.Float64bits(b)
}

// diffNorm confronta un valore atteso (formato del generatore) con il
// valore Go e restituisce le differenze.
func diffNorm(t testing.TB, path string, want, got any) []string {
	if wf, ok := pyFloatRepr(t, want); ok {
		if gf, ok := got.(float64); !ok || !sameFloat(wf, gf) {
			return []string{fmt.Sprintf("%s: want float %v, got %#v", path, wf, got)}
		}
		return nil
	}
	switch w := want.(type) {
	case map[string]any:
		return diffMap(t, path, w, got)
	case []any:
		return diffSlice(t, path, w, got)
	case float64:
		if gf, ok := toFloat(got); !ok || gf != w {
			return []string{fmt.Sprintf("%s: want %v, got %#v", path, w, got)}
		}
		return nil
	default:
		if want != got {
			return []string{fmt.Sprintf("%s: want %#v, got %#v", path, want, got)}
		}
		return nil
	}
}

// diffMap confronta un oggetto atteso con il valore Go, chiave per chiave.
func diffMap(t testing.TB, path string, w map[string]any, got any) []string {
	g, ok := got.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("%s: want object, got %#v", path, got)}
	}
	keys := slices.Sorted(maps.Keys(w))
	for k := range g {
		if _, ok := w[k]; !ok {
			keys = append(keys, k)
		}
	}
	var diffs []string
	for _, k := range keys {
		wv, wok := w[k]
		gv, gok := g[k]
		switch {
		case !wok:
			diffs = append(diffs, fmt.Sprintf("%s.%s: unexpected %#v", path, k, gv))
		case !gok:
			diffs = append(diffs, fmt.Sprintf("%s.%s: missing (want %#v)", path, k, wv))
		default:
			diffs = append(diffs, diffNorm(t, path+"."+k, wv, gv)...)
		}
	}
	return diffs
}

// diffSlice confronta una lista attesa con il valore Go, elemento per elemento.
func diffSlice(t testing.TB, path string, w []any, got any) []string {
	g, ok := got.([]any)
	if !ok || len(g) != len(w) {
		return []string{fmt.Sprintf("%s: want %#v, got %#v", path, w, got)}
	}
	var diffs []string
	for i := range w {
		diffs = append(diffs, diffNorm(t, fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
	}
	return diffs
}

// toFloat converte i numeri Go (float64 o interi) in float64.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

// utilityMap converte una fornitura nel dict di database._row_to_dict.
func utilityMap(u database.Utility) map[string]any {
	m := map[string]any{
		"tipo": u.Tipo, "fascia": u.Fascia, "energia": u.Energia,
		"commercializzazione": u.Commercializzazione,
	}
	for k, p := range map[string]*float64{
		"consumo_f1": u.ConsumoF1, "consumo_f2": u.ConsumoF2, "consumo_f3": u.ConsumoF3,
		"consumo_annuo": u.ConsumoAnnuo,
	} {
		if p != nil {
			m[k] = *p
		}
	}
	if u.Scadenza != "" {
		m["scadenza"] = u.Scadenza
	}
	return m
}

// userMap converte l'utente nel dict restituito da load_user (nil se assente).
func userMap(u *database.User) any {
	if u == nil {
		return nil
	}
	m := map[string]any{"luce": utilityMap(u.Luce)}
	if u.Gas != nil {
		m["gas"] = utilityMap(*u.Gas)
	}
	if len(u.LastNotifiedRates) > 0 {
		m["last_notified_rates"] = map[string]any(u.LastNotifiedRates)
	}
	return m
}

// utilityFromNorm costruisce una fornitura dal dict normalizzato del generatore.
func utilityFromNorm(t testing.TB, v map[string]any) database.Utility {
	u := database.Utility{}
	u.Tipo, _ = v["tipo"].(string)
	u.Fascia, _ = v["fascia"].(string)
	num := func(k string) *float64 {
		raw, ok := v[k]
		if !ok || raw == nil {
			return nil
		}
		f, ok := pyFloatRepr(t, raw)
		if !ok {
			t.Fatalf("campo %s non float: %#v", k, raw)
		}
		return &f
	}
	if p := num("energia"); p != nil {
		u.Energia = *p
	}
	if p := num("commercializzazione"); p != nil {
		u.Commercializzazione = *p
	}
	u.ConsumoF1, u.ConsumoF2, u.ConsumoF3 = num("consumo_f1"), num("consumo_f2"), num("consumo_f3")
	u.ConsumoAnnuo = num("consumo_annuo")
	u.Scadenza, _ = v["scadenza"].(string)
	return u
}

// userFromNorm costruisce l'utente dal dict normalizzato {"luce": ..., "gas": ...}.
func userFromNorm(t testing.TB, v any) database.User {
	m := v.(map[string]any)
	u := database.User{Luce: utilityFromNorm(t, m["luce"].(map[string]any))}
	if g, ok := m["gas"].(map[string]any); ok {
		gas := utilityFromNorm(t, g)
		u.Gas = &gas
	}
	return u
}

// normalizeCalls converte le chiamate registrate dal FakeClient nel formato
// dei parametri di python-telegram-bot: JSON generico, senza il reply_markup
// vuoto che gotgbot invia sempre in editMessageText.
func normalizeCalls(t testing.TB, calls []testutil.Call) []any {
	out := []any{}
	for _, c := range calls {
		raw, err := json.Marshal(c.Params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if rm, ok := params["reply_markup"].(map[string]any); ok && len(rm) == 0 {
			delete(params, "reply_markup")
		}
		out = append(out, map[string]any{"method": c.Method, "params": params})
	}
	return out
}

// goldenError è l'eccezione Python registrata dal generatore.
type goldenError struct {
	Type     string `json:"type"`
	Message  string `json:"message"`
	Telegram bool   `json:"telegram"`
}

// matches verifica che l'handler abbia restituito un errore come
// l'eccezione Python: un errore dell'API Telegram se lo era in Python,
// altrimenti un errore qualsiasi (il testo non conta).
func (g *goldenError) matches(err error) bool {
	var tgErr *gotgbot.TelegramError
	return err != nil && (!g.Telegram || errors.As(err, &tgErr))
}

func compareLogs(t *testing.T, name string, want, got [][]string) {
	t.Helper()
	if fmt.Sprint(want) != fmt.Sprint(got) {
		t.Errorf("%s logs:\nwant %q\n got %q", name, want, got)
	}
}

// handlerFuncs mappa i nomi delle funzioni Python sui metodi Go.
func handlerFuncs(h *Handlers) map[string]handlers.Response {
	return map[string]handlers.Response{
		"start":                 h.Start,
		"tipo_tariffa":          h.TipoTariffa,
		"luce_tipo_variabile":   h.LuceTipoVariabile,
		"luce_energia":          h.LuceEnergia,
		"luce_comm":             h.LuceComm,
		"luce_attivazione":      h.LuceAttivazione,
		"skip_luce_attivazione": h.SkipLuceAttivazione,
		"vuoi_consumi_luce":     h.VuoiConsumiLuce,
		"luce_consumo_f1":       h.LuceConsumoF1,
		"luce_consumo_f2":       h.LuceConsumoF2,
		"luce_consumo_f3":       h.LuceConsumoF3,
		"ha_gas":                h.HaGas,
		"gas_tipo_tariffa":      h.GasTipoTariffa,
		"gas_energia":           h.GasEnergia,
		"gas_comm":              h.GasComm,
		"gas_attivazione":       h.GasAttivazione,
		"skip_gas_attivazione":  h.SkipGasAttivazione,
		"vuoi_consumi_gas":      h.VuoiConsumiGas,
		"gas_consumo":           h.GasConsumo,
	}
}

// decodeUpdate decodifica un update JSON come farebbe gotgbot in produzione.
func decodeUpdate(t testing.TB, raw json.RawMessage) *gotgbot.Update {
	var u gotgbot.Update
	if err := json.Unmarshal(raw, &u); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	return &u
}

// newCtx costruisce il contesto gotgbot per un update.
func newCtx(b *gotgbot.Bot, u *gotgbot.Update) *ext.Context {
	return ext.NewContext(b, u, nil)
}
