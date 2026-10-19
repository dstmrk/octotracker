package app

// Test end-to-end differenziale: testdata/e2e_golden.json contiene sequenze
// di update elaborate dalla versione Python del bot (stessi handler e
// stesso ordine, error handler incluso) con un client HTTP finto che
// registra le chiamate all'API. Qui gli stessi update passano dal
// dispatcher di NewDispatcher: le chiamate all'API (metodo e parametri come
// vanno sulla rete) e lo stato del database devono coincidere passo per
// passo.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/PaulSonOfLars/gotgbot/v2"

	"github.com/dstmrk/octotracker/internal/checker"
	"github.com/dstmrk/octotracker/internal/config"
	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/testutil"
)

type e2eCall struct {
	Method string            `json:"method"`
	Params map[string]string `json:"params"`
}

type e2eStep struct {
	Update    json.RawMessage `json:"update"`
	SeedRates *struct {
		DataFonte string `json:"data_fonte"`
		Rates     []struct {
			Servizio            string   `json:"servizio"`
			Tipo                string   `json:"tipo"`
			Fascia              string   `json:"fascia"`
			Energia             float64  `json:"energia"`
			Commercializzazione *float64 `json:"commercializzazione"`
			CodOfferta          *string  `json:"cod_offerta"`
		} `json:"rates"`
		Inserted int `json:"inserted"`
	} `json:"seed_rates"`
	Checker bool `json:"checker"`
	CheckDB *struct {
		User          json.RawMessage `json:"user"`
		Pending       bool            `json:"pending"`
		FeedbackCount int             `json:"feedback_count"`
	} `json:"check_db"`
	Calls []e2eCall `json:"calls"`
}

// wireValue converte un parametro di gotgbot nel testo inviato sulla rete
// (getFieldContents: stringhe e numeri così come sono, il resto in JSON).
func wireValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case *string:
		if x == nil {
			return ""
		}
		return *x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<%v>", err)
	}
	return string(raw)
}

// normalizeParam rende confrontabili i valori JSON (reply_markup): stessa
// struttura indipendentemente da spazi e ordine delle chiavi.
func normalizeParam(value string) any {
	var parsed any
	if (strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")) && json.Unmarshal([]byte(value), &parsed) == nil {
		return parsed
	}
	return value
}

// adminAlertValues sono i valori delle righe Tipo, Messaggio e Update
// dell'alert all'admin: il tipo e il testo dell'errore (in Python la classe
// e il messaggio dell'eccezione, il repr dell'update) non fanno parte del
// confronto, il modello del messaggio sì.
var adminAlertValues = regexp.MustCompile(`(?s)(<b>Tipo:</b>) .*\n(<b>Messaggio:</b>) .*\n(<b>Update:</b>) .*`)

// isEmptyKeyboard indica un reply_markup senza pulsanti (gotgbot lo invia
// sempre in editMessageText, la versione Python lo omette): è considerato
// assente.
func isEmptyKeyboard(v string) bool {
	var kb struct {
		InlineKeyboard [][]any `json:"inline_keyboard"`
	}
	return json.Unmarshal([]byte(v), &kb) == nil && len(kb.InlineKeyboard) == 0
}

// normalizeCalls prepara le chiamate per il confronto.
func normalizeCalls(calls []e2eCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		m := map[string]any{"method": c.Method}
		for k, v := range c.Params {
			if k == "reply_markup" && isEmptyKeyboard(v) {
				continue
			}
			if k == "text" && c.Params["chat_id"] == "999" {
				v = adminAlertValues.ReplaceAllString(v, "$1 …\n$2 …\n$3 …")
			}
			m[k] = normalizeParam(v)
		}
		out = append(out, m)
	}
	return out
}

func goCalls(calls []testutil.Call) []e2eCall {
	out := make([]e2eCall, 0, len(calls))
	for _, c := range calls {
		if c.Method == "getMe" {
			continue
		}
		params := map[string]string{}
		for k, v := range c.Params {
			params[k] = wireValue(v)
		}
		out = append(out, e2eCall{Method: c.Method, Params: params})
	}
	return out
}

