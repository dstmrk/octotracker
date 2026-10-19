package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("api.handlers")

// Messaggi di errore comuni.
const (
	ErrMissingAuthHeader = "Missing X-Telegram-Init-Data header"
	ErrInternalServer    = "Internal server error"
)

// Percorsi degli endpoint della Mini App.
const (
	PathRatesHistory = "/api/rates/history"
	PathRatesCurrent = "/api/rates/current"
	PathUserRates    = "/api/user/rates"
)

// Valori ammessi dei parametri di /api/rates/history, in ordine alfabetico
// come nei messaggi "Must be one of".
var (
	ValidServizi = []string{"gas", "luce"}
	ValidTipi    = []string{"fissa", "variabile"}
	ValidFasce   = []string{"bioraria", "monoraria", "trioraria"}
)

// historyParams sono i parametri obbligatori di /api/rates/history, nell'ordine
// in cui vengono validati.
var historyParams = []struct {
	name  string
	valid []string
}{{"servizio", ValidServizi}, {"tipo", ValidTipi}, {"fascia", ValidFasce}}

// Default e limiti del parametro days di /api/rates/history.
const (
	DefaultDays = 365
	MaxDays     = 3650 // 10 anni
)

// Store è la parte del database usata dagli handler (*database.Store la
// implementa).
type Store interface {
	GetRateHistory(servizio, tipo, fascia string, days int, includeCommercializzazione, includeStats bool) database.RateHistory
	GetCurrentRates() database.CurrentRates
	GetLatestRateDate() *string
	LoadUser(userID string) *database.User
}

// API contiene gli handler REST della Mini App.
type API struct {
	store    Store
	botToken string
}

// New crea gli handler: botToken è TELEGRAM_BOT_TOKEN, usato per validare
// initData (vuoto → ogni richiesta autenticata riceve 401).
func New(store Store, botToken string) *API {
	return &API{store: store, botToken: botToken}
}

// Register registra i tre endpoint (percorsi esatti).
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle(PathRatesHistory, a.RatesHistoryHandler())
	mux.Handle(PathRatesCurrent, a.RatesCurrentHandler())
	mux.Handle(PathUserRates, a.UserRatesHandler())
}

// RatesHistoryHandler è GET /api/rates/history.
func (a *API) RatesHistoryHandler() http.Handler {
	return a.handler("RatesHistoryHandler", a.ratesHistory)
}

// RatesCurrentHandler è GET /api/rates/current.
func (a *API) RatesCurrentHandler() http.Handler {
	return a.handler("RatesCurrentHandler", a.ratesCurrent)
}

// UserRatesHandler è GET /api/user/rates.
func (a *API) UserRatesHandler() http.Handler { return a.handler("UserRatesHandler", a.userRates) }

// endpoint calcola la risposta di una GET già autenticata: status e corpo
// da serializzare in JSON.
type endpoint func(r *http.Request, auth InitData) (status int, body any)

// dataBody è la risposta di successo {"success": true, "data": ...}.
type dataBody struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

// errorBody è la risposta di errore {"success": false, "error": ...}.
type errorBody struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func ok(data any) (int, any) { return http.StatusOK, dataBody{Success: true, Data: data} }

func fail(status int, message string) (int, any) { return status, errorBody{Error: message} }

// handler applica a un endpoint ciò che è comune a tutti: header CORS,
// preflight OPTIONS (204 senza corpo), solo GET (altrimenti 405) e
// risposta JSON.
func (a *API) handler(name string, get endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS per Mini App Telegram
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Headers", "X-Telegram-Init-Data, Content-Type")
		h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		switch r.Method {
		case http.MethodGet:
			status, body := a.respond(name, r, get)
			h.Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		}
	})
}

// respond autentica la richiesta, esegue get e serializza la risposta:
// header assente → 401 con ErrMissingAuthHeader, initData non valido → 401
// con il messaggio dell'errore, panic o valore non serializzabile (es.
// NaN) → log e 500.
func (a *API) respond(name string, r *http.Request, get endpoint) (status int, body []byte) {
	defer func() {
		if rec := recover(); rec != nil {
			status, body = internalError(name, rec)
		}
	}()
	status, v := a.authenticated(r, get)
	body, err := json.Marshal(v)
	if err != nil {
		return internalError(name, err)
	}
	return status, body
}

