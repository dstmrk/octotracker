package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
	"github.com/dstmrk/octotracker/internal/logging"
)

var logger = logging.Logger("data_reader")

// Costanti di data_reader.py.
const (
	// OctopusPIVA è la P.IVA di Octopus Energy Italia, usata per filtrare le offerte.
	OctopusPIVA = "01771990445"
	// AreraBaseURL è la base degli URL dei file XML Open Data ARERA.
	AreraBaseURL = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML"
	// ServiceNameElectricity è il nome del servizio elettrico nei log.
	ServiceNameElectricity = "elettricità"
	// ServiceNameGas è il nome del servizio gas nei log.
	ServiceNameGas = "gas"
	// RequestTimeout è il timeout delle richieste HTTP (REQUEST_TIMEOUT = 30
	// secondi), con la semantica del timeout di socket di urllib.
	RequestTimeout = 30 * time.Second
	// DefaultMaxDaysBack è il default di max_days_back di fetch_octopus_tariffe.
	DefaultMaxDaysBack = 7
)

// HTTPHeaders sono gli header HTTP_HEADERS inviati a ARERA, nell'ordine Python.
var HTTPHeaders = [][2]string{
	{"User-Agent", "Mozilla/5.0 (compatible; OctoTracker/1.0; +https://github.com/)"},
	{"Accept", "application/xml,text/xml;q=0.9,*/*;q=0.8"},
	{"Accept-Language", "it-IT,it;q=0.9,en;q=0.8"},
}

// RateStore è la parte di *database.Store usata da FetchOctopusTariffe
// (save_rates_batch).
type RateStore interface {
	SaveRatesBatch(dataFonte string, rates []database.RateRecord) int
}

