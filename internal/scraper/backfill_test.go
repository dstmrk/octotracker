package scraper

// Porting di tests/test_backfill.py: i mock di _download_xml/_parse_arera_xml
// e _download_and_parse_date diventano file XML serviti da un trasporto finto.

import (
	"context"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
)

var (
	backfillDate = time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	luceFissaXML = testDoc(testOffer("01", "01", "01", "", comm("72.0"), energia("0.1078")))
	gasFissaXML  = testDoc(testOffer("02", "01", "", "", comm("84.0"), energia("0.39")))
	// Una sola tariffa luce fissa monoraria a 0.10 (mock_rates dei test Python).
	mockRatesXML = testDoc(testOffer("01", "01", "01", "", energia("0.10")))
)

func urlFor(d time.Time, service string) string { return (&Client{}).buildAreraURL(d, service) }

// routesEveryDay serve body per il servizio indicato in ognuno dei giorni
// da oggi a oggi-days.
func routesEveryDay(days int, service, body string) map[string]route {
	routes := map[string]route{}
	for d := 0; d <= days; d++ {
		routes[urlFor(frozenNow.AddDate(0, 0, -d), service)] = bodyRoute(body)
	}
	return routes
}

// ===== TestDownloadAndParseDate =====

func TestDownloadAndParseDateSuccess(t *testing.T) {
	client, _ := fakeClient(map[string]route{
		urlFor(backfillDate, "E"): bodyRoute(luceFissaXML),
		urlFor(backfillDate, "G"): bodyRoute(gasFissaXML),
	}, frozenNow)

	result, err := client.downloadAndParseDate(context.Background(), backfillDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("len = %d", len(result))
	}
	var luce, gas []database.RateRecord
	for _, r := range result {
		switch r.Servizio {
		case "luce":
			luce = append(luce, r)
		case "gas":
			gas = append(gas, r)
		}
	}
	if len(luce) != 1 || len(gas) != 1 || luce[0].Energia != 0.1078 || gas[0].Energia != 0.39 {
		t.Errorf("luce = %+v, gas = %+v", luce, gas)
	}
}

func TestDownloadAndParseDateDownloadFails(t *testing.T) {
	refused := "refused"
	client, _ := fakeClient(map[string]route{
		urlFor(backfillDate, "E"): {URLError: &refused},
		urlFor(backfillDate, "G"): {URLError: &refused},
	}, frozenNow)

	result, err := client.downloadAndParseDate(context.Background(), backfillDate)
	// Deve ritornare lista vuota, non errore
	if err != nil || len(result) != 0 {
		t.Errorf("result = %+v, err = %v", result, err)
	}
}

// Download parziale (solo luce disponibile)
func TestDownloadAndParseDatePartialFailure(t *testing.T) {
	refused := "refused"
	client, _ := fakeClient(map[string]route{
		urlFor(backfillDate, "E"): bodyRoute(testDoc(testOffer("01", "01", "01", "", energia("0.1078")))),
		urlFor(backfillDate, "G"): {URLError: &refused},
	}, frozenNow)

	result, err := client.downloadAndParseDate(context.Background(), backfillDate)
	if err != nil {
		t.Fatal(err)
	}
	// Solo luce dovrebbe essere presente
	if len(result) != 1 || result[0].Servizio != "luce" {
		t.Errorf("result = %+v", result)
	}
}

// ===== TestBackfill =====

func backfillOpts(client *Client, days int, dryRun bool, delay float64) BackfillOptions {
	return BackfillOptions{Days: days, DryRun: dryRun, Delay: delay, Client: client, Sleep: noSleep}
}

func TestBackfillDryRun(t *testing.T) {
	client, _ := fakeClient(routesEveryDay(2, "E", mockRatesXML), frozenNow)
	store := &fakeStore{t: t} // Fatal se SaveRatesBatch viene chiamato

	if err := Backfill(context.Background(), store, backfillOpts(client, 2, true, 0)); err != nil {
		t.Fatal(err)
	}
	// In dry-run, save_rates_batch non deve essere chiamato
	if len(store.calls) != 0 {
		t.Errorf("save chiamato %d volte", len(store.calls))
	}
}

// Tariffe già presenti non vengono risalvate
func TestBackfillSkipsExistingKeys(t *testing.T) {
	today := formatDate(frozenNow)
	client, _ := fakeClient(routesEveryDay(0, "E", mockRatesXML), frozenNow)
	// La chiave (data, servizio, tipo, fascia) è già nel DB
	store := &fakeStore{t: t, existing: map[database.RateKey]struct{}{
		{DataFonte: today, Servizio: "luce", Tipo: "fissa", Fascia: "monoraria"}: {},
	}}

	if err := Backfill(context.Background(), store, backfillOpts(client, 0, false, 0)); err != nil {
		t.Fatal(err)
	}
	// Tutte le tariffe già presenti: save non deve essere chiamato
	if len(store.calls) != 0 {
		t.Errorf("save chiamato %d volte", len(store.calls))
	}
}

func TestBackfillSavesNewRates(t *testing.T) {
	client, _ := fakeClient(routesEveryDay(0, "E", mockRatesXML), frozenNow)
	store := &fakeStore{t: t, returns: []int{1}}

	if err := Backfill(context.Background(), store, backfillOpts(client, 0, false, 0)); err != nil {
		t.Fatal(err)
	}
	// save_rates_batch deve essere chiamato
	if len(store.calls) != 1 {
		t.Errorf("save chiamato %d volte", len(store.calls))
	}
}

// Gestione giorni senza dati
func TestBackfillHandlesEmptyDays(t *testing.T) {
	client, _ := fakeClient(nil, frozenNow)
	store := &fakeStore{t: t}

	if err := Backfill(context.Background(), store, backfillOpts(client, 2, false, 0)); err != nil {
		t.Fatal(err)
	}
	// Non deve salvare se non ci sono dati
	if len(store.calls) != 0 {
		t.Errorf("save chiamato %d volte", len(store.calls))
	}
}

// Un errore del database (-1) non interrompe il backfill
func TestBackfillHandlesSaveError(t *testing.T) {
	client, _ := fakeClient(routesEveryDay(1, "E", mockRatesXML), frozenNow)
	store := &fakeStore{t: t, returns: []int{-1, 1}}
	logs := captureLogs(t)

	if err := Backfill(context.Background(), store, backfillOpts(client, 1, false, 0)); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 2 || !logs.has("INFO", "   2026-01-02: -1 tariffe salvate") || !logs.has("INFO", "   Tariffe inserite: 0") {
		t.Errorf("calls = %d, log = %v", len(store.calls), logs.all())
	}
}

// Il delay viene rispettato
func TestBackfillRespectsDelay(t *testing.T) {
	client, _ := fakeClient(routesEveryDay(1, "E", mockRatesXML), frozenNow)
	store := &fakeStore{t: t, returns: []int{1, 1}}
	var sleeps []time.Duration
	opts := backfillOpts(client, 1, false, 0.5)
	opts.Sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}

	if err := Backfill(context.Background(), store, opts); err != nil {
		t.Fatal(err)
	}
	// sleep deve essere chiamato con il delay specificato (2 giorni: oggi e ieri)
	if len(sleeps) != 2 || sleeps[1] != 500*time.Millisecond {
		t.Errorf("sleep = %v", sleeps)
	}
}
