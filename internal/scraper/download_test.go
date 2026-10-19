package scraper

// Test Go-specifici: HTTP reale (httptest), timeout per singola lettura,
// annullamento del contesto, integrazione con database.Store ed entrypoint.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
)

func download(t *testing.T, c *Client, ctx context.Context, url string) (string, string, error) {
	t.Helper()
	var content bytes.Buffer
	head, err := c.downloadXML(ctx, url, func(r io.Reader) { _, _ = io.Copy(&content, r) })
	return content.String(), head, err
}

func TestDownloadSendsHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()
	if _, _, err := download(t, &Client{}, context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, h := range HTTPHeaders {
		if got.Get(h[0]) != h[1] {
			t.Errorf("%s = %q", h[0], got.Get(h[0]))
		}
	}
	if got.Get("Accept-Encoding") != "identity" {
		t.Errorf("Accept-Encoding = %q", got.Get("Accept-Encoding"))
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	logs := captureLogs(t)
	_, _, err := download(t, &Client{}, context.Background(), srv.URL+"/x.xml")
	var se *httpStatusError
	if !errors.As(err, &se) || se.code != 404 || err.Error() != "HTTP 404" {
		t.Fatalf("err = %#v", err)
	}
	if !logs.has("WARNING", "⚠️  HTTP 404 durante download "+srv.URL+"/x.xml") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadKeepsContentAndHead(t *testing.T) {
	body := "\ufeff<a>caffè</a>" + strings.Repeat(" ", 600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	logs := captureLogs(t)
	content, head, err := download(t, &Client{}, context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if content != body || head != body[:headBytes] {
		t.Errorf("content %q head %q", content, head)
	}
	if !logs.has("DEBUG", fmt.Sprintf("Downloaded %d bytes", len(body))) {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadInvalidUTF8IsDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<a>\xff</a>"))
	}))
	defer srv.Close()
	logs := captureLogs(t)
	_, _, err := download(t, &Client{}, context.Background(), srv.URL)
	if !errors.Is(err, errInvalidUTF8) {
		t.Fatalf("err = %v", err)
	}
	if logs.has("DEBUG", "Downloaded") || logs.has("WARNING", "") {
		t.Errorf("log = %v", logs.all())
	}
}

// chunkReader restituisce al massimo n byte per Read.
type chunkReader struct {
	r io.Reader
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(p) > c.n {
		p = p[:c.n]
	}
	return c.r.Read(p)
}

// Le sequenze multibyte spezzate tra due letture sono valide; quelle
// troncate a fine file o malformate no.
func TestBodyReaderChecksUTF8AcrossReads(t *testing.T) {
	for _, c := range []struct {
		in    string
		valid bool
	}{
		{"caffè €🐙", true},
		{"", true},
		{"abc\xe2\x82", false},
		{"abc\xff", false},
		{"\xc3(", false},
		{"\xed\xa0\x80", false}, // surrogato
		{"\xf0\x9f\x90", false},
	} {
		for _, n := range []int{1, 2, 3, 4096} {
			b := &bodyReader{r: &chunkReader{r: strings.NewReader(c.in), n: n}, onData: func() {}}
			if _, err := io.Copy(io.Discard, b); err != nil {
				t.Fatal(err)
			}
			if got := b.err == nil; got != c.valid {
				t.Errorf("%q (chunk %d): valido = %v, err = %v", c.in, n, got, b.err)
			}
		}
	}
}

// Il timeout vale per singola lettura: un download lento ma continuo non
// viene interrotto anche se dura più del timeout.
func TestDownloadTimeoutIsIdleNotTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 8; i++ {
			_, _ = fmt.Fprintf(w, "<a%d/>", i)
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
	}))
	defer srv.Close()
	c := &Client{Timeout: 150 * time.Millisecond}
	start := time.Now()
	content, _, err := download(t, c, context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("err = %v dopo %v", err, time.Since(start))
	}
	if !strings.HasPrefix(content, "<a0/>") || !strings.HasSuffix(content, "<a7/>") {
		t.Errorf("content = %q", content)
	}
}

