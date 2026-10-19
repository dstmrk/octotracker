package scraper

// Estrazione delle tariffe Octopus dalle offerte di un file XML ARERA.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
)

// errNoNomeOfferta: un'offerta Octopus senza prezzo energia e senza
// NOME_OFFERTA interrompe la lettura (come l'AttributeError della versione
// Python).
var errNoNomeOfferta = errors.New("offerta senza prezzo energia e senza NOME_OFFERTA")

// logSink riceve i log prodotti dal parsing di un'offerta.
type logSink func(level slog.Level, msg string)

type logEntry struct {
	level slog.Level
	msg   string
}

// normalizedCode normalizza un codice ARERA: spazi attorno rimossi e, se è
// numerico, zeri iniziali rimossi ("01" → "1"). false se il valore manca o
// è vuoto.
func normalizedCode(value *string) (string, bool) {
	if value == nil {
		return "", false
	}
	cleaned := strings.TrimSpace(*value)
	if cleaned == "" {
		return "", false
	}
	if strings.Trim(cleaned, "0123456789") == "" {
		if cleaned = strings.TrimLeft(cleaned, "0"); cleaned == "" {
			cleaned = "0"
		}
	}
	return cleaned, true
}

// normalizedCodeOf normalizza il testo di un elemento (che può mancare).
func normalizedCodeOf(e *element) (string, bool) {
	return normalizedCode(textOf(e))
}

// parsePrezzo legge il numero di un elemento PREZZO.
func parsePrezzo(text *string) (float64, error) {
	if text == nil {
		return 0, errors.New("PREZZO vuoto")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*text), 64)
	if err != nil {
		return 0, fmt.Errorf("PREZZO non valido: %w", err)
	}
	return v, nil
}