// authenticated valida X-Telegram-Init-Data e, se valido, esegue get.
func (a *API) authenticated(r *http.Request, get endpoint) (int, any) {
	// più header vengono uniti con "," (RFC 9110): la firma non è più valida
	initData := strings.Join(r.Header.Values("X-Telegram-Init-Data"), ",")
	if initData == "" {
		return fail(http.StatusUnauthorized, ErrMissingAuthHeader)
	}
	auth, err := ValidateInitData(initData, a.botToken, DefaultMaxAgeSeconds)
	if err != nil {
		logger.Warn("Auth error: " + err.Error())
		return fail(http.StatusUnauthorized, err.Error())
	}
	return get(r, auth)
}

// internalError logga l'errore inatteso dell'handler name e restituisce
// la risposta 500.
func internalError(name string, cause any) (int, []byte) {
	logger.Error(fmt.Sprintf("Unexpected error in %s: %v", name, cause))
	body, _ := json.Marshal(errorBody{Error: ErrInternalServer})
	return http.StatusInternalServerError, body
}

// lastValue restituisce l'ultimo valore del parametro senza spazi ai lati
// ("" se assente).
func lastValue(q url.Values, name string) string {
	v := q[name]
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[len(v)-1])
}

// parseDays valida days: non numerico (o fuori dal range di int) o < 1 →
// DefaultDays, > MaxDays → MaxDays.
func parseDays(s string) int {
	days, err := strconv.Atoi(s)
	switch {
	case err != nil, days < 1:
		return DefaultDays
	case days > MaxDays:
		return MaxDays
	}
	return days
}

// ratesHistory è GET /api/rates/history: query servizio, tipo, fascia
// (obbligatori) e days (default 365, massimo 3650).
func (a *API) ratesHistory(r *http.Request, auth InitData) (int, any) {
	q := r.URL.Query()
	values := make([]string, len(historyParams))
	for i, p := range historyParams {
		v := lastValue(q, p.name)
		if v == "" {
			return fail(http.StatusBadRequest, "Missing required parameter: "+p.name)
		}
		if !slices.Contains(p.valid, v) {
			return fail(http.StatusBadRequest, fmt.Sprintf("Invalid %s: %s. Must be one of: {'%s'}",
				p.name, v, strings.Join(p.valid, "', '")))
		}
		values[i] = v
	}
	servizio, tipo, fascia := values[0], values[1], values[2]
	days := parseDays(lastValue(q, "days"))

	logger.Info(fmt.Sprintf("Rate history request: user=%s, %s/%s/%s, days=%d", auth.UserID(), servizio, tipo, fascia, days))
	return ok(a.store.GetRateHistory(servizio, tipo, fascia, days, true, true))
}

// ratesCurrent è GET /api/rates/current: le tariffe più recenti, con la
// loro data in "date".
func (a *API) ratesCurrent(_ *http.Request, auth InitData) (int, any) {
	rates := a.store.GetCurrentRates()
	if len(rates) == 0 {
		return fail(http.StatusNotFound, "No rates found")
	}
	data := map[string]any{"date": a.store.GetLatestRateDate()}
	for servizio, byTipo := range rates {
		data[servizio] = byTipo
	}

	logger.Info("Current rates request: user=" + auth.UserID())
	return ok(data)
}

// userRates è GET /api/user/rates: le tariffe salvate dell'utente (non
// gli altri campi come last_notified_rates); "gas" è null se assente.
func (a *API) userRates(_ *http.Request, auth InitData) (int, any) {
	userID := auth.UserID()
	user := a.store.LoadUser(userID)
	if user == nil {
		return fail(http.StatusNotFound, "User not found: "+userID)
	}

	logger.Info("User rates request: user=" + userID)
	return ok(struct {
		Luce database.Utility  `json:"luce"`
		Gas  *database.Utility `json:"gas"`
	}{user.Luce, user.Gas})
}