func TestDownloadTimeoutBeforeHeaders(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	logs := captureLogs(t)
	_, _, err := download(t, &Client{Timeout: 50 * time.Millisecond}, context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "nessun dato ricevuto per 50ms") {
		t.Fatalf("err = %v", err)
	}
	if !logs.has("WARNING", "⚠️  Errore rete durante download "+srv.URL+": nessun dato ricevuto") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadTimeoutDuringBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<a>")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	logs := captureLogs(t)
	_, _, err := download(t, &Client{Timeout: 50 * time.Millisecond}, context.Background(), srv.URL)
	if err == nil || !strings.Contains(err.Error(), "nessun dato ricevuto") {
		t.Fatalf("err = %v", err)
	}
	// Gli errori durante la lettura del corpo non hanno il warning di rete.
	if logs.has("WARNING", "") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "<root>....")
	}))
	defer srv.Close()
	_, _, err := download(t, &Client{}, context.Background(), srv.URL)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String() + "/f.xml"
	l.Close()
	logs := captureLogs(t)
	_, _, err = download(t, &Client{}, context.Background(), url)
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("err = %v", err)
	}
	if !logs.has("WARNING", "⚠️  Errore rete durante download "+url+": ") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadInvalidURL(t *testing.T) {
	logs := captureLogs(t)
	_, _, err := download(t, &Client{}, context.Background(), "http://bad host/\x7f")
	if err == nil || logs.has("WARNING", "") {
		t.Errorf("err = %v, log = %v", err, logs.all())
	}
}

func TestDownloadContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logs := captureLogs(t)
	_, _, err := download(t, &Client{}, ctx, srv.URL)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if logs.has("WARNING", "") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadContextCanceledDuringBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<a>")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := download(t, &Client{}, ctx, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

// ========== Annullamento ==========

func TestFetchOctopusTariffeDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	client, _ := fakeClient(nil, frozenNow)
	res, err := client.FetchOctopusTariffe(ctx, &fakeStore{t: t}, DefaultMaxDaysBack)
	if !errors.Is(err, context.DeadlineExceeded) || res != nil {
		t.Errorf("res = %v, err = %v", res, err)
	}
}

func TestFetchOctopusTariffeCanceledDuringGas(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sample := readTestdata(t, "sample_arera.xml")
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(sample)}, frozenNow)
	client.HTTPClient.Transport = cancelOn{base: client.HTTPClient.Transport, url: todayURL("G"), cancel: cancel}
	_, err := client.FetchOctopusTariffe(ctx, &fakeStore{t: t}, DefaultMaxDaysBack)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// cancelOn annulla il contesto quando viene richiesto url.
type cancelOn struct {
	base   http.RoundTripper
	url    string
	cancel context.CancelFunc
}

func (c cancelOn) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() == c.url {
		c.cancel()
		return nil, context.Canceled
	}
	return c.base.RoundTrip(r)
}

type cancelingStore struct {
	fakeStore
	cancel context.CancelFunc
}

func (s *cancelingStore) SaveRatesBatch(df string, r []database.RateRecord) int {
	s.cancel()
	return s.fakeStore.SaveRatesBatch(df, r)
}

func TestFetchOctopusTariffeCanceledDuringSave(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sample := readTestdata(t, "sample_arera.xml")
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(sample)}, frozenNow)
	store := &cancelingStore{fakeStore: fakeStore{t: t, returns: []int{3}}, cancel: cancel}
	logs := captureLogs(t)
	res, err := client.FetchOctopusTariffe(ctx, store, 0)
	if !errors.Is(err, context.Canceled) || res != nil {
		t.Fatalf("res = %v, err = %v", res, err)
	}
	if len(store.calls) != 1 || logs.has("INFO", "Lettura ARERA completata") {
		t.Errorf("calls = %d, log = %v", len(store.calls), logs.all())
	}
}

// Le funzioni di package usano il Client di default (rete reale): con il
// contesto già annullato non parte alcuna connessione.
func TestPackageLevelEntryPointsUseDefaultClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FetchOctopusTariffe(ctx, &fakeStore{t: t}, DefaultMaxDaysBack); !errors.Is(err, context.Canceled) {
		t.Errorf("FetchOctopusTariffe: %v", err)
	}
	if err := RunScrape(ctx, &fakeStore{t: t}); !errors.Is(err, context.Canceled) {
		t.Errorf("RunScrape: %v", err)
	}
	if err := Backfill(ctx, &fakeStore{t: t}, DefaultBackfillOptions()); !errors.Is(err, context.Canceled) {
		t.Errorf("Backfill: %v", err)
	}
}

func TestBackfillCanceledDuringSleep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, _ := fakeClient(routesEveryDay(1, "E", mockRatesXML), frozenNow)
	store := &fakeStore{t: t, returns: []int{1}}
	logs := captureLogs(t)
	opts := BackfillOptions{Days: 1, Delay: 60, Client: client, Sleep: func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}}
	if err := Backfill(ctx, store, opts); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	// Come un KeyboardInterrupt: niente riepilogo.
	if logs.has("INFO", "Riepilogo") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestDefaultBackfillOptions(t *testing.T) {
	o := DefaultBackfillOptions()
	if o.Days != 365 || o.DryRun || o.Delay != 0.5 || o.Client != nil || o.Sleep != nil {
		t.Errorf("opts = %+v", o)
	}
}

