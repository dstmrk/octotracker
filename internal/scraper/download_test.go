package scraper

// Test Go-specifici: HTTP reale (httptest), timeout con semantica di socket,
// annullamento del contesto, integrazione con database.Store, entrypoint e
// handler di logging degli script standalone.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dstmrk/octotracker/internal/database"
)

func download(t *testing.T, c *Client, ctx context.Context, url string) (string, downloaded, error) {
	t.Helper()
	var content bytes.Buffer
	dl, err := c.downloadXML(ctx, url, func(r io.Reader) { _, _ = io.Copy(&content, r) })
	return content.String(), dl, err
}

func TestDownloadSendsUrllibHeaders(t *testing.T) {
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
	var de *downloadError
	if !errors.As(err, &de) || de.code != 404 || de.msg != "HTTP Error 404: Not Found" {
		t.Fatalf("err = %#v", err)
	}
	if !logs.has("WARNING", "⚠️  HTTP 404 durante download "+srv.URL+"/x.xml") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadCountsCharactersAndKeepsBOM(t *testing.T) {
	body := "\ufeff<a>caffè</a>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	logs := captureLogs(t)
	content, dl, err := download(t, &Client{}, context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// len(content) di Python conta i caratteri, BOM compreso.
	if content != body || dl.chars != 13 || dl.head != body {
		t.Errorf("content %q chars %d head %q", content, dl.chars, dl.head)
	}
	if !logs.has("DEBUG", "Downloaded 13 bytes") {
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
	var de *downloadError
	if !errors.As(err, &de) || de.code != 0 || de.msg != "'utf-8' codec can't decode byte 0xff in position 3: invalid start byte" {
		t.Fatalf("err = %v", err)
	}
	if logs.has("DEBUG", "Downloaded") || logs.has("WARNING", "") {
		t.Errorf("log = %v", logs.all())
	}
}

// Il timeout di urlopen vale per singola operazione: un download lento ma
// continuo non viene interrotto anche se dura più del timeout.
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
	var de *downloadError
	if !errors.As(err, &de) || de.msg != "<urlopen error timed out>" {
		t.Fatalf("err = %v", err)
	}
	if !logs.has("WARNING", "⚠️  Errore rete durante download "+srv.URL+": timed out") {
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
	var de *downloadError
	if !errors.As(err, &de) || de.msg != "timed out" {
		t.Fatalf("err = %v", err)
	}
	// Gli errori durante read() non sono URLError: nessun warning.
	if logs.has("WARNING", "") {
		t.Errorf("log = %v", logs.all())
	}
	if got := readErrorMessage(errors.New("x"), true, "https://example.com/f.xml", 0, -1); got != "The read operation timed out" {
		t.Errorf("https timeout = %q", got)
	}
}

func TestDownloadTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "<root>....")
	}))
	defer srv.Close()
	_, _, err := download(t, &Client{}, context.Background(), srv.URL)
	if err == nil || err.Error() != "IncompleteRead(10 bytes read, 90 more expected)" {
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
	if err == nil || err.Error() != "<urlopen error [Errno 111] Connection refused>" {
		t.Fatalf("err = %v", err)
	}
	if !logs.has("WARNING", "⚠️  Errore rete durante download "+url+": [Errno 111] Connection refused") {
		t.Errorf("log = %v", logs.all())
	}
}

func TestDownloadInvalidURL(t *testing.T) {
	logs := captureLogs(t)
	_, _, err := download(t, &Client{}, context.Background(), "http://bad host/\x7f")
	var de *downloadError
	if !errors.As(err, &de) || de.code != 0 || logs.has("WARNING", "") {
		t.Errorf("err = %v, log = %v", err, logs.all())
	}
}