// extractPrezzi restituisce i prezzi degli IntervalloPrezzi del componente
// (quelli senza PREZZO vengono saltati). Un prezzo non valido è un errore
// anche se non è il primo.
func extractPrezzi(comp *element) ([]float64, error) {
	var out []float64
	for _, iv := range comp.findAllDesc("IntervalloPrezzi") {
		prezzo := iv.find("PREZZO")
		if prezzo == nil {
			continue
		}
		v, err := parsePrezzo(prezzo.text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// componente è un ComponenteImpresa trovato: found è false se nessun
// componente ha la MACROAREA cercata.
type componente struct {
	found  bool
	prezzi []float64
}

// firstPrezzo è il primo prezzo del componente, se presente.
func (c componente) firstPrezzo() (float64, bool) {
	if len(c.prezzi) == 0 {
		return 0, false
	}
	return c.prezzi[0], true
}

// extractComponenteImpresa restituisce il primo ComponenteImpresa con la
// MACROAREA indicata (confronto senza zeri iniziali: "6" == "06").
func extractComponenteImpresa(offerta *element, macroarea string) (componente, error) {
	target, _ := normalizedCode(&macroarea)
	for _, comp := range offerta.findAllDesc("ComponenteImpresa") {
		macro := comp.find("MACROAREA")
		if macro == nil {
			continue
		}
		if code, ok := normalizedCode(macro.text); ok && code == target {
			prezzi, err := extractPrezzi(comp)
			return componente{found: true, prezzi: prezzi}, err
		}
	}
	return componente{}, nil
}

// prezzoComponente è il primo prezzo del componente con la MACROAREA indicata.
func prezzoComponente(offerta *element, macroarea string) (float64, bool, error) {
	comp, err := extractComponenteImpresa(offerta, macroarea)
	if err != nil {
		return 0, false, err
	}
	v, ok := comp.firstPrezzo()
	return v, ok, nil
}

// parsedOffer è un'offerta Octopus valida: tipo ("fissa"/"variabile"),
// fascia (vuota per il gas) e tariffa.
type parsedOffer struct {
	tipoOfferta string
	tipoFascia  string
	dati        Rate
}

func isOctopus(offerta *element) bool {
	piva := offerta.findDesc("PIVA_UTENTE")
	return piva != nil && piva.text != nil && *piva.text == OctopusPIVA
}

// tipoMercatoIs verifica TIPO_MERCATO (1 = luce, 2 = gas).
func tipoMercatoIs(offerta *element, code string) bool {
	got, ok := normalizedCodeOf(offerta.findDesc("TIPO_MERCATO"))
	return ok && got == code
}

// tipoOffertaOf: 01 = fissa, altro = variabile; false se TIPO_OFFERTA manca.
func tipoOffertaOf(offerta *element) (string, bool) {
	code, ok := normalizedCodeOf(offerta.findDesc("TIPO_OFFERTA"))
	switch {
	case !ok:
		return "", false
	case code == "1":
		return "fissa", true
	}
	return "variabile", true
}

// tipoFasciaOf: 02 = bioraria, 03 = trioraria; se il campo manca o non è
// standard la fascia è monoraria, per non perdere l'offerta.
func tipoFasciaOf(offerta *element) string {
	code, _ := normalizedCodeOf(offerta.findDesc("TIPOLOGIA_FASCE"))
	switch code {
	case "3":
		return "trioraria"
	case "2":
		return "bioraria"
	}
	return "monoraria"
}

// commercializzazioneOf è il costo di commercializzazione (MACROAREA 01,
// €/anno), nil se assente.
func commercializzazioneOf(offerta *element) (*float64, error) {
	v, ok, err := prezzoComponente(offerta, "01")
	if err != nil || !ok {
		return nil, err
	}
	return &v, nil
}

// energiaNotFound logga l'offerta senza prezzo energia.
func energiaNotFound(offerta *element, prefix string, log logSink) error {
	nome := offerta.findDesc("NOME_OFFERTA")
	if nome == nil {
		return errNoNomeOfferta
	}
	text := "None"
	if nome.text != nil {
		text = *nome.text
	}
	log(slog.LevelWarn, prefix+text)
	return nil
}

// parseOffertaLuce restituisce nil se l'offerta non è una luce Octopus valida.
func parseOffertaLuce(offerta *element, log logSink) (*parsedOffer, error) {
	if !isOctopus(offerta) || !tipoMercatoIs(offerta, "1") {
		return nil, nil
	}
	tipoOfferta, ok := tipoOffertaOf(offerta)
	if !ok {
		return nil, nil
	}
	tipoFascia := tipoFasciaOf(offerta)
	codOfferta := textOf(offerta.findDesc("COD_OFFERTA"))

	commercializzazione, err := commercializzazioneOf(offerta)
	if err != nil {
		return nil, err
	}

	// Prezzo energia: MACROAREA 04 (tracciato storico) o, se il componente
	// manca, 06 (nuovo tracciato).
	comp, err := extractComponenteImpresa(offerta, "04")
	if err == nil && !comp.found {
		comp, err = extractComponenteImpresa(offerta, "06")
	}
	if err != nil {
		return nil, err
	}
	energia, ok := comp.firstPrezzo()
	if !ok {
		return nil, energiaNotFound(offerta, "Energia non trovata per offerta ", log)
	}

	if tipoOfferta == "fissa" {
		log(slog.LevelInfo, fmt.Sprintf("✅ Luce fissa %s: %v €/kWh, comm: %s €/anno",
			tipoFascia, energia, optFloat(commercializzazione)))
	} else {
		log(slog.LevelInfo, fmt.Sprintf("✅ Luce variabile %s: PUN + %v €/kWh, comm: %s €/anno",
			tipoFascia, energia, optFloat(commercializzazione)))
	}
	return &parsedOffer{
		tipoOfferta: tipoOfferta,
		tipoFascia:  tipoFascia,
		dati:        Rate{Energia: energia, Commercializzazione: commercializzazione, CodOfferta: codOfferta},
	}, nil
}

// parseOffertaGas restituisce nil se l'offerta non è un gas Octopus valido.
func parseOffertaGas(offerta *element, log logSink) (*parsedOffer, error) {
	if !isOctopus(offerta) || !tipoMercatoIs(offerta, "2") {
		return nil, nil
	}
	tipoOfferta, ok := tipoOffertaOf(offerta)
	if !ok {
		return nil, nil
	}
	codOfferta := textOf(offerta.findDesc("COD_OFFERTA"))

	commercializzazione, err := commercializzazioneOf(offerta)
	if err != nil {
		return nil, err
	}
	energia, ok, err := prezzoComponente(offerta, "04")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, energiaNotFound(offerta, "Energia non trovata per offerta gas ", log)
	}

	if tipoOfferta == "fissa" {
		log(slog.LevelInfo, fmt.Sprintf("✅ Gas fisso monorario: %v €/Smc, comm: %s €/anno",
			energia, optFloat(commercializzazione)))
	} else {
		log(slog.LevelInfo, fmt.Sprintf("✅ Gas variabile monorario: PSV + %v €/Smc, comm: %s €/anno",
			energia, optFloat(commercializzazione)))
	}
	return &parsedOffer{
		tipoOfferta: tipoOfferta,
		tipoFascia:  "monoraria",
		dati:        Rate{Energia: energia, Commercializzazione: commercializzazione, CodOfferta: codOfferta},
	}, nil
}

// optFloat formatta un numero opzionale per i log.
func optFloat(f *float64) string {
	if f == nil {
		return "None"
	}
	return fmt.Sprint(*f)
}

// areraData sono le tariffe lette da un file: servizio "luce", "gas" o ""
// (servizio sconosciuto).
type areraData struct {
	servizio string
	rates    ServiceRates
}

// get restituisce le tariffe se il file è del servizio indicato.
func (d areraData) get(servizio string) ServiceRates {
	if d.servizio == servizio {
		return d.rates
	}
	return ServiceRates{}
}

// servizioOf converte il codice ARERA ("E" luce, "G" gas) nel servizio.
func servizioOf(service string) string {
	switch service {
	case "E":
		return "luce"
	case "G":
		return "gas"
	}
	return ""
}

func serviceName(service string) string {
	if service == "E" {
		return ServiceNameElectricity
	}
	return ServiceNameGas
}

// offerOutcome sono gli effetti del parsing di una singola offerta (log,
// risultato, errore), applicati solo se il documento è ben formato.
type offerOutcome struct {
	logs   []logEntry
	parsed *parsedOffer
	err    error
}

// offerList raccoglie, in ordine di documento, le offerte con un tag
// (<offerta> o <Offerta>): count le conta tutte, outcomes conserva quelle
// con effetti fino al primo errore (dopo il quale la lettura si ferma).
type offerList struct {
	count    int
	outcomes []offerOutcome
	failed   bool
}

func (l *offerList) add(offerta *element, service string) {
	l.count++
	if l.failed {
		return
	}
	var out offerOutcome
	sink := func(level slog.Level, msg string) { out.logs = append(out.logs, logEntry{level, msg}) }
	switch service {
	case "E":
		out.parsed, out.err = parseOffertaLuce(offerta, sink)
	case "G":
		out.parsed, out.err = parseOffertaGas(offerta, sink)
	default:
		return
	}
	l.failed = out.err != nil
	if out.parsed != nil || out.err != nil || len(out.logs) > 0 {
		l.outcomes = append(l.outcomes, out)
	}
}

// areraParse è lo stato della lettura di un file dopo la scansione.
type areraParse struct {
	service      string
	err          error // errore di sintassi o di struttura del documento
	lower, upper offerList
}

// scanAreraXML scansiona il documento ed elabora le offerte una alla volta,
// senza loggare: gli effetti vengono applicati da result.
func scanAreraXML(r io.Reader, service string) *areraParse {
	p := &areraParse{service: service}
	capture := func(tag string, depth int) bool {
		return depth >= 2 && (tag == "offerta" || tag == "Offerta")
	}
	onTree := func(e *element) {
		// L'offerta catturata e quelle eventualmente annidate, in ordine di
		// documento.
		visit := func(n *element) bool {
			switch n.tag {
			case "offerta":
				p.lower.add(n, service)
			case "Offerta":
				p.upper.add(n, service)
			}
			return true
		}
		visit(e)
		e.walkDescendants(visit)
	}
	p.err = scanXML(r, capture, onTree)
	return p
}

// result emette i log e restituisce le tariffe del file. Un documento non
// valido dà una struttura vuota (head sono i primi caratteri del file, per
// il log di debug); l'errore di un'offerta interrompe la lettura.
func (p *areraParse) result(head string) (areraData, error) {
	empty := areraData{servizio: servizioOf(p.service)}
	if p.err != nil {
		name := serviceName(p.service)
		if errors.Is(p.err, errEntitiesForbidden) {
			logger.Error(fmt.Sprintf("❌ Errore inaspettato parsing XML %s: %v", name, p.err))
			return empty, nil
		}
		logger.Error(fmt.Sprintf("❌ XML malformato per %s: %v", name, p.err))
		logger.Debug("   Primi 500 caratteri: " + head)
		return empty, nil
	}

	// Offerte con il tag storico <offerta> o, se non ce ne sono, <Offerta>.
	offerte := &p.lower
	if offerte.count == 0 {
		offerte = &p.upper
	}
	logger.Debug(fmt.Sprintf("Trovate %d offerte nel file XML", offerte.count))

	if empty.servizio == "" {
		return empty, nil
	}
	var tariffe ServiceRates
	for _, o := range offerte.outcomes {
		for _, l := range o.logs {
			logger.Log(context.Background(), l.level, l.msg)
		}
		if o.err != nil {
			return areraData{}, o.err
		}
		if o.parsed != nil {
			tariffe.tipo(o.parsed.tipoOfferta).set(o.parsed.tipoFascia, o.parsed.dati)
		}
	}
	return areraData{servizio: empty.servizio, rates: tariffe}, nil
}

// parseAreraXML legge un file XML già in memoria.
func parseAreraXML(xmlContent, service string) (areraData, error) {
	head := xmlContent[:min(len(xmlContent), headBytes)]
	return scanAreraXML(strings.NewReader(xmlContent), service).result(strings.ToValidUTF8(head, ""))
}