// jsonEqual confronta due documenti JSON come valori.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// userState è {"luce": ..., "gas": ...} dell'utente, o null.
func userState(store *database.Store, id string) []byte {
	u := store.LoadUser(id)
	if u == nil {
		return []byte("null")
	}
	raw, _ := json.Marshal(map[string]any{"luce": u.Luce, "gas": u.Gas})
	return raw
}

// runE2EStep esegue il passo i (update, tariffe, checker o controllo del
// database).
func (h *harness) runE2EStep(i int, step e2eStep) {
	t := h.t
	t.Helper()
	switch {
	case step.Update != nil:
		var u gotgbot.Update
		if err := json.Unmarshal(step.Update, &u); err != nil {
			t.Fatalf("passo %d: update non valido: %v", i, err)
		}
		if err := h.d.ProcessUpdate(h.b, &u, nil); err != nil {
			t.Fatalf("passo %d: ProcessUpdate: %v", i, err)
		}
	case step.SeedRates != nil:
		var recs []database.RateRecord
		for _, r := range step.SeedRates.Rates {
			recs = append(recs, database.RateRecord{Servizio: r.Servizio, Tipo: r.Tipo, Fascia: r.Fascia,
				Energia: r.Energia, Commercializzazione: r.Commercializzazione, CodOfferta: r.CodOfferta})
		}
		if n := h.store.SaveRatesBatch(step.SeedRates.DataFonte, recs); n != step.SeedRates.Inserted {
			t.Fatalf("passo %d: SaveRatesBatch = %d, atteso %d", i, n, step.SeedRates.Inserted)
		}
	case step.Checker:
		if err := checker.CheckAndNotifyUsers(context.Background(), h.b, h.store); err != nil {
			t.Fatalf("passo %d: checker: %v", i, err)
		}
	case step.CheckDB != nil:
		h.checkE2EDB(i, step)
	}
}

// checkE2EDB confronta utente, tariffe pendenti e numero di feedback.
func (h *harness) checkE2EDB(i int, step e2eStep) {
	t := h.t
	t.Helper()
	id := strconv.FormatInt(testutil.DefaultUserID, 10)
	if got := userState(h.store, id); !jsonEqual(got, step.CheckDB.User) {
		t.Errorf("passo %d: utente = %s, atteso %s", i, got, step.CheckDB.User)
	}
	if got := h.store.LoadPendingRates(id) != nil; got != step.CheckDB.Pending {
		t.Errorf("passo %d: tariffe pendenti = %v, atteso %v", i, got, step.CheckDB.Pending)
	}
	if got := h.store.GetFeedbackCount(); got != step.CheckDB.FeedbackCount {
		t.Errorf("passo %d: feedback = %d, attesi %d", i, got, step.CheckDB.FeedbackCount)
	}
}

// countCompared conta le chiamate attese con un testo o un
// answerCallbackQuery.
func countCompared(calls []map[string]any) int {
	n := 0
	for _, c := range calls {
		if _, ok := c["text"]; ok || c["method"] == "answerCallbackQuery" {
			n++
		}
	}
	return n
}

func TestGoldenE2EDispatcher(t *testing.T) {
	var scenarios []struct {
		Name  string    `json:"name"`
		Steps []e2eStep `json:"steps"`
	}
	loadGolden(t, "e2e_golden.json", &scenarios)
	captureLogs(t)
	compared := 0
	t.Cleanup(func() {
		// Il confronto non deve essere vuoto: il golden ha centinaia di chiamate
		if compared < 200 {
			t.Errorf("solo %d chiamate confrontate", compared)
		}
	})

	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			h := newHarness(t, config.Config{AdminUserID: "999", CheckerHour: 10})
			for i, step := range sc.Steps {
				h.fc.Reset()
				h.runE2EStep(i, step)
				got, want := normalizeCalls(goCalls(h.fc.Calls())), normalizeCalls(step.Calls)
				compared += countCompared(want)
				if !reflect.DeepEqual(got, want) {
					gotJSON, _ := json.MarshalIndent(got, "", " ")
					wantJSON, _ := json.MarshalIndent(want, "", " ")
					t.Errorf("passo %d (%s): chiamate diverse\nGo:     %s\nPython: %s", i, step.Update, gotJSON, wantJSON)
				}
			}
		})
	}
}