func TestClientDefaults(t *testing.T) {
	c := &Client{}
	if c.baseURL() != AreraBaseURL || c.httpClient() != http.DefaultClient || c.timeout() != RequestTimeout {
		t.Error("default non applicati")
	}
	if d := time.Since(c.now()); d < 0 || d > time.Minute {
		t.Errorf("now = %v", d)
	}
	c = &Client{BaseURL: "http://x"}
	if got := c.buildAreraURL(frozenNow, "E"); got != "http://x/2026_1/PO_Offerte_E_MLIBERO_20260103.xml" {
		t.Errorf("url = %s", got)
	}
}

// Le date dei file sono quelle del fuso dell'orologio, non UTC.
func TestClientDatesUseLocalWallClock(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	c := &Client{Now: func() time.Time { return time.Date(2026, 1, 1, 0, 30, 0, 0, loc) }}
	if got := formatDate(c.daysAgo(0)); got != "2026-01-01" {
		t.Errorf("oggi = %s", got)
	}
	if got := formatDate(c.daysAgo(1)); got != "2025-12-31" {
		t.Errorf("ieri = %s", got)
	}
}

// ========== Integrazione con database.Store ==========

func openStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), database.DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestFetchOctopusTariffeWithDatabase(t *testing.T) {
	store := openStore(t)
	sample := readTestdata(t, "sample_arera.xml")
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(sample), todayURL("G"): bodyRoute(sample)}, frozenNow)

	logs := captureLogs(t)
	if _, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack); err != nil {
		t.Fatal(err)
	}
	if !logs.has("INFO", "💾 5 tariffe salvate nel database") {
		t.Errorf("log = %v", logs.messages("INFO"))
	}
	cur := store.GetCurrentRates()
	r, ok := cur.Get("luce", "variabile", "trioraria")
	if !ok || r.Energia != 0.0088 || *r.Commercializzazione != 72.0 || *r.CodOfferta != "000129ESVFL91XXXXXXXXOCTOFLEXv91" {
		t.Errorf("trioraria = %+v", r)
	}
	if d := store.GetLatestRateDate(); d == nil || *d != "2026-01-03" {
		t.Errorf("data_fonte = %v", d)
	}

	// Seconda lettura: tutte già presenti → salvataggio parziale (0/5).
	logs = captureLogs(t)
	if _, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack); err != nil {
		t.Fatal(err)
	}
	if !logs.has("INFO", "💾 0/5 tariffe salvate (resto già presenti)") {
		t.Errorf("log = %v", logs.messages("INFO"))
	}
}

func TestBackfillWithDatabase(t *testing.T) {
	store := openStore(t)
	sample := readTestdata(t, "sample_arera.xml")
	routes := routesEveryDay(2, "E", sample)
	for k, v := range routesEveryDay(2, "G", sample) {
		routes[k] = v
	}
	client, _ := fakeClient(routes, frozenNow)
	opts := BackfillOptions{Days: 2, Delay: 0, Client: client, Sleep: noSleep}

	logs := captureLogs(t)
	if err := Backfill(context.Background(), store, opts); err != nil {
		t.Fatal(err)
	}
	if !logs.has("INFO", "   Tariffe inserite: 15") {
		t.Errorf("log = %v", logs.messages("INFO"))
	}
	if n := len(store.GetRateHistoryDates()); n != 15 {
		t.Errorf("chiavi = %d", n)
	}
	// Rieseguito: tutti i giorni già presenti.
	logs = captureLogs(t)
	if err := Backfill(context.Background(), store, opts); err != nil {
		t.Fatal(err)
	}
	if !logs.has("INFO", "   Giorni saltati (già presenti): 3") || !logs.has("INFO", "   Tariffe inserite: 0") {
		t.Errorf("log = %v", logs.messages("INFO"))
	}
}

// ========== Entrypoint e logging standalone ==========

func TestRunScrape(t *testing.T) {
	sample := readTestdata(t, "sample_arera.xml")
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(sample), todayURL("G"): bodyRoute(sample)}, frozenNow)
	logs := captureLogs(t)
	if err := client.RunScrape(context.Background(), &fakeStore{t: t, returns: []int{5}}); err != nil {
		t.Fatal(err)
	}
	info := logs.messages("INFO")
	if len(info) < 2 || info[len(info)-2] != "📊 Tariffe estratte:" {
		t.Fatalf("log = %v", info)
	}
	last := info[len(info)-1]
	if !strings.HasPrefix(last, "{\n  \"luce\": {\n    \"fissa\": {\n      \"monoraria\": {\n        \"energia\": 0.1078,") ||
		!strings.HasSuffix(last, "\"data_fonte_xml\": \"2026-01-03\"\n}") {
		t.Errorf("json = %s", last)
	}
}

