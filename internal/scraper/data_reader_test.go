package scraper

// Porting di tests/test_data_reader.py: i mock di urlopen,
// _fetch_service_data e save_rates_batch diventano un trasporto HTTP finto,
// file XML equivalenti e uno store finto.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ========== Builder XML per i test ==========

type testComp struct {
	macro  string
	prezzi []string
}

// testOffer costruisce un'offerta Octopus; cod vuoto = senza COD_OFFERTA.
func testOffer(mercato, tipo, fasce, cod string, comps ...testComp) string {
	var b strings.Builder
	b.WriteString("<offerta><IdentificativiOfferta><PIVA_UTENTE>01771990445</PIVA_UTENTE>")
	if cod != "" {
		b.WriteString("<COD_OFFERTA>" + cod + "</COD_OFFERTA>")
	}
	b.WriteString("</IdentificativiOfferta><DettaglioOfferta><TIPO_MERCATO>" + mercato + "</TIPO_MERCATO><TIPO_OFFERTA>" + tipo + "</TIPO_OFFERTA><NOME_OFFERTA>Test</NOME_OFFERTA></DettaglioOfferta>")
	if fasce != "" {
		b.WriteString("<TipoPrezzo><TIPOLOGIA_FASCE>" + fasce + "</TIPOLOGIA_FASCE></TipoPrezzo>")
	}
	for _, c := range comps {
		b.WriteString("<ComponenteImpresa><MACROAREA>" + c.macro + "</MACROAREA>")
		for _, p := range c.prezzi {
			b.WriteString("<IntervalloPrezzi><PREZZO>" + p + "</PREZZO></IntervalloPrezzi>")
		}
		b.WriteString("</ComponenteImpresa>")
	}
	b.WriteString("</offerta>")
	return b.String()
}

func testDoc(offers ...string) string {
	return `<?xml version="1.0"?><ListaOfferteMercatoLibero>` + strings.Join(offers, "") + `</ListaOfferteMercatoLibero>`
}

func comm(p string) testComp    { return testComp{"01", []string{p}} }
func energia(p string) testComp { return testComp{"04", []string{p}} }

func todayURL(service string) string {
	return (&Client{}).buildAreraURL(frozenNow, service)
}

// ========== URL BUILDING TESTS ==========

func TestBuildAreraURLElectricity(t *testing.T) {
	url := (&Client{}).buildAreraURL(time.Date(2025, 11, 13, 0, 0, 0, 0, time.UTC), "E")
	want := "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_11/PO_Offerte_E_MLIBERO_20251113.xml"
	if url != want {
		t.Errorf("url = %s", url)
	}
}

func TestBuildAreraURLGas(t *testing.T) {
	url := (&Client{}).buildAreraURL(time.Date(2025, 11, 13, 0, 0, 0, 0, time.UTC), "G")
	want := "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_11/PO_Offerte_G_MLIBERO_20251113.xml"
	if url != want {
		t.Errorf("url = %s", url)
	}
}

func TestBuildAreraURLDifferentMonth(t *testing.T) {
	// Nota: il path usa il mese senza zero iniziale (es: 2025_1, non 2025_01)
	url := (&Client{}).buildAreraURL(time.Date(2025, 1, 5, 0, 0, 0, 0, time.UTC), "E")
	want := "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_1/PO_Offerte_E_MLIBERO_20250105.xml"
	if url != want {
		t.Errorf("url = %s", url)
	}
}

func TestDownloadXMLSetsHeadersAndDecodesResponse(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = io.WriteString(w, "<root/>")
	}))
	defer srv.Close()

	var content bytes.Buffer
	_, err := (&Client{}).downloadXML(context.Background(), srv.URL+"/file.xml", func(r io.Reader) {
		_, _ = io.Copy(&content, r)
	})
	if err != nil {
		t.Fatal(err)
	}
	if content.String() != "<root/>" {
		t.Errorf("content = %q", content.String())
	}
	if got.Get("User-Agent") != HTTPHeaders[0][1] {
		t.Errorf("User-Agent = %q", got.Get("User-Agent"))
	}
	if got.Get("Accept") != HTTPHeaders[1][1] {
		t.Errorf("Accept = %q", got.Get("Accept"))
	}
}