func TestURLErrorReasons(t *testing.T) {
	dns := &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}
	if got := urlErrorReason(dns, false); got != "[Errno -2] Name or service not known" {
		t.Errorf("dns = %q", got)
	}
	wrapped := fmt.Errorf("wrap: %w", errors.New("inner"))
	if got := urlErrorReason(wrapped, false); got != "inner" {
		t.Errorf("wrapped = %q", got)
	}
	if got := urlErrorReason(errors.New("plain"), false); got != "plain" {
		t.Errorf("plain = %q", got)
	}
	reset := &net.OpError{Op: "read", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	if got := readErrorMessage(reset, false, "http://x", 1, 10); got != "[Errno 104] Connection reset by peer" {
		t.Errorf("reset = %q", got)
	}
	if got := readErrorMessage(errors.New("boom"), false, "http://x", 1, -1); got != "boom" {
		t.Errorf("generic = %q", got)
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

// ========== Annullamento (asyncio.wait_for) ==========

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
	store := &cancelingStore{fakeStore: fakeStore{t: t, returns: []any{3}}, cancel: cancel}
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
	store := &fakeStore{t: t, returns: []any{1}}
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

func TestPySleepDuration(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want time.Duration
		err  string
	}{
		{0.5, 500 * time.Millisecond, ""},
		{0, 0, ""},
		{1e-10, 1, ""}, // _PyTime_ROUND_TIMEOUT arrotonda per eccesso
		{-1, 0, "sleep length must be non-negative"},
		{math.NaN(), 0, "Invalid value NaN (not a number)"},
		{math.Inf(1), 0, "timestamp out of range for C PyTime_t"},
		{1e10, 0, "timestamp out of range for C PyTime_t"},
	} {
		got, err := pySleepDuration(c.in)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("sleep(%v): err = %v", c.in, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("sleep(%v) = %v, %v", c.in, got, err)
		}
	}
}

func TestDefaultBackfillOptions(t *testing.T) {
	o := DefaultBackfillOptions()
	if o.Days != 365 || o.DryRun || o.Delay != 0.5 || o.Client != nil || o.Sleep != nil {
		t.Errorf("opts = %+v", o)
	}
}

func TestSubDaysOverflow(t *testing.T) {
	if _, err := subDays(frozenNow, 1000000000); err == nil || err.Error() != "days=1000000000; must have magnitude <= 999999999" {
		t.Errorf("err = %v", err)
	}
	if _, err := subDays(frozenNow, -3000000); err == nil || err.Error() != "date value out of range" {
		t.Errorf("err = %v", err)
	}
	if got := formatDate(time.Date(999, 1, 2, 0, 0, 0, 0, time.UTC), ""); got != "9990102" {
		t.Errorf("formatDate = %q", got)
	}
}

func TestClientDefaults(t *testing.T) {
	c := &Client{}
	if c.baseURL() != AreraBaseURL || c.httpClient() != http.DefaultClient || c.timeout() != RequestTimeout {
		t.Error("default non applicati")
	}
	if d := time.Since(c.clock()); d < 0 || d > time.Minute {
		t.Errorf("clock = %v", d)
	}
	c = &Client{BaseURL: "http://x"}
	if got := c.buildAreraURL(frozenNow, "E"); got != "http://x/2026_1/PO_Offerte_E_MLIBERO_20260103.xml" {
		t.Errorf("url = %s", got)
	}
}

// datetime.now() è l'ora locale "naive": Client.now usa i campi locali.
func TestClientNowUsesLocalWallClock(t *testing.T) {
	loc := time.FixedZone("CET", 3600)
	c := &Client{Now: func() time.Time { return time.Date(2026, 1, 1, 0, 30, 0, 0, loc) }}
	if got := formatDate(c.now(), "-"); got != "2026-01-01" {
		t.Errorf("now = %s", got)
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
	if err := client.RunScrape(context.Background(), &fakeStore{t: t, returns: []any{5}}); err != nil {
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

func TestRunScrapePropagatesPyException(t *testing.T) {
	bad := testDoc(strings.Replace(testOffer("01", "01", "01", "", comm("72")), "<NOME_OFFERTA>Test</NOME_OFFERTA>", "", 1))
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(bad)}, frozenNow)
	err := client.RunScrape(context.Background(), &fakeStore{t: t})
	var exc *PyException
	if !errors.As(err, &exc) || exc.Type != "AttributeError" || err.Error() != "'NoneType' object has no attribute 'text'" {
		t.Errorf("err = %v", err)
	}
}

func TestBasicHandler(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewBasicHandler(&buf, slog.LevelInfo, ScrapeLogTimeLayout)).With("logger", "data_reader").WithGroup("g")
	l.Debug("nascosto")
	l.Info("📊 Tariffe estratte:", "k", "v")
	l.Warn("{\n  \"a\": 1\n}")
	if got := buf.String(); got != "📊 Tariffe estratte:\n{\n  \"a\": 1\n}\n" {
		t.Errorf("output = %q", got)
	}

	buf.Reset()
	h := NewBasicHandler(&buf, slog.LevelDebug, BackfillLogTimeLayout)
	r := slog.NewRecord(time.Date(2026, 1, 3, 14, 5, 9, 0, time.Local), slog.LevelDebug, "   2026-01-03: 2 tariffe salvate", 0)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "14:05:09    2026-01-03: 2 tariffe salvate\n" {
		t.Errorf("output = %q", got)
	}
	buf.Reset()
	if err := h.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "x", 0)); err != nil || !strings.HasSuffix(buf.String(), " x\n") {
		t.Errorf("output = %q", buf.String())
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

func TestPyExceptionError(t *testing.T) {
	e := &PyException{Type: "ValueError", Msg: "boom"}
	if e.Error() != "boom" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestPyStrReprAndJSONString(t *testing.T) {
	in := "\b\f\x01\x7f\u00e8\U0001F419\"\\"
	if got := pyJSONString(in); got != "\"\\b\\f\\u0001\\u007f\\u00e8\\ud83d\\udc19\\\"\\\\\"" {
		t.Errorf("json = %s", got)
	}
	// repr: \x per i non stampabili < 0x100, \u / \U oltre, stampabili invariati.
	in = "\x00\x7f\u0085\u00a0\u2028\U0001F419\U000E0001"
	if got := pyStrRepr(in); got != "'\\x00\\x7f\\x85\\xa0\\u2028\U0001F419\\U000e0001'" {
		t.Errorf("repr = %s", got)
	}
	for in, want := range map[string]string{`a"b`: `'a"b'`, "a'b": `"a'b"`, `a'b"c`: `'a\'b"c'`} {
		if got := pyStrRepr(in); got != want {
			t.Errorf("repr(%s) = %s, atteso %s", in, got, want)
		}
	}
}
