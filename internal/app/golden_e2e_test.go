package app

// Test end-to-end differenziale: testdata/e2e_golden.json contiene sequenze
// di update elaborate dall'Application reale di bot.main()
// (python-telegram-bot 22.6, stessi handler e stesso ordine, error handler
// incluso) con un client HTTP finto che registra le chiamate all'API. Qui
// gli stessi update passano dal dispatcher di NewDispatcher: le chiamate
// all'API (metodo e parametri come vanno sulla rete) e lo stato del
// database devono coincidere passo per passo.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
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
func normalizeParam(key, value string) any {
	var parsed any
	if (strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")) && json.Unmarshal([]byte(value), &parsed) == nil {
		return parsed
	}
	return value
}

// normalizeCalls prepara le chiamate per il confronto:
//   - reply_markup senza pulsanti (gotgbot lo invia sempre in
//     editMessageText, PTB lo omette) è considerato assente;
//   - nell'alert all'admin la riga "Update:" (repr Python dell'update, non
//     riproducibile) viene esclusa.
func normalizeCalls(calls []e2eCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, c := range calls {
		m := map[string]any{"method": c.Method}
		for k, v := range c.Params {
			if k == "reply_markup" {
				var kb struct {
					InlineKeyboard [][]any `json:"inline_keyboard"`
				}
				if json.Unmarshal([]byte(v), &kb) == nil && len(kb.InlineKeyboard) == 0 {
					continue
				}
			}
			if k == "text" && c.Params["chat_id"] == "999" {
				if i := strings.Index(v, "<b>Update:</b>"); i >= 0 {
					v = v[:i]
				}
			}
			m[k] = normalizeParam(k, v)
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

// userState è {"luce": ..., "gas": ...} di load_user, o null.
func userState(store *database.Store, id string) []byte {
	u := store.LoadUser(id)
	if u == nil {
		return []byte("null")
	}
	raw, _ := json.Marshal(map[string]any{"luce": u.Luce, "gas": u.Gas})
	return raw
}

func TestGoldenE2EDispatcher(t *testing.T) {
	var scenarios []struct {
		Name  string    `json:"name"`
		Steps []e2eStep `json:"steps"`
	}
	loadHTTPGolden(t, "e2e_golden.json", &scenarios)
	_ = captureRecords(t)
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
				got, want := normalizeCalls(goCalls(h.fc.Calls())), normalizeCalls(step.Calls)
				for _, c := range want {
					if _, ok := c["text"]; ok || c["method"] == "answerCallbackQuery" {
						compared++
					}
				}
				if !reflect.DeepEqual(got, want) {
					gotJSON, _ := json.MarshalIndent(got, "", " ")
					wantJSON, _ := json.MarshalIndent(want, "", " ")
					t.Errorf("passo %d (%s): chiamate diverse\nGo:     %s\nPython: %s", i, step.Update, gotJSON, wantJSON)
				}
			}
		})
	}
}
