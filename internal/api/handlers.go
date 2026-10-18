package api

import (
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"

	"github.com/dstmrk/octotracker/internal/api/pycompat"
	"github.com/dstmrk/octotracker/internal/api/tornado"
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

// Valori validi per la validazione dei parametri (VALID_SERVIZI, VALID_TIPI,
// VALID_FASCE).
var (
	ValidServizi = []string{"luce", "gas"}
	ValidTipi    = []string{"fissa", "variabile"}
	ValidFasce   = []string{"monoraria", "bioraria", "trioraria"}
)

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

// API contiene gli handler REST della Mini App (api/handlers.py).
type API struct {
	store    Store
	botToken string
}

// New crea gli handler: botToken è TELEGRAM_BOT_TOKEN, usato per validare
// initData (vuoto → ogni richiesta autenticata riceve 401).
func New(store Store, botToken string) *API {
	return &API{store: store, botToken: botToken}
}

// Register registra i tre endpoint (percorsi esatti, come le route Tornado).
func (a *API) Register(mux *http.ServeMux) {
	mux.Handle(PathRatesHistory, a.RatesHistoryHandler())
	mux.Handle(PathRatesCurrent, a.RatesCurrentHandler())
	mux.Handle(PathUserRates, a.UserRatesHandler())
}

// setDefaultHeaders replica BaseAPIHandler.set_default_headers.
func setDefaultHeaders(h *tornado.RequestHandler) {
	h.SetHeader("Content-Type", "application/json")
	// CORS per Mini App Telegram
	h.SetHeader("Access-Control-Allow-Origin", "*")
	h.SetHeader("Access-Control-Allow-Headers", "X-Telegram-Init-Data, Content-Type")
	h.SetHeader("Access-Control-Allow-Methods", "GET, OPTIONS")
}

// options replica BaseAPIHandler.options: preflight CORS → 204.
func options(h *tornado.RequestHandler) error {
	h.SetStatus(http.StatusNoContent)
	h.Finish()
	return nil
}

func (a *API) handler(name string, get func(h *tornado.RequestHandler) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tornado.Serve(w, r, setDefaultHeaders, tornado.Methods{
			http.MethodGet: func(h *tornado.RequestHandler) error {
				a.guard(h, name, get)
				return nil
			},
			http.MethodOptions: options,
		})
	})
}

// RatesHistoryHandler è GET /api/rates/history (RatesHistoryHandler).
func (a *API) RatesHistoryHandler() http.Handler {
	return a.handler("RatesHistoryHandler", a.ratesHistoryGet)
}

// RatesCurrentHandler è GET /api/rates/current (RatesCurrentHandler).
func (a *API) RatesCurrentHandler() http.Handler {
	return a.handler("RatesCurrentHandler", a.ratesCurrentGet)
}

// UserRatesHandler è GET /api/user/rates (UserRatesHandler).
func (a *API) UserRatesHandler() http.Handler { return a.handler("UserRatesHandler", a.userRatesGet) }

// panicError converte il valore di un panic nell'errore equivalente.
func panicError(r any) error {
	if err, ok := r.(error); ok {
		return err
	}
	return fmt.Errorf("%v", r)
}

// guard replica i blocchi except comuni ai tre get(): TelegramAuthError →
// 401 con il messaggio, ogni altra eccezione (anche un panic) → log e 500.
func (a *API) guard(h *tornado.RequestHandler, name string, get func(h *tornado.RequestHandler) error) {
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = panicError(r)
			}
		}()
		return get(h)
	}()
	if err == nil {
		return
	}
	var authErr *TelegramAuthError
	if errors.As(err, &authErr) {
		logger.Warn(fmt.Sprintf("Auth error: %s", authErr.Error()))
		writeErrorJSON(h, authErr.Error(), http.StatusUnauthorized)
		return
	}
	// logger.exception: il traceback Python contiene già l'errore nel messaggio
	logger.Error(fmt.Sprintf("Unexpected error in %s: %s", name, err.Error()))
	writeErrorJSON(h, ErrInternalServer, http.StatusInternalServerError)
}

// writeJSON replica write_json: status e json.dumps(data).
func writeJSON(h *tornado.RequestHandler, data any, status int) error {
	body, err := pycompat.Dumps(data)
	if err != nil {
		return err
	}
	h.SetStatus(status)
	h.Write(body)
	return nil
}

// writeErrorJSON replica write_error_json: {"success": false, "error": ...}.
func writeErrorJSON(h *tornado.RequestHandler, message string, status int) {
	h.SetStatus(status)
	h.Write(pycompat.MustDumps(pycompat.Object{{Key: "success", Value: false}, {Key: "error", Value: message}}))
}

