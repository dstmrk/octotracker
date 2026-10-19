package api

// Port di tests/test_api_handlers.py (27 casi). I mock di Python
// (validate_init_data, get_rate_history, ...) sono sostituiti da initData
// firmati davvero e da uno Store finto che registra le chiamate e può
// simulare eccezioni.

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/dstmrk/octotracker/internal/api/apitest"
	"github.com/dstmrk/octotracker/internal/database"
)

type historyCall struct {
	servizio, tipo, fascia    string
	days                      int
	includeComm, includeStats bool
}

// fakeStore implementa Store per i test.
type fakeStore struct {
	history      database.RateHistory
	historyPanic error
	historyCalls []historyCall

	rates      database.CurrentRates
	ratesPanic error
	date       *string

	users     map[string]*database.User
	userPanic error
	userCalls []string
}

func (f *fakeStore) GetRateHistory(servizio, tipo, fascia string, days int, includeComm, includeStats bool) database.RateHistory {
	f.historyCalls = append(f.historyCalls, historyCall{servizio, tipo, fascia, days, includeComm, includeStats})
	if f.historyPanic != nil {
		panic(f.historyPanic)
	}
	return f.history
}

func (f *fakeStore) GetCurrentRates() database.CurrentRates {
	if f.ratesPanic != nil {
		panic(f.ratesPanic)
	}
	return f.rates
}

func (f *fakeStore) GetLatestRateDate() *string { return f.date }

func (f *fakeStore) LoadUser(userID string) *database.User {
	f.userCalls = append(f.userCalls, userID)
	if f.userPanic != nil {
		panic(f.userPanic)
	}
	return f.users[userID]
}

// validInit restituisce initData valido per userID, firmato con testBotToken.
func validInit(userID int64) string {
	return generateTestInitData(initDataOpts{userID: userID})
}