func TestParseAreraXMLSupportsUppercaseOffertaTag(t *testing.T) {
	xml := `<?xml version="1.0"?>
    <ListaOfferteMercatoLibero>
        <Offerta>
            <IdentificativiOfferta>
                <PIVA_UTENTE>01771990445</PIVA_UTENTE>
            </IdentificativiOfferta>
            <DettaglioOfferta>
                <TIPO_MERCATO>01</TIPO_MERCATO>
                <TIPO_OFFERTA>01</TIPO_OFFERTA>
            </DettaglioOfferta>
            <TipoPrezzo>
                <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
            </TipoPrezzo>
            <ComponenteImpresa>
                <MACROAREA>01</MACROAREA>
                <IntervalloPrezzi><PREZZO>72.0</PREZZO></IntervalloPrezzi>
            </ComponenteImpresa>
            <ComponenteImpresa>
                <MACROAREA>04</MACROAREA>
                <IntervalloPrezzi><PREZZO>0.10</PREZZO></IntervalloPrezzi>
            </ComponenteImpresa>
        </Offerta>
    </ListaOfferteMercatoLibero>
    `
	result, err := parseAreraXML(xml, "E")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := result.get("luce").Fissa.Get("monoraria"); !ok || r.Energia != 0.10 {
		t.Errorf("luce fissa monoraria = %+v %v", r, ok)
	}
}

func TestNormalizedCodeHandlesZeroPaddingAndSpaces(t *testing.T) {
	for _, c := range []struct {
		in   *string
		want string
		ok   bool
	}{
		{ptr("01"), "1", true},
		{ptr(" 02 "), "2", true},
		{ptr("ABC"), "ABC", true},
		{ptr(""), "", false},
		{nil, "", false},
	} {
		if got, ok := normalizedCode(c.in); got != c.want || ok != c.ok {
			t.Errorf("normalizedCode(%v) = %q, %v", c.in, got, ok)
		}
	}
}