// getAuthData replica get_auth_data: nil se l'header X-Telegram-Init-Data
// manca o è vuoto, altrimenti validate_init_data (TelegramAuthError se non
// valido). Più header con lo stesso nome vengono uniti con "," e i byte
// letti come latin-1, come fa Tornado.
func (a *API) getAuthData(h *tornado.RequestHandler) (InitData, error) {
	initData := pycompat.Latin1(strings.Join(h.Request.Header.Values("X-Telegram-Init-Data"), ","))
	if initData == "" {
		return nil, nil
	}
	return ValidateInitData(initData, a.botToken, DefaultMaxAgeSeconds)
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// parseDays replica la validazione di days: int(days_str), < 1 → 365,
// > 3650 → 3650, ValueError → 365.
func parseDays(daysStr string) int {
	v, err := pycompat.ParseInt(daysStr)
	switch {
	case err != nil:
		return DefaultDays
	case v.Cmp(big.NewInt(1)) < 0:
		return DefaultDays
	case v.Cmp(big.NewInt(MaxDays)) > 0:
		return MaxDays
	}
	return int(v.Int64())
}

// validateParam replica i controlli "Missing required parameter" e
// "Invalid ...: x. Must be one of: {...}".
func validateParam(h *tornado.RequestHandler, name string, value *string, valid []string) bool {
	if value == nil || *value == "" {
		writeErrorJSON(h, "Missing required parameter: "+name, http.StatusBadRequest)
		return false
	}
	if !contains(valid, *value) {
		writeErrorJSON(h, fmt.Sprintf("Invalid %s: %s. Must be one of: %s", name, *value, pycompat.SetRepr(valid)), http.StatusBadRequest)
		return false
	}
	return true
}

// ratesHistoryGet replica RatesHistoryHandler.get.
//
// Query params: servizio ("luce"/"gas"), tipo ("fissa"/"variabile"), fascia
// ("monoraria"/"bioraria"/"trioraria"), days (default 365, massimo 3650).
func (a *API) ratesHistoryGet(h *tornado.RequestHandler) error {
	authData, err := a.getAuthData(h)
	if err != nil {
		return err
	}
	if authData == nil {
		writeErrorJSON(h, ErrMissingAuthHeader, http.StatusUnauthorized)
		return nil
	}

	servizio, err := h.GetArgument("servizio", nil)
	if err != nil {
		return err
	}
	tipo, err := h.GetArgument("tipo", nil)
	if err != nil {
		return err
	}
	fascia, err := h.GetArgument("fascia", nil)
	if err != nil {
		return err
	}
	defaultDays := "365"
	daysStr, err := h.GetArgument("days", &defaultDays)
	if err != nil {
		return err
	}

	if !validateParam(h, "servizio", servizio, ValidServizi) ||
		!validateParam(h, "tipo", tipo, ValidTipi) ||
		!validateParam(h, "fascia", fascia, ValidFasce) {
		return nil
	}
	days := parseDays(*daysStr)

	userID := authData.UserID()
	logger.Info(fmt.Sprintf("Rate history request: user=%s, %s/%s/%s, days=%d", userID, *servizio, *tipo, *fascia, days))

	history := a.store.GetRateHistory(*servizio, *tipo, *fascia, days, true, true)
	return writeJSON(h, pycompat.Object{{Key: "success", Value: true}, {Key: "data", Value: rateHistoryObject(history)}}, http.StatusOK)
}

// ratesCurrentGet replica RatesCurrentHandler.get: le tariffe più recenti.
func (a *API) ratesCurrentGet(h *tornado.RequestHandler) error {
	authData, err := a.getAuthData(h)
	if err != nil {
		return err
	}
	if authData == nil {
		writeErrorJSON(h, ErrMissingAuthHeader, http.StatusUnauthorized)
		return nil
	}

	rates := a.store.GetCurrentRates()
	if rates == nil {
		writeErrorJSON(h, "No rates found", http.StatusNotFound)
		return nil
	}
	rateDate := a.store.GetLatestRateDate()

	userID := authData.UserID()
	logger.Info(fmt.Sprintf("Current rates request: user=%s", userID))

	data := pycompat.Object{{Key: "date", Value: rateDate}}
	data = append(data, currentRatesObject(rates)...)
	return writeJSON(h, pycompat.Object{{Key: "success", Value: true}, {Key: "data", Value: data}}, http.StatusOK)
}

// userRatesGet replica UserRatesHandler.get: le tariffe salvate dell'utente.
func (a *API) userRatesGet(h *tornado.RequestHandler) error {
	authData, err := a.getAuthData(h)
	if err != nil {
		return err
	}
	if authData == nil {
		writeErrorJSON(h, ErrMissingAuthHeader, http.StatusUnauthorized)
		return nil
	}

	userID := authData.UserID()
	user := a.store.LoadUser(userID)
	if user == nil {
		writeErrorJSON(h, fmt.Sprintf("User not found: %s", userID), http.StatusNotFound)
		return nil
	}

	logger.Info(fmt.Sprintf("User rates request: user=%s", userID))

	// Solo le tariffe (non altri campi come last_notified_rates)
	var gas any
	if user.Gas != nil {
		gas = utilityObject(*user.Gas)
	}
	data := pycompat.Object{{Key: "luce", Value: utilityObject(user.Luce)}, {Key: "gas", Value: gas}}
	return writeJSON(h, pycompat.Object{{Key: "success", Value: true}, {Key: "data", Value: data}}, http.StatusOK)
}

// ========== Conversione nei dict Python ==========

// utilityObject replica il dict "luce"/"gas" di _row_to_dict (chiavi
// opzionali assenti se non valorizzate).
func utilityObject(u database.Utility) pycompat.Object {
	o := pycompat.Object{
		{Key: "tipo", Value: u.Tipo},
		{Key: "fascia", Value: u.Fascia},
		{Key: "energia", Value: u.Energia},
		{Key: "commercializzazione", Value: u.Commercializzazione},
	}
	optional := []struct {
		key string
		v   *float64
	}{{"consumo_f1", u.ConsumoF1}, {"consumo_f2", u.ConsumoF2}, {"consumo_f3", u.ConsumoF3}, {"consumo_annuo", u.ConsumoAnnuo}}
	for _, f := range optional {
		if f.v != nil {
			o = append(o, pycompat.Member{Key: f.key, Value: *f.v})
		}
	}
	if u.Scadenza != "" {
		o = append(o, pycompat.Member{Key: "scadenza", Value: u.Scadenza})
	}
	return o
}

// rateHistoryObject replica il dict di get_rate_history (labels, data,
// period, commercializzazione e stats se presenti).
func rateHistoryObject(h database.RateHistory) pycompat.Object {
	labels := make([]any, len(h.Labels))
	for i, l := range h.Labels {
		labels[i] = l
	}
	data := make([]any, len(h.Data))
	for i, d := range h.Data {
		data[i] = d
	}
	o := pycompat.Object{
		{Key: "labels", Value: labels},
		{Key: "data", Value: data},
		{Key: "period", Value: pycompat.Object{{Key: "from", Value: h.Period.From}, {Key: "to", Value: h.Period.To}}},
	}
	if h.Commercializzazione != nil {
		comm := make([]any, len(h.Commercializzazione))
		for i, c := range h.Commercializzazione {
			if c != nil {
				comm[i] = *c
			}
		}
		o = append(o, pycompat.Member{Key: "commercializzazione", Value: comm})
	}
	if h.Stats != nil {
		o = append(o, pycompat.Member{Key: "stats", Value: pycompat.Object{
			{Key: "min", Value: h.Stats.Min}, {Key: "max", Value: h.Stats.Max}, {Key: "avg", Value: h.Stats.Avg},
		}})
	}
	return o
}

// fasciaOrder è l'ordine con cui vengono serializzate le fasce di
// /api/rates/current. In Python è l'ordine di inserimento delle righe nel
// DB (rowid), che CurrentRates non conserva: si usa l'ordine canonico,
// che è quello con cui lo scraper salva le fasce delle offerte ARERA nei
// casi normali (le fasce sconosciute seguono in ordine alfabetico).
var fasciaOrder = map[string]int{"monoraria": 0, "bioraria": 1, "trioraria": 2}

func sortedFasce(m map[string]database.Rate) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, iok := fasciaOrder[keys[i]]
		pj, jok := fasciaOrder[keys[j]]
		switch {
		case iok && jok:
			return pi < pj
		case iok != jok:
			return iok
		}
		return keys[i] < keys[j]
	})
	return keys
}