func do(t *testing.T, h http.Handler, method, target, initData string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if initData != "" {
		req.Header.Set("X-Telegram-Init-Data", initData)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type apiResponse struct {
	Success bool            `json:"success"`
	Error   *string         `json:"error"`
	Data    json.RawMessage `json:"data"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) apiResponse {
	t.Helper()
	var r apiResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("JSON non valido %q: %v", rec.Body.String(), err)
	}
	return r
}

func errorText(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	r := decode(t, rec)
	if r.Error == nil {
		t.Fatalf("manca error: %s", rec.Body.String())
	}
	return *r.Error
}

// assertJSON confronta il corpo con want come JSON decodificato (chiavi,
// valori e null; non formato né ordine delle chiavi).
func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var got, exp any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("JSON non valido %q: %v", rec.Body.String(), err)
	}
	if err := json.Unmarshal([]byte(want), &exp); err != nil {
		t.Fatalf("JSON atteso non valido: %v", err)
	}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("corpo = %s\natteso %s", rec.Body.String(), want)
	}
}

func history(t *testing.T, store *fakeStore, query, initData string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, New(store, testBotToken).RatesHistoryHandler(), http.MethodGet, PathRatesHistory+query, initData)
}

// ========== TestRatesHistoryHandler ==========

func TestRatesHistoryGetSuccess(t *testing.T) {
	from, to := "2025-01-01", "2025-01-02"
	store := &fakeStore{history: database.RateHistory{
		Labels: []string{"2025-01-01", "2025-01-02"},
		Data:   []float64{0.115, 0.116},
		Period: database.Period{From: &from, To: &to},
	}}
	rec := history(t, store, "?servizio=luce&tipo=fissa&fascia=monoraria&days=30", validInit(123456789))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	r := decode(t, rec)
	if !r.Success {
		t.Error("success false")
	}
	var data struct{ Labels []string }
	if err := json.Unmarshal(r.Data, &data); err != nil || strings.Join(data.Labels, ",") != "2025-01-01,2025-01-02" {
		t.Errorf("labels = %v (%v)", data.Labels, err)
	}
	assertJSON(t, rec, `{"success": true, "data": {"labels": ["2025-01-01", "2025-01-02"], "data": [0.115, 0.116], "period": {"from": "2025-01-01", "to": "2025-01-02"}}}`)
	if c := store.historyCalls; len(c) != 1 || c[0] != (historyCall{"luce", "fissa", "monoraria", 30, true, true}) {
		t.Errorf("chiamate = %+v", c)
	}
}

func TestRatesHistoryGetMissingAuthHeader(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=fissa&fascia=monoraria", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
	if r := decode(t, rec); r.Success || r.Error == nil || *r.Error != ErrMissingAuthHeader {
		t.Errorf("risposta = %s", rec.Body.String())
	}
}

func TestRatesHistoryGetInvalidAuth(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=fissa&fascia=monoraria", "invalid_data")
	if rec.Code != http.StatusUnauthorized || errorText(t, rec) != "Invalid Telegram authentication signature" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestRatesHistoryGetMissingServizio(t *testing.T) {
	rec := history(t, &fakeStore{}, "?tipo=fissa&fascia=monoraria", validInit(123456789))
	if rec.Code != http.StatusBadRequest || !strings.Contains(strings.ToLower(errorText(t, rec)), "servizio") {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestRatesHistoryGetInvalidServizio(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=acqua&tipo=fissa&fascia=monoraria", validInit(123456789))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if got := errorText(t, rec); got != "Invalid servizio: acqua. Must be one of: {'gas', 'luce'}" {
		t.Errorf("errore = %q", got)
	}
}

func TestRatesHistoryGetDefaultDays365(t *testing.T) {
	store := &fakeStore{}
	history(t, store, "?servizio=luce&tipo=fissa&fascia=monoraria", validInit(123456789))
	if len(store.historyCalls) != 1 || store.historyCalls[0].days != 365 {
		t.Errorf("chiamate = %+v", store.historyCalls)
	}
}

func TestRatesHistoryGetCustomDays(t *testing.T) {
	store := &fakeStore{}
	history(t, store, "?servizio=luce&tipo=fissa&fascia=monoraria&days=90", validInit(123456789))
	if len(store.historyCalls) != 1 || store.historyCalls[0].days != 90 {
		t.Errorf("chiamate = %+v", store.historyCalls)
	}
}

func TestRatesHistoryGetCORSHeaders(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=fissa&fascia=monoraria", validInit(123456789))
	want := map[string]string{
		"Content-Type":                 "application/json",
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Headers": "X-Telegram-Init-Data, Content-Type",
		"Access-Control-Allow-Methods": "GET, OPTIONS",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q; atteso %q", k, got, v)
		}
	}
}

// ========== TestRatesCurrentHandler ==========

func TestRatesCurrentGetSuccess(t *testing.T) {
	date := "2025-01-15"
	store := &fakeStore{
		rates: database.CurrentRates{
			"luce": {"fissa": {"monoraria": {Energia: 0.115}}},
			"gas":  {"fissa": {"monoraria": {Energia: 0.39}}},
		},
		date: &date,
	}
	rec := do(t, New(store, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(123456789))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	assertJSON(t, rec, `{"success": true, "data": {"date": "2025-01-15", "luce": {"fissa": {"monoraria": {"energia": 0.115}}}, "gas": {"fissa": {"monoraria": {"energia": 0.39}}}}}`)
}

func TestRatesCurrentGetNoRates(t *testing.T) {
	rec := do(t, New(&fakeStore{}, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(123456789))
	if rec.Code != http.StatusNotFound || decode(t, rec).Success || errorText(t, rec) != "No rates found" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

// ========== TestUserRatesHandler ==========

func TestUserRatesGetSuccess(t *testing.T) {
	store := &fakeStore{users: map[string]*database.User{
		"123456789": {Luce: database.Utility{Tipo: "variabile", Fascia: "monoraria", Energia: 0.090, Commercializzazione: 72.0}},
	}}
	rec := do(t, New(store, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(123456789))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	assertJSON(t, rec, `{"success": true, "data": {"luce": {"tipo": "variabile", "fascia": "monoraria", "energia": 0.09, "commercializzazione": 72.0}, "gas": null}}`)
	if len(store.userCalls) != 1 || store.userCalls[0] != "123456789" {
		t.Errorf("LoadUser chiamato con %v", store.userCalls)
	}
}

func TestUserRatesGetUserNotFound(t *testing.T) {
	rec := do(t, New(&fakeStore{}, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(999999))
	if rec.Code != http.StatusNotFound || decode(t, rec).Success || errorText(t, rec) != "User not found: 999999" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestUserRatesGetExtractsUserIDFromAuth(t *testing.T) {
	store := &fakeStore{users: map[string]*database.User{"987654321": {Luce: database.Utility{Tipo: "fissa"}}}}
	do(t, New(store, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(987654321))
	if len(store.userCalls) != 1 || store.userCalls[0] != "987654321" {
		t.Errorf("LoadUser chiamato con %v", store.userCalls)
	}
}

// ========== TestAPIErrorHandling ==========

func TestOptionsPreflight(t *testing.T) {
	a := New(&fakeStore{}, testBotToken)
	for _, h := range []http.Handler{a.RatesHistoryHandler(), a.RatesCurrentHandler(), a.UserRatesHandler()} {
		rec := do(t, h, http.MethodOptions, "/", "")
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Errorf("status %d, corpo %q", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Type") != "" || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("header = %v", rec.Header())
		}
	}
}

func TestMissingTipo(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce", validInit(123456789))
	if rec.Code != http.StatusBadRequest || errorText(t, rec) != "Missing required parameter: tipo" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidTipo(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=invalido", validInit(123456789))
	if rec.Code != http.StatusBadRequest || errorText(t, rec) != "Invalid tipo: invalido. Must be one of: {'fissa', 'variabile'}" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestMissingFascia(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=fissa", validInit(123456789))
	if rec.Code != http.StatusBadRequest || errorText(t, rec) != "Missing required parameter: fascia" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidFascia(t *testing.T) {
	rec := history(t, &fakeStore{}, "?servizio=luce&tipo=fissa&fascia=invalida", validInit(123456789))
	want := "Invalid fascia: invalida. Must be one of: {'bioraria', 'monoraria', 'trioraria'}"
	if rec.Code != http.StatusBadRequest || errorText(t, rec) != want {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestDaysBounds(t *testing.T) {
	tests := map[string]int{
		"-5": 365, "9999": 3650, "abc": 365, // i tre casi Python
		"0": 365, "1": 1, "3650": 3650, "3651": 3650, "": 365, "1.5": 365, "%2B7": 7, "%2030%20": 30,
		"3_0": 365, "99999999999999999999999": 365,
	}
	for days, want := range tests {
		store := &fakeStore{}
		history(t, store, "?servizio=luce&tipo=fissa&fascia=monoraria&days="+days, validInit(123456789))
		if len(store.historyCalls) != 1 || store.historyCalls[0].days != want {
			t.Errorf("days=%s → %+v; atteso %d", days, store.historyCalls, want)
		}
	}
}

func TestRatesCurrentAuthError(t *testing.T) {
	rec := do(t, New(&fakeStore{}, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, "invalid")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d", rec.Code)
	}
}

func TestRatesCurrentInternalError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	store := &fakeStore{ratesPanic: errors.New("DB crash")}
	rec := do(t, New(store, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(123456789))
	if rec.Code != http.StatusInternalServerError || decode(t, rec).Success || errorText(t, rec) != ErrInternalServer {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
	if !hasLog(logs.Take(), "api.handlers", "ERROR", "Unexpected error in RatesCurrentHandler: DB crash") {
		t.Error("log mancante")
	}
}

func TestRatesCurrentMissingAuth(t *testing.T) {
	rec := do(t, New(&fakeStore{}, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, "")
	if rec.Code != http.StatusUnauthorized || errorText(t, rec) != ErrMissingAuthHeader {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestUserRatesAuthError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	rec := do(t, New(&fakeStore{}, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, "invalid")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d", rec.Code)
	}
	if !hasLog(logs.Take(), "api.handlers", "WARNING", "Auth error: Invalid Telegram authentication signature") {
		t.Error("log mancante")
	}
}

func TestUserRatesInternalError(t *testing.T) {
	store := &fakeStore{userPanic: errors.New("DB crash")}
	rec := do(t, New(store, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(123456789))
	if rec.Code != http.StatusInternalServerError || decode(t, rec).Success {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}

func TestInternalServerError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	store := &fakeStore{historyPanic: errors.New("Unexpected error")}
	rec := history(t, store, "?servizio=luce&tipo=fissa&fascia=monoraria&days=30", validInit(123456789))
	if rec.Code != http.StatusInternalServerError || errorText(t, rec) != ErrInternalServer {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
	if !hasLog(logs.Take(), "api.handlers", "ERROR", "Unexpected error in RatesHistoryHandler: Unexpected error") {
		t.Error("log mancante")
	}
}

// ========== Casi aggiuntivi ==========

func hasLog(recs []apitest.Record, name, level, msg string) bool {
	for _, r := range recs {
		if r == (apitest.Record{name, level, msg}) {
			return true
		}
	}
	return false
}

func TestNonErrorPanicIsInternalError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	store := &fakeStore{}
	h := New(store, testBotToken).handler("RatesHistoryHandler", func(*http.Request, InitData) (int, any) { panic("boom") })
	rec := do(t, h, http.MethodGet, "/", validInit(1))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d", rec.Code)
	}
	if !hasLog(logs.Take(), "api.handlers", "ERROR", "Unexpected error in RatesHistoryHandler: boom") {
		t.Error("log mancante")
	}
}

// TestUnserializableIsInternalError: un valore che encoding/json non
// serializza (NaN) dà 500 come ogni altro errore inatteso.
func TestUnserializableIsInternalError(t *testing.T) {
	logs := apitest.CaptureLogs(t)
	store := &fakeStore{users: map[string]*database.User{"1": {Luce: database.Utility{Tipo: "fissa", Energia: math.NaN()}}}}
	rec := do(t, New(store, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(1))
	if rec.Code != http.StatusInternalServerError || errorText(t, rec) != ErrInternalServer {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
	if !hasLog(logs.Take(), "api.handlers", "ERROR", "Unexpected error in UserRatesHandler: json: unsupported value: NaN") {
		t.Error("log mancante")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := New(&fakeStore{}, testBotToken).RatesHistoryHandler()
	for _, m := range []string{http.MethodPost, http.MethodHead, http.MethodPut, http.MethodDelete, "FOO"} {
		rec := do(t, h, m, PathRatesHistory, validInit(1))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s: status %d, header %v", m, rec.Code, rec.Header())
		}
	}
}

func TestUserRatesFullUtility(t *testing.T) {
	f1, f2, f3, annuo := 1000.0, 800.25, 1200.0, 1200.5
	store := &fakeStore{users: map[string]*database.User{"1": {
		Luce: database.Utility{Tipo: "fissa", Fascia: "trioraria", Energia: 0.115, Commercializzazione: 96.5,
			ConsumoF1: &f1, ConsumoF2: &f2, ConsumoF3: &f3, Scadenza: "2026-12-31"},
		Gas: &database.Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.39, Commercializzazione: 84, ConsumoAnnuo: &annuo},
	}}}
	rec := do(t, New(store, testBotToken).UserRatesHandler(), http.MethodGet, PathUserRates, validInit(1))
	assertJSON(t, rec, `{"success": true, "data": {"luce": {"tipo": "fissa", "fascia": "trioraria", "energia": 0.115, "commercializzazione": 96.5, `+
		`"consumo_f1": 1000.0, "consumo_f2": 800.25, "consumo_f3": 1200.0, "scadenza": "2026-12-31"}, `+
		`"gas": {"tipo": "fissa", "fascia": "monoraria", "energia": 0.39, "commercializzazione": 84.0, "consumo_annuo": 1200.5}}}`)
}

func TestCurrentRatesAllFasce(t *testing.T) {
	comm := 72.0
	cod := "COD"
	date := "2025-01-15"
	store := &fakeStore{date: &date, rates: database.CurrentRates{
		"luce": {"fissa": {}, "variabile": {
			"trioraria": {Energia: 0.3}, "zeta": {Energia: 0.5}, "monoraria": {Energia: 0.1, Commercializzazione: &comm, CodOfferta: &cod},
			"alfa": {Energia: 0.4}, "bioraria": {Energia: 0.2},
		}},
		"gas": {"fissa": {}, "variabile": {}},
	}}
	rec := do(t, New(store, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(1))
	assertJSON(t, rec, `{"success": true, "data": {"date": "2025-01-15", "luce": {"fissa": {}, "variabile": {`+
		`"monoraria": {"energia": 0.1, "commercializzazione": 72.0, "cod_offerta": "COD"}, "bioraria": {"energia": 0.2}, `+
		`"trioraria": {"energia": 0.3}, "alfa": {"energia": 0.4}, "zeta": {"energia": 0.5}}}, "gas": {"fissa": {}, "variabile": {}}}}`)
	// data nil → "date": null
	store.date = nil
	rec = do(t, New(store, testBotToken).RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(1))
	assertJSON(t, rec, `{"success": true, "data": {"date": null, "luce": {"fissa": {}, "variabile": {`+
		`"monoraria": {"energia": 0.1, "commercializzazione": 72.0, "cod_offerta": "COD"}, "bioraria": {"energia": 0.2}, `+
		`"trioraria": {"energia": 0.3}, "alfa": {"energia": 0.4}, "zeta": {"energia": 0.5}}}, "gas": {"fissa": {}, "variabile": {}}}}`)
}

func TestRegister(t *testing.T) {
	mux := http.NewServeMux()
	New(&fakeStore{}, testBotToken).Register(mux)
	for _, p := range []string{PathRatesHistory, PathRatesCurrent, PathUserRates} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", p, rec.Code)
		}
	}
}

func TestEmptyBotTokenRejectsAll(t *testing.T) {
	rec := do(t, New(&fakeStore{}, "").RatesCurrentHandler(), http.MethodGet, PathRatesCurrent, validInit(1))
	if rec.Code != http.StatusUnauthorized || errorText(t, rec) != "Invalid Telegram authentication signature" {
		t.Errorf("status %d, corpo %s", rec.Code, rec.Body.String())
	}
}