func TestRunScrapePropagatesOfferError(t *testing.T) {
	bad := testDoc(strings.Replace(testOffer("01", "01", "01", "", comm("72")), "<NOME_OFFERTA>Test</NOME_OFFERTA>", "", 1))
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(bad)}, frozenNow)
	if err := client.RunScrape(context.Background(), &fakeStore{t: t}); !errors.Is(err, errNoNomeOfferta) {
		t.Errorf("err = %v", err)
	}
}

// ========== Streaming ==========

// Un documento grande, letto da una pipe: si conservano solo le offerte
// Octopus, non i sottoalberi delle altre.
func TestScanLargeDocumentKeepsOnlyRelevantOffers(t *testing.T) {
	pr, pw := io.Pipe()
	const others = 20000
	go func() {
		_, _ = io.WriteString(pw, `<?xml version="1.0"?><ListaOfferteMercatoLibero xmlns="urn:x">`)
		other := strings.Replace(testOffer("01", "01", "01", "X", comm("72"), energia("0.2")), "01771990445", "00000000000", 1)
		for i := 0; i < others; i++ {
			_, _ = io.WriteString(pw, other)
		}
		_, _ = io.WriteString(pw, testOffer("01", "02", "03", "OCTO", comm("72"), energia("0.0088")))
		_, _ = io.WriteString(pw, "</ListaOfferteMercatoLibero>")
		pw.Close()
	}()
	p := scanAreraXML(pr, "E")
	if p.err != nil {
		t.Fatal(p.err)
	}
	if p.lower.count != others+1 || len(p.lower.outcomes) != 1 {
		t.Fatalf("count %d outcomes %d", p.lower.count, len(p.lower.outcomes))
	}
	res, err := p.result("")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := res.rates.Variabile.Get("trioraria"); !ok || *r.CodOfferta != "OCTO" {
		t.Errorf("res = %+v", res)
	}
}

func TestScanPropagatesReaderError(t *testing.T) {
	boom := errors.New("boom")
	err := scanXML(io.MultiReader(strings.NewReader("<a>"), errReader{boom}), func(string, int) bool { return false }, nil)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	// Errore di lettura dentro la dichiarazione XML.
	err = scanXML(io.MultiReader(strings.NewReader("<?xml version"), errReader{boom}), func(string, int) bool { return false }, nil)
	if !errors.Is(err, boom) {
		t.Errorf("err decl = %v", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// I controlli di struttura che encoding/xml non fa: radice unica, niente
// testo fuori dalla radice, niente dichiarazioni ENTITY.
func TestScanXMLStructureErrors(t *testing.T) {
	for doc, want := range map[string]error{
		"":                                   errNoRoot,
		"  \n ":                              errNoRoot,
		"<a/><b/>":                           errOutsideRoot,
		"<a/>x":                              errOutsideRoot,
		"x<a/>":                              errOutsideRoot,
		"<!DOCTYPE a [<!ENTITY e 'x'>]><a/>": errEntitiesForbidden,
		"<!ENTITY e 'x'><a/>":                errEntitiesForbidden,
	} {
		if _, err := parseElement(doc); !errors.Is(err, want) {
			t.Errorf("%q: err = %v, atteso %v", doc, err, want)
		}
	}
	for _, doc := range []string{
		"\ufeff<?xml version=\"1.0\"?>\n<a/>\n",
		"<?xml version='1.0' encoding='ISO-8859-1'?><a>caffè</a>",
		"<!-- c --><!DOCTYPE a><a/><?pi x?>",
	} {
		if _, err := parseElement(doc); err != nil {
			t.Errorf("%q: err = %v", doc, err)
		}
	}
}

func TestTariffeStringAndNonFiniteNumbers(t *testing.T) {
	tr := completeTariffe()
	assertSameJSON(t, tr.String(), tr.JSONIndent())
	if !strings.HasPrefix(fmt.Sprint(&tr), `{"luce":{"fissa":{"monoraria":{"energia":0.1078,`) {
		t.Errorf("String = %s", tr.String())
	}
	// Un prezzo "NaN" (accettato da strconv) non è serializzabile in JSON.
	tr.Gas.Fissa.set("monoraria", Rate{Energia: math.NaN()})
	if got := tr.String(); !strings.HasPrefix(got, "tariffe non serializzabili: ") {
		t.Errorf("String = %s", got)
	}
}