// currentRatesObject replica la struttura di get_current_rates: "luce" e
// "gas", ciascuno con "fissa" e "variabile"; ogni tariffa ha "energia" e,
// se non NULL, "commercializzazione" e "cod_offerta".
func currentRatesObject(rates database.CurrentRates) pycompat.Object {
	var out pycompat.Object
	for _, servizio := range []string{"luce", "gas"} {
		byTipo, ok := rates[servizio]
		if !ok {
			continue
		}
		var tipi pycompat.Object
		for _, tipo := range []string{"fissa", "variabile"} {
			byFascia, ok := byTipo[tipo]
			if !ok {
				continue
			}
			fasce := pycompat.Object{}
			for _, fascia := range sortedFasce(byFascia) {
				r := byFascia[fascia]
				rate := pycompat.Object{{Key: "energia", Value: r.Energia}}
				if r.Commercializzazione != nil {
					rate = append(rate, pycompat.Member{Key: "commercializzazione", Value: *r.Commercializzazione})
				}
				if r.CodOfferta != nil {
					rate = append(rate, pycompat.Member{Key: "cod_offerta", Value: *r.CodOfferta})
				}
				fasce = append(fasce, pycompat.Member{Key: fascia, Value: rate})
			}
			tipi = append(tipi, pycompat.Member{Key: tipo, Value: fasce})
		}
		out = append(out, pycompat.Member{Key: servizio, Value: tipi})
	}
	return out
}
