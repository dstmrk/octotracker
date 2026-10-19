package scraper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("data_reader")

const (
	// OctopusPIVA è la P.IVA di Octopus Energy Italia, usata per filtrare le offerte.
	OctopusPIVA = "01771990445"
	// AreraBaseURL è la base degli URL dei file XML Open Data ARERA.
	AreraBaseURL = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML"
	// ServiceNameElectricity è il nome del servizio elettrico nei log.
	ServiceNameElectricity = "elettricità"
	// ServiceNameGas è il nome del servizio gas nei log.
	ServiceNameGas = "gas"
	// RequestTimeout è il tempo massimo senza ricevere dati durante un download.
	RequestTimeout = 30 * time.Second
	// DefaultMaxDaysBack è il numero di giorni indietro provati da
	// FetchOctopusTariffe se il file di oggi non è ancora pubblicato.
	DefaultMaxDaysBack = 7
)

// HTTPHeaders sono gli header HTTP inviati ad ARERA.
var HTTPHeaders = [][2]string{
	{"User-Agent", "Mozilla/5.0 (compatible; OctoTracker/1.0; +https://github.com/)"},
	{"Accept", "application/xml,text/xml;q=0.9,*/*;q=0.8"},
	{"Accept-Language", "it-IT,it;q=0.9,en;q=0.8"},
}

// RateSaver salva le tariffe lette (implementato da *database.Store).
type RateSaver interface {
	SaveRatesBatch(dataFonte string, rates []database.RateRecord) int
}

// Client legge i file XML Open Data ARERA. Lo zero value è pronto all'uso;
// i campi permettono di sostituire, nei test, l'URL base, il client HTTP,
// l'orologio e il timeout.
type Client struct {
	// BaseURL sostituisce AreraBaseURL se non vuoto.
	BaseURL string
	// HTTPClient è il client HTTP (nil = http.DefaultClient).
	HTTPClient *http.Client
	// Now è l'orologio (nil = time.Now).
	Now func() time.Time
	// Timeout sostituisce RequestTimeout se > 0.
	Timeout time.Duration
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return AreraBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return RequestTimeout
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// daysAgo restituisce la data di days giorni fa.
func (c *Client) daysAgo(days int) time.Time {
	return c.now().AddDate(0, 0, -days)
}

func formatDate(t time.Time) string {
	return t.Format(time.DateOnly)
}

// buildAreraURL costruisce l'URL del file del giorno; il path usa il mese
// senza zero iniziale (2026_1, non 2026_01):
//
//	https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2026_1/PO_Offerte_E_MLIBERO_20260123.xml
func (c *Client) buildAreraURL(date time.Time, service string) string {
	url := fmt.Sprintf("%s/%d_%d/PO_Offerte_%s_MLIBERO_%s.xml",
		c.baseURL(), date.Year(), int(date.Month()), service, date.Format("20060102"))
	logger.Debug("URL costruito: " + url)
	return url
}

// fetchXML scarica il file ed esegue il parsing in streaming; gli effetti
// del parsing (log e tariffe) vengono applicati dalla funzione restituita.
func (c *Client) fetchXML(ctx context.Context, url, service string) (func() (areraData, error), error) {
	var parsed *areraParse
	head, err := c.downloadXML(ctx, url, func(r io.Reader) {
		parsed = scanAreraXML(r, service)
	})
	if err != nil {
		return nil, err
	}
	return func() (areraData, error) { return parsed.result(head) }, nil
}

// fetchServiceData scarica il file del servizio partendo da oggi e
// andando indietro fino a maxDaysBack giorni, poi ne legge le tariffe.
// sourceDate è nil se nessun download è riuscito. Restituisce un errore
// solo se ctx viene annullato o se un'offerta interrompe la lettura.
func (c *Client) fetchServiceData(ctx context.Context, service string, maxDaysBack int) (areraData, *time.Time, error) {
	name := serviceName(service)
	for daysBack := 0; daysBack <= maxDaysBack; daysBack++ {
		date := c.daysAgo(daysBack)
		dateStr := formatDate(date)
		url := c.buildAreraURL(date, service)
		if daysBack == 0 {
			logger.Info(fmt.Sprintf("📄 Download dati %s (data: %s)...", name, dateStr))
		} else {
			logger.Debug(fmt.Sprintf("   Tentativo %s con data: %s...", name, dateStr))
		}

		result, err := c.fetchXML(ctx, url, service)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return areraData{}, nil, ctxErr
		}
		if err == nil {
			logger.Info(fmt.Sprintf("✅ File %s scaricato (data: %s)", name, dateStr))
			parsed, err := result()
			if err != nil {
				return areraData{}, nil, err
			}
			return parsed, &date, nil
		}
		// Gli errori HTTP sono già stati loggati dal download.
		var statusErr *httpStatusError
		if !errors.As(err, &statusErr) {
			logger.Debug(fmt.Sprintf("⚠️  Errore download %s per %s: %v", name, dateStr, err))
		}
	}
	logger.Warn(fmt.Sprintf("⚠️  Nessun dato %s disponibile (download fallito)", name))
	return areraData{}, nil, nil
}