// Se TIPOLOGIA_FASCE manca, non scartare l'offerta luce
func TestParseOffertaLuceWithoutTipologiaFasceUsesFallbackMono(t *testing.T) {
	offerta := mustElement(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>1</TIPO_MERCATO>
            <TIPO_OFFERTA>1</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi><PREZZO>72.0</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi><PREZZO>0.11</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>
    `)
	parsed, err := parseOffertaLuce(offerta, immediateSink)
	if err != nil || parsed == nil {
		t.Fatalf("parsed = %v, %v", parsed, err)
	}
	if parsed.tipoOfferta != "fissa" || parsed.tipoFascia != "monoraria" || parsed.dati.Energia != 0.11 {
		t.Errorf("parsed = %+v", parsed)
	}
}

// ========== XML NAMESPACE TESTS ==========

func TestRemoveNamespace(t *testing.T) {
	tree := mustElement(t, `<?xml version="1.0"?>
    <root xmlns="http://example.com">
        <child>test</child>
    </root>`)
	// Tag non dovrebbe contenere namespace
	if strings.Contains(tree.tag, "}") || tree.tag != "root" {
		t.Errorf("tag = %q", tree.tag)
	}
	if tree.find("child") == nil {
		t.Error("child non trovato")
	}
}

// ========== COMPONENT EXTRACTION TESTS ==========

func TestExtractComponenteImpresaFound(t *testing.T) {
	offerta := mustElement(t, `<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Test</NOME>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
                <UNITA_MISURA>01</UNITA_MISURA>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	result, err := extractComponenteImpresa(offerta, "01")
	if err != nil || !result.found {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(result.prezzi) != 1 || result.prezzi[0] != 72.0 {
		t.Errorf("prezzi = %v", result.prezzi)
	}
}

func TestExtractComponenteImpresaNotFound(t *testing.T) {
	offerta := mustElement(t, `<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Test</NOME>
            <MACROAREA>01</MACROAREA>
        </ComponenteImpresa>
    </offerta>`)
	result, err := extractComponenteImpresa(offerta, "04")
	if err != nil || result.found {
		t.Errorf("result = %+v, %v", result, err)
	}
}

func TestExtractComponenteImpresaMultipleFasce(t *testing.T) {
	offerta := mustElement(t, `<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Spread</NOME>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <FASCIA_COMPONENTE>01</FASCIA_COMPONENTE>
                <PREZZO>0.0088</PREZZO>
                <UNITA_MISURA>03</UNITA_MISURA>
            </IntervalloPrezzi>
            <IntervalloPrezzi>
                <FASCIA_COMPONENTE>02</FASCIA_COMPONENTE>
                <PREZZO>0.0088</PREZZO>
                <UNITA_MISURA>03</UNITA_MISURA>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	result, err := extractComponenteImpresa(offerta, "04")
	if err != nil || !result.found {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(result.prezzi) != 2 || result.prezzi[0] != 0.0088 || result.prezzi[1] != 0.0088 {
		t.Errorf("prezzi = %v", result.prezzi)
	}
}

// MACROAREA viene confrontata senza zero iniziali (es: '6' == '06')
func TestExtractComponenteImpresaNormalizesMacroareaZeroPadding(t *testing.T) {
	offerta := mustElement(t, `<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Energia</NOME>
            <MACROAREA>6</MACROAREA>
            <IntervalloPrezzi><PREZZO>0.0099</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	result, err := extractComponenteImpresa(offerta, "06")
	if p, ok := result.firstPrezzo(); err != nil || !ok || p != 0.0099 {
		t.Errorf("result = %+v, %v", result, err)
	}
}

// ========== LUCE PARSING TESTS ==========

func parseLuce(t *testing.T, xml string) *parsedOffer {
	t.Helper()
	parsed, err := parseOffertaLuce(mustElement(t, xml), immediateSink)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func parseGas(t *testing.T, xml string) *parsedOffer {
	t.Helper()
	parsed, err := parseOffertaGas(mustElement(t, xml), immediateSink)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestParseOffertaLuceFissaMonoraria(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
            <NOME_OFFERTA>Test Fissa</NOME_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.1078</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil {
		t.Fatal("result nil")
	}
	if result.tipoOfferta != "fissa" || result.tipoFascia != "monoraria" || result.dati.Energia != 0.1078 ||
		result.dati.Commercializzazione == nil || *result.dati.Commercializzazione != 72.0 {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaLuceVariabileTrioraria(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>03</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.0088</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.tipoOfferta != "variabile" || result.tipoFascia != "trioraria" || result.dati.Energia != 0.0088 {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaLuceWrongPiva(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>12345678901</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>`)
	if result != nil {
		t.Errorf("result = %+v", result)
	}
}

// Offerta gas (dovrebbe restituire None)
func TestParseOffertaLuceGasMarket(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>`)
	if result != nil {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaLuceWithCodOfferta(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
            <COD_OFFERTA>000129ESVML77XXXXXOCTOFLEXMONv77</COD_OFFERTA>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.0088</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.dati.CodOfferta == nil || *result.dati.CodOfferta != "000129ESVML77XXXXXOCTOFLEXMONv77" {
		t.Errorf("result = %+v", result)
	}
}

// Luce senza codice offerta (backward compatibility)
func TestParseOffertaLuceWithoutCodOfferta(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.1045</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.dati.CodOfferta != nil {
		t.Errorf("result = %+v", result)
	}
}

// Nel nuovo tracciato luce il prezzo energia può stare in MACROAREA=06
func TestParseOffertaLuceUsesMacroarea06When04Missing(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>03</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi><PREZZO>72.0</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>06</MACROAREA>
            <IntervalloPrezzi><PREZZO>0.0088</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>
    `)
	if result == nil || result.tipoOfferta != "variabile" || result.tipoFascia != "trioraria" || result.dati.Energia != 0.0088 {
		t.Errorf("result = %+v", result)
	}
}

// TIPOLOGIA_FASCE=02 viene classificata come bioraria (non monoraria)
func TestParseOffertaLuceBiorariaFascia(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>02</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi><PREZZO>72.0</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi><PREZZO>0.0050</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>
    `)
	if result == nil || result.tipoOfferta != "variabile" || result.tipoFascia != "bioraria" || result.dati.Energia != 0.0050 {
		t.Errorf("result = %+v", result)
	}
}

// Luce fissa con MACROAREA '6' (senza zero) viene letta correttamente
func TestParseOffertaLuceFissaWithMacroarea06NoPadding(t *testing.T) {
	result := parseLuce(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi><PREZZO>72.0</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>6</MACROAREA>
            <IntervalloPrezzi><PREZZO>0.1150</PREZZO></IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>
    `)
	if result == nil || result.tipoOfferta != "fissa" || result.tipoFascia != "monoraria" || result.dati.Energia != 0.1150 {
		t.Errorf("result = %+v", result)
	}
}

// ========== GAS PARSING TESTS ==========

func TestParseOffertaGasFissa(t *testing.T) {
	result := parseGas(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.39</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.tipoOfferta != "fissa" || result.dati.Energia != 0.39 || *result.dati.Commercializzazione != 84.0 {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaGasVariabile(t *testing.T) {
	result := parseGas(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.08</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.tipoOfferta != "variabile" || result.dati.Energia != 0.08 {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaGasWrongPiva(t *testing.T) {
	result := parseGas(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>12345678901</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>`)
	if result != nil {
		t.Errorf("result = %+v", result)
	}
}

func TestParseOffertaGasWithCodOfferta(t *testing.T) {
	result := parseGas(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
            <COD_OFFERTA>000129GSFML37XXXXXXXXOCTOFIXGv37</COD_OFFERTA>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.36</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.dati.CodOfferta == nil || *result.dati.CodOfferta != "000129GSFML37XXXXXXXXOCTOFIXGv37" {
		t.Errorf("result = %+v", result)
	}
}

// Gas senza codice offerta (backward compatibility)
func TestParseOffertaGasWithoutCodOfferta(t *testing.T) {
	result := parseGas(t, `<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.08</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>`)
	if result == nil || result.dati.CodOfferta != nil {
		t.Errorf("result = %+v", result)
	}
}

// ========== XML PARSING TESTS ==========

func TestParseAreraXMLElectricity(t *testing.T) {
	result, err := parseAreraXML(readTestdata(t, "sample_arera.xml"), "E")
	if err != nil || result.servizio != "luce" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(result.rates.Fissa) == 0 || len(result.rates.Variabile) == 0 {
		t.Errorf("luce = %+v", result.rates)
	}
}

func TestParseAreraXMLGas(t *testing.T) {
	// Usa le offerte gas di sample_arera.xml
	result, err := parseAreraXML(readTestdata(t, "sample_arera.xml"), "G")
	if err != nil || result.servizio != "gas" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(result.rates.Fissa) == 0 || len(result.rates.Variabile) == 0 {
		t.Errorf("gas = %+v", result.rates)
	}
}

// ========== FLATTEN RATES TESTS ==========

func TestFlattenRates(t *testing.T) {
	var luce, gas ServiceRates
	luce.Fissa.set("monoraria", Rate{Energia: 0.1078, Commercializzazione: ptr(72.0)})
	luce.Variabile.set("monoraria", Rate{Energia: 0.0088, Commercializzazione: ptr(72.0), CodOfferta: ptr("COD123")})
	gas.Fissa.set("monoraria", Rate{Energia: 0.39, Commercializzazione: ptr(84.0)})

	result := flattenRates(luce, gas)

	if len(result) != 3 {
		t.Fatalf("len = %d", len(result))
	}
	var luceFissa, luceVar []int
	for i, r := range result {
		if r.Servizio == "luce" && r.Tipo == "fissa" {
			luceFissa = append(luceFissa, i)
		}
		if r.Servizio == "luce" && r.Tipo == "variabile" {
			luceVar = append(luceVar, i)
		}
	}
	if len(luceFissa) != 1 {
		t.Fatalf("luce fissa = %v", luceFissa)
	}
	lf := result[luceFissa[0]]
	if lf.Fascia != "monoraria" || lf.Energia != 0.1078 || *lf.Commercializzazione != 72.0 {
		t.Errorf("luce fissa = %+v", lf)
	}
	if len(luceVar) != 1 || *result[luceVar[0]].CodOfferta != "COD123" {
		t.Errorf("luce variabile = %v", luceVar)
	}
}

func TestFlattenRatesEmpty(t *testing.T) {
	if result := flattenRates(ServiceRates{}, ServiceRates{}); len(result) != 0 {
		t.Errorf("result = %+v", result)
	}
}

// ========== INTEGRATION TESTS ==========

// Il sample contiene le 5 tariffe dei mock Python: luce fissa monoraria
// (0.1078/72), variabile monoraria e trioraria (0.0088/72), gas fissa
// (0.39/84) e variabile (0.08/84).
func TestFetchOctopusTariffeSuccess(t *testing.T) {
	sample := readTestdata(t, "sample_arera.xml")
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(sample), todayURL("G"): bodyRoute(sample)}, frozenNow)
	store := &fakeStore{t: t, returns: []int{5}}

	result, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack)
	if err != nil {
		t.Fatal(err)
	}
	// Verify structure
	if result.DataAggiornamento == "" {
		t.Error("data_aggiornamento mancante")
	}
	// Verify data
	if r, _ := result.Luce.Fissa.Get("monoraria"); r.Energia != 0.1078 {
		t.Errorf("luce fissa = %+v", r)
	}
	if r, _ := result.Gas.Fissa.Get("monoraria"); r.Energia != 0.39 {
		t.Errorf("gas fissa = %+v", r)
	}
}

// Fallimento parziale (solo gas disponibile)
func TestFetchOctopusTariffePartialFailure(t *testing.T) {
	gasXML := testDoc(testOffer("02", "01", "", "", comm("84.0"), energia("0.39")))
	client, _ := fakeClient(map[string]route{todayURL("G"): bodyRoute(gasXML)}, frozenNow)
	store := &fakeStore{t: t, returns: []int{2}}

	result, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack)
	if err != nil {
		t.Fatal(err)
	}
	// Luce should be empty
	if len(result.Luce.Fissa) != 0 || len(result.Luce.Variabile) != 0 {
		t.Errorf("luce = %+v", result.Luce)
	}
	// Gas should have data
	if r, _ := result.Gas.Fissa.Get("monoraria"); r.Energia != 0.39 {
		t.Errorf("gas fissa = %+v", r)
	}
}

// Il DB NON viene scritto quando nessuna tariffa è trovata
func TestFetchOctopusTariffeNoRatesFoundDoesNotSave(t *testing.T) {
	client, _ := fakeClient(nil, frozenNow)
	store := &fakeStore{t: t} // Fatal se SaveRatesBatch viene chiamato

	result, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Luce, ServiceRates{}) || !reflect.DeepEqual(result.Gas, ServiceRates{}) {
		t.Errorf("result = %+v", result)
	}
	if len(store.calls) != 0 {
		t.Errorf("save chiamato %d volte", len(store.calls))
	}
}

// Errore DB viene loggato correttamente
func TestFetchOctopusTariffeDBErrorLogsError(t *testing.T) {
	luceXML := testDoc(testOffer("01", "01", "01", "", comm("72.0"), energia("0.1078")))
	client, _ := fakeClient(map[string]route{todayURL("E"): bodyRoute(luceXML)}, frozenNow)
	store := &fakeStore{t: t, returns: []int{-1}}
	logs := captureLogs(t)

	result, err := client.FetchOctopusTariffe(context.Background(), store, DefaultMaxDaysBack)
	if err != nil {
		t.Fatal(err)
	}
	// Dovrebbe loggare errore critico
	n := 0
	for _, m := range logs.messages("ERROR") {
		if strings.Contains(m, "ERRORE SALVATAGGIO DB") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("log ERRORE SALVATAGGIO DB = %d", n)
	}
	// Ma dovrebbe comunque ritornare i dati estratti
	if r, _ := result.Luce.Fissa.Get("monoraria"); r.Energia != 0.1078 {
		t.Errorf("luce fissa = %+v", r)
	}
}

func TestFetchServiceDataDownloadFailure(t *testing.T) {
	routes := map[string]route{}
	refused := "refused"
	for d := 0; d <= 2; d++ {
		routes[urlFor(frozenNow.AddDate(0, 0, -d), "E")] = route{URLError: &refused}
	}
	client, tr := fakeClient(routes, frozenNow)

	result, date, err := client.fetchServiceData(context.Background(), "E", 2)
	if err != nil {
		t.Fatal(err)
	}
	// Should return empty dict and None when all attempts fail
	if result.servizio != "" || date != nil {
		t.Errorf("result = %+v, date = %v", result, date)
	}
	if n := len(tr.urls()); n != 3 {
		t.Errorf("tentativi = %d, attesi 3", n)
	}
}

// ========== DATA STRUCTURE TESTS ==========

func completeTariffe() Tariffe {
	tr := Tariffe{DataAggiornamento: "2025-11-13"}
	tr.Luce.Fissa.set("monoraria", Rate{Energia: 0.1078, Commercializzazione: ptr(72.0)})
	tr.Luce.Variabile.set("monoraria", Rate{Energia: 0.0088, Commercializzazione: ptr(72.0)})
	tr.Luce.Variabile.set("trioraria", Rate{Energia: 0.0088, Commercializzazione: ptr(72.0)})
	tr.Gas.Fissa.set("monoraria", Rate{Energia: 0.39, Commercializzazione: ptr(84.0)})
	tr.Gas.Variabile.set("monoraria", Rate{Energia: 0.08, Commercializzazione: ptr(84.0)})
	return tr
}

// Struttura completa con tutte le tariffe
func TestCompleteDataStructure(t *testing.T) {
	var v map[string]json.RawMessage
	if err := json.Unmarshal([]byte(completeTariffe().JSONIndent()), &v); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"luce", "gas", "data_aggiornamento"} {
		if _, ok := v[k]; !ok {
			t.Errorf("chiave %q mancante", k)
		}
	}
	for _, k := range []string{"luce", "gas"} {
		var m map[string]any
		if err := json.Unmarshal(v[k], &m); err != nil {
			t.Fatal(err)
		}
		for _, tipo := range []string{"fissa", "variabile"} {
			if _, ok := m[tipo]; !ok {
				t.Errorf("%s.%s mancante", k, tipo)
			}
		}
	}
}

// Tipi di dati corretti (numeri per energia/commercializzazione, stringa per la data)
func TestDataTypes(t *testing.T) {
	tr := Tariffe{DataAggiornamento: "2025-11-13"}
	tr.Luce.Fissa.set("monoraria", Rate{Energia: 0.1078, Commercializzazione: ptr(72.0)})
	var v struct {
		Luce struct {
			Fissa map[string]map[string]any `json:"fissa"`
		} `json:"luce"`
		DataAggiornamento any `json:"data_aggiornamento"`
	}
	if err := json.Unmarshal([]byte(tr.JSONIndent()), &v); err != nil {
		t.Fatal(err)
	}
	mono := v.Luce.Fissa["monoraria"]
	if _, ok := mono["energia"].(float64); !ok {
		t.Errorf("energia = %T", mono["energia"])
	}
	if _, ok := mono["commercializzazione"].(float64); !ok {
		t.Errorf("commercializzazione = %T", mono["commercializzazione"])
	}
	if _, ok := v.DataAggiornamento.(string); !ok {
		t.Errorf("data_aggiornamento = %T", v.DataAggiornamento)
	}
}

// L'output è serializzabile in JSON e si rilegge identico
func TestJSONSerializable(t *testing.T) {
	tr := Tariffe{DataAggiornamento: "2025-11-13"}
	tr.Luce.Fissa.set("monoraria", Rate{Energia: 0.1078, Commercializzazione: ptr(72.0)})
	tr.Luce.Variabile.set("monoraria", Rate{Energia: 0.0088, Commercializzazione: ptr(72.0)})
	tr.Gas.Fissa.set("monoraria", Rate{Energia: 0.39, Commercializzazione: ptr(84.0)})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(tr.JSONIndent()), &parsed); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"luce": map[string]any{
			"fissa":     map[string]any{"monoraria": map[string]any{"energia": 0.1078, "commercializzazione": 72.0, "cod_offerta": nil}},
			"variabile": map[string]any{"monoraria": map[string]any{"energia": 0.0088, "commercializzazione": 72.0, "cod_offerta": nil}},
		},
		"gas": map[string]any{
			"fissa":     map[string]any{"monoraria": map[string]any{"energia": 0.39, "commercializzazione": 84.0, "cod_offerta": nil}},
			"variabile": map[string]any{},
		},
		"data_aggiornamento": "2025-11-13",
	}
	if !reflect.DeepEqual(parsed, want) {
		t.Errorf("parsed = %v", parsed)
	}
}

// ===== Test Error Handling =====

func assertEmptyResult(t *testing.T, xml, service, want string) {
	t.Helper()
	result, err := parseAreraXML(xml, service)
	if err != nil {
		t.Fatal(err)
	}
	if got := areraJSON(t, result); got != want {
		t.Errorf("result = %s, atteso %s", got, want)
	}
}

func TestParseAreraXMLMalformed(t *testing.T) {
	malformed := `<?xml version="1.0"?>
    <OfferList>
        <Offer>
            <SupplierName>Octopus Energy Italia</SupplierName>
            <OfferName>TARIFF<missing_close_tag>
        </Offer>
    ` // Missing closing tags
	assertEmptyResult(t, malformed, "E", `{"luce":{"fissa":{},"variabile":{}}}`)
}

// Contenuto non-XML (es. pagina di errore HTML)
func TestParseAreraXMLNonXMLContent(t *testing.T) {
	htmlError := `<!DOCTYPE html>
    <html>
    <head><title>500 Internal Server Error</title></head>
    <body><h1>Server Error</h1></body>
    </html>`
	assertEmptyResult(t, htmlError, "G", `{"gas":{"fissa":{},"variabile":{}}}`)
}

func TestParseAreraXMLEmptyString(t *testing.T) {
	assertEmptyResult(t, "", "E", `{"luce":{"fissa":{},"variabile":{}}}`)
}

func TestEmptyStructure(t *testing.T) {
	for service, want := range map[string]string{
		"E":       `{"luce":{"fissa":{},"variabile":{}}}`,
		"G":       `{"gas":{"fissa":{},"variabile":{}}}`,
		"INVALID": `{}`,
	} {
		if got := areraJSON(t, areraData{servizio: servizioOf(service)}); got != want {
			t.Errorf("%s: got %s", service, got)
		}
	}
}

// ========== SECURITY TESTS ==========

// Gli attacchi XXE (XML External Entity) vengono bloccati
func TestParseAreraXMLXXEAttackBlocked(t *testing.T) {
	xxe := `<?xml version="1.0"?>
    <!DOCTYPE foo [
      <!ENTITY xxe SYSTEM "file:///etc/passwd">
    ]>
    <root>
        <offerta>&xxe;</offerta>
    </root>`
	logs := captureLogs(t)
	assertEmptyResult(t, xxe, "E", `{"luce":{"fissa":{},"variabile":{}}}`)
	if !logs.has("ERROR", "❌ Errore inaspettato parsing XML elettricità: ") {
		t.Errorf("log = %v", logs.all())
	}
}

// Gli attacchi billion laughs (entity expansion bomb) vengono bloccati
func TestParseAreraXMLEntityExpansionBlocked(t *testing.T) {
	bomb := `<?xml version="1.0"?>
    <!DOCTYPE lolz [
      <!ENTITY lol "lol">
      <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
      <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
      <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
    ]>
    <root>&lol4;</root>`
	assertEmptyResult(t, bomb, "E", `{"luce":{"fissa":{},"variabile":{}}}`)
}