// Client legge i file XML Open Data ARERA. Lo zero value è pronto all'uso e
// si comporta come data_reader.py; i campi permettono di sostituire, nei
// test, l'URL base, il client HTTP, l'orologio e il timeout.
type Client struct {
	// BaseURL sostituisce AreraBaseURL se non vuoto.
	BaseURL string
	// HTTPClient è il client HTTP (nil = http.DefaultClient, che usa i proxy
	// dell'ambiente come urllib).
	HTTPClient *http.Client
	// Now è l'orologio locale (nil = time.Now), l'equivalente di datetime.now().
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

// clock restituisce l'istante corrente (time.time() per le durate).
func (c *Client) clock() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// now restituisce datetime.now(): l'ora locale come datetime "naive" (i
// campi data/ora locali in un time.Time UTC, così l'aritmetica sui giorni
// non risente dell'ora legale, come in Python).
func (c *Client) now() time.Time {
	t := c.clock()
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// subDays replica datetime - timedelta(days=days), con gli OverflowError di
// Python (timedelta oltre ±999999999 giorni, anno fuori da 1..9999).
func subDays(t time.Time, days int) (time.Time, error) {
	if days > 999999999 || days < -999999999 {
		return time.Time{}, newPyException("OverflowError", "days=%d; must have magnitude <= 999999999", days)
	}
	r := t.AddDate(0, 0, -days)
	if r.Year() < 1 || r.Year() > 9999 {
		return time.Time{}, newPyException("OverflowError", "date value out of range")
	}
	return r, nil
}

// formatDate replica strftime("%Y-%m-%d") (sep "-") o "%Y%m%d" (sep ""):
// come la glibc, l'anno non viene riempito di zeri sotto il 1000.
func formatDate(t time.Time, sep string) string {
	return fmt.Sprintf("%d%s%02d%s%02d", t.Year(), sep, int(t.Month()), sep, t.Day())
}

// buildAreraURL è _build_arera_url: il path usa il mese senza zero iniziale
// (es. 2026_1, non 2026_01).
//
//	https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2026_1/PO_Offerte_E_MLIBERO_20260123.xml
func (c *Client) buildAreraURL(date time.Time, service string) string {
	yearMonth := fmt.Sprintf("%d_%d", date.Year(), int(date.Month()))
	filename := fmt.Sprintf("PO_Offerte_%s_MLIBERO_%s.xml", service, formatDate(date, ""))
	url := fmt.Sprintf("%s/%s/%s", c.baseURL(), yearMonth, filename)
	logger.Debug("URL costruito: " + url)
	return url
}

// fetchXML scarica il file e ne esegue il parsing in streaming
// (_download_xml seguito da _parse_arera_xml): gli effetti del parsing
// vengono riprodotti dalla funzione result restituita, che il chiamante
// invoca dopo i propri log di download riuscito.
func (c *Client) fetchXML(ctx context.Context, url, service string) (func() (areraData, error), error) {
	var parsed *areraParse
	dl, err := c.downloadXML(ctx, url, func(r io.Reader) {
		parsed = scanAreraXML(r, service)
	})
	if err != nil {
		return nil, err
	}
	return func() (areraData, error) { return parsed.result(dl.head) }, nil
}

// fetchServiceData è _fetch_service_data: prova a scaricare il file del
// servizio partendo da oggi e andando indietro fino a maxDaysBack giorni,
// poi ne esegue il parsing. sourceDate è nil se nessun download è riuscito.
// Restituisce un errore solo se ctx viene annullato o se il parsing solleva
// un'eccezione non gestita (*PyException).
func (c *Client) fetchServiceData(ctx context.Context, service string, maxDaysBack int) (areraData, *time.Time, error) {
	name := serviceName(service)
	var result func() (areraData, error)
	var sourceDate *time.Time
	var targetDate time.Time

	// Prova a scaricare il file, partendo da oggi e andando indietro
	for daysBack := 0; daysBack < maxDaysBack+1; daysBack++ {
		attempt := func() error {
			t, err := subDays(c.now(), daysBack)
			if err != nil {
				return err
			}
			targetDate = t
			url := c.buildAreraURL(targetDate, service)

			dateStr := formatDate(targetDate, "-")
			if daysBack == 0 {
				logger.Info(fmt.Sprintf("📄 Download dati %s (data: %s)...", name, dateStr))
			} else {
				logger.Debug(fmt.Sprintf("   Tentativo %s con data: %s...", name, dateStr))
			}

			res, err := c.fetchXML(ctx, url, service)
			if err != nil {
				return err
			}
			result = res
			d := targetDate
			sourceDate = &d
			logger.Info(fmt.Sprintf("✅ File %s scaricato (data: %s)", name, dateStr))
			return nil
		}
		err := attempt()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return areraData{}, nil, ctxErr
		}
		if err == nil {
			break
		}
		// Log solo errori non HTTP
		if de, ok := err.(*downloadError); !ok || de.code == 0 {
			logger.Debug(fmt.Sprintf("⚠️  Errore download %s per %s: %v", name, formatDate(targetDate, "-"), err))
		}
	}

	if result == nil {
		logger.Warn(fmt.Sprintf("⚠️  Nessun dato %s disponibile (download fallito)", name))
		return areraData{}, nil, nil
	}

	// Parsea XML
	parsed, err := result()
	if err != nil {
		return areraData{}, nil, err
	}
	return parsed, sourceDate, nil
}

// FetchOctopusTariffe usa il Client di default (vedi Client.FetchOctopusTariffe).
func FetchOctopusTariffe(ctx context.Context, store RateStore, maxDaysBack int) (*Tariffe, error) {
	return (&Client{}).FetchOctopusTariffe(ctx, store, maxDaysBack)
}

// FetchOctopusTariffe porta fetch_octopus_tariffe: legge le tariffe Octopus
// dai file Open Data ARERA (luce, poi gas, ciascuno provando fino a
// maxDaysBack giorni indietro; il default Python è DefaultMaxDaysBack) e, se
// almeno una delle 5 tariffe principali è stata trovata, le salva con
// store.SaveRatesBatch sotto la data del file più recente.
//
// Gli errori di download e gli XML malformati non sono errori: vengono
// loggati e producono una struttura vuota, come in Python. Restituisce un
// errore solo se ctx viene annullato (bot.py usa asyncio.wait_for: il
// chiamante riconosce il timeout con errors.Is(err, context.DeadlineExceeded))
// oppure per le eccezioni che in Python risalgono non gestite (*PyException,
// es. AttributeError su un'offerta senza NOME_OFFERTA).
func (c *Client) FetchOctopusTariffe(ctx context.Context, store RateStore, maxDaysBack int) (*Tariffe, error) {
	startTime := c.clock()
	logger.Info("🔍 Avvio lettura tariffe Octopus da ARERA Open Data...")

	// Scarica e parsea dati luce e gas
	dataLuce, dateLuce, err := c.fetchServiceData(ctx, "E", maxDaysBack)
	if err != nil {
		return nil, err
	}
	dataGas, dateGas, err := c.fetchServiceData(ctx, "G", maxDaysBack)
	if err != nil {
		return nil, err
	}

	// Combina i risultati
	tariffe := &Tariffe{
		Luce:              dataLuce.get("luce"),
		Gas:               dataGas.get("gas"),
		DataAggiornamento: formatDate(c.now(), "-"),
	}

	// Aggiungi metadata: usa la data più recente
	if dateLuce != nil || dateGas != nil {
		var fonte time.Time
		switch {
		case dateLuce != nil && dateGas != nil:
			fonte = *dateLuce
			if dateGas.After(fonte) {
				fonte = *dateGas
			}
		case dateLuce != nil:
			fonte = *dateLuce
		default:
			fonte = *dateGas
		}
		s := formatDate(fonte, "-")
		tariffe.DataFonteXML = &s
	}

	// Conta tariffe trovate
	totalFound := 0
	for _, found := range []bool{
		hasFascia(tariffe.Luce.Fissa, "monoraria"),
		hasFascia(tariffe.Luce.Variabile, "monoraria"),
		hasFascia(tariffe.Luce.Variabile, "trioraria"),
		hasFascia(tariffe.Gas.Fissa, "monoraria"),
		hasFascia(tariffe.Gas.Variabile, "monoraria"),
	} {
		if found {
			totalFound++
		}
	}

	duration := c.clock().Sub(startTime).Seconds()

	// Salva risultati nel DB solo se almeno una tariffa è stata trovata
	if totalFound > 0 {
		flatRates := flattenRates(tariffe.Luce, tariffe.Gas)
		dataFonte := tariffe.DataAggiornamento
		if tariffe.DataFonteXML != nil {
			dataFonte = *tariffe.DataFonteXML
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		inserted := store.SaveRatesBatch(dataFonte, flatRates)
		// Come con asyncio.wait_for: se il tempo è scaduto durante il
		// salvataggio, il risultato viene scartato senza i log finali.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		switch {
		case inserted == -1:
			// Errore critico nel salvataggio DB
			logger.Error(fmt.Sprintf("❌ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe MA ERRORE SALVATAGGIO DB", duration, totalFound))
		case inserted < len(flatRates):
			// Salvataggio parziale (alcuni duplicati o errori minori)
			logger.Info(fmt.Sprintf("✅ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe", duration, totalFound))
			logger.Info(fmt.Sprintf("💾 %d/%d tariffe salvate (resto già presenti)", inserted, len(flatRates)))
		default:
			// Tutto salvato correttamente
			logger.Info(fmt.Sprintf("✅ Lettura ARERA completata in %.2fs - Trovate %d/5 tariffe", duration, totalFound))
			logger.Info(fmt.Sprintf("💾 %d tariffe salvate nel database", inserted))
		}
	} else {
		logger.Warn(fmt.Sprintf("⚠️  Lettura ARERA completata in %.2fs - Nessuna tariffa trovata", duration))
	}

	return tariffe, nil
}

// hasFascia replica bool(tipo_data.get(fascia)): il dict della tariffa
// prodotto dal parser non è mai vuoto.
func hasFascia(f Fasce, fascia string) bool {
	_, ok := f.Get(fascia)
	return ok
}

// RunScrape usa il Client di default (vedi Client.RunScrape).
func RunScrape(ctx context.Context, store RateStore) error {
	return (&Client{}).RunScrape(ctx, store)
}

// RunScrape porta il blocco __main__ di data_reader.py (sottocomando
// `octotracker scrape`): legge le tariffe con FetchOctopusTariffe(ctx,
// store, DefaultMaxDaysBack) e logga "📊 Tariffe estratte:" seguito da
// json.dumps(result, indent=2).
//
// Il chiamante deve prima configurare il logging come
// logging.basicConfig(level=LOG_LEVEL, format="%(message)s") (vedi
// NewBasicHandler) e aprire il database con database.Open, che esegue
// init_db().
func (c *Client) RunScrape(ctx context.Context, store RateStore) error {
	result, err := c.FetchOctopusTariffe(ctx, store, DefaultMaxDaysBack)
	if err != nil {
		return err
	}
	logger.Info("📊 Tariffe estratte:")
	logger.Info(result.JSONIndent())
	return nil
}