// FetchOctopusTariffe usa il Client di default (vedi Client.FetchOctopusTariffe).
func FetchOctopusTariffe(ctx context.Context, store RateSaver, maxDaysBack int) (*Tariffe, error) {
	return (&Client{}).FetchOctopusTariffe(ctx, store, maxDaysBack)
}

// FetchOctopusTariffe legge le tariffe Octopus dai file Open Data ARERA
// (luce, poi gas, ciascuno provando fino a maxDaysBack giorni indietro) e,
// se almeno una delle 5 tariffe principali è stata trovata, le salva con
// store.SaveRatesBatch sotto la data del file più recente.
//
// Download falliti e XML malformati non sono errori: vengono loggati e
// danno tariffe vuote. Restituisce un errore se ctx viene annullato (il
// chiamante riconosce il timeout con errors.Is(err,
// context.DeadlineExceeded)) o se un'offerta Octopus non è leggibile (es.
// PREZZO non numerico): in quel caso non salva nulla.
func (c *Client) FetchOctopusTariffe(ctx context.Context, store RateSaver, maxDaysBack int) (*Tariffe, error) {
	startTime := time.Now()
	logger.Info("🔍 Avvio lettura tariffe Octopus da ARERA Open Data...")

	dataLuce, dateLuce, err := c.fetchServiceData(ctx, "E", maxDaysBack)
	if err != nil {
		return nil, err
	}
	dataGas, dateGas, err := c.fetchServiceData(ctx, "G", maxDaysBack)
	if err != nil {
		return nil, err
	}

	tariffe := &Tariffe{
		Luce:              dataLuce.get("luce"),
		Gas:               dataGas.get("gas"),
		DataAggiornamento: formatDate(c.now()),
		DataFonteXML:      latestDate(dateLuce, dateGas),
	}
	totalFound := tariffe.mainRatesFound()
	duration := time.Since(startTime).Seconds()

	if totalFound == 0 {
		logger.Warn(fmt.Sprintf("⚠️  Lettura ARERA completata in %.2fs - Nessuna tariffa trovata", duration))
		return tariffe, nil
	}

	// Salva solo se almeno una tariffa principale è stata trovata.
	flatRates := flattenRates(tariffe.Luce, tariffe.Gas)
	dataFonte := tariffe.DataAggiornamento
	if tariffe.DataFonteXML != nil {
		dataFonte = *tariffe.DataFonteXML
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	inserted := store.SaveRatesBatch(dataFonte, flatRates)
	// Se il tempo è scaduto durante il salvataggio il risultato viene
	// scartato, senza i log finali.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	logSaved(duration, totalFound, inserted, len(flatRates))
	return tariffe, nil
}

// latestDate restituisce la più recente delle due date (nil se mancano entrambe).
func latestDate(a, b *time.Time) *string {
	if a == nil || (b != nil && b.After(*a)) {
		a = b
	}
	if a == nil {
		return nil
	}
	s := formatDate(*a)
	return &s
}

// mainRatesFound conta le 5 tariffe principali trovate: luce fissa
// monoraria, luce variabile monoraria e trioraria, gas fisso e variabile.
func (t *Tariffe) mainRatesFound() int {
	n := 0
	for _, found := range []bool{
		hasFascia(t.Luce.Fissa, "monoraria"),
		hasFascia(t.Luce.Variabile, "monoraria"),
		hasFascia(t.Luce.Variabile, "trioraria"),
		hasFascia(t.Gas.Fissa, "monoraria"),
		hasFascia(t.Gas.Variabile, "monoraria"),
	} {
		if found {
			n++
		}
	}
	return n
}

func hasFascia(f Fasce, fascia string) bool {
	_, ok := f.Get(fascia)
	return ok
}

// logSaved logga l'esito del salvataggio (inserted -1 = errore del database).
func logSaved(duration float64, totalFound, inserted, total int) {
	switch {
	case inserted == -1:
		logger.Error(fmt.Sprintf("❌ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe MA ERRORE SALVATAGGIO DB", duration, totalFound))
	case inserted < total:
		logger.Info(fmt.Sprintf("✅ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe", duration, totalFound))
		logger.Info(fmt.Sprintf("💾 %d/%d tariffe salvate (resto già presenti)", inserted, total))
	default:
		logger.Info(fmt.Sprintf("✅ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe", duration, totalFound))
		logger.Info(fmt.Sprintf("💾 %d tariffe salvate nel database", inserted))
	}
}

// RunScrape usa il Client di default (vedi Client.RunScrape).
func RunScrape(ctx context.Context, store RateSaver) error {
	return (&Client{}).RunScrape(ctx, store)
}

// RunScrape esegue il sottocomando `octotracker scrape`: legge e salva le
// tariffe con FetchOctopusTariffe(ctx, store, DefaultMaxDaysBack) e logga
// il risultato in JSON.
func (c *Client) RunScrape(ctx context.Context, store RateSaver) error {
	result, err := c.FetchOctopusTariffe(ctx, store, DefaultMaxDaysBack)
	if err != nil {
		return err
	}
	logger.Info("📊 Tariffe estratte:")
	logger.Info(result.JSONIndent())
	return nil
}
