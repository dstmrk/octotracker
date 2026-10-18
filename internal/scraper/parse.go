package scraper

// Porting delle funzioni di parsing di data_reader.py: _normalized_code,
// _parse_intervallo_prezzo, _extract_intervalli_prezzi,
// _build_componente_result, _extract_componente_impresa,
// _validate_and_extract_luce_metadata, _parse_offerta_luce,
// _parse_offerta_gas, _empty_structure, _process_*_offers e _parse_arera_xml.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// logSink riceve i log prodotti dal parsing di un'offerta: subito (logger) o
// in differita, quando il risultato va emesso solo a documento completato.
type logSink func(level slog.Level, msg string)

type logEntry struct {
	level slog.Level
	msg   string
}

// _normalized_code: normalizza codici ARERA rimuovendo spazi e zeri iniziali.
// nil corrisponde a None. Può sollevare ValueError come int().
func normalizedCode(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	cleaned := pyStrip(*value)
	if cleaned == "" {
		return nil, nil
	}
	if pyIsDigitString(cleaned) {
		s, err := pyIntString(cleaned)
		if err != nil {
			return nil, err
		}
		return &s, nil
	}
	return &cleaned, nil
}

// normalizedCodeOf è _normalized_code(elem.text if elem is not None else None).
func normalizedCodeOf(e *element) (string, bool, error) {
	c, err := normalizedCode(textOf(e))
	if err != nil || c == nil {
		return "", false, err
	}
	return *c, true, nil
}

// intervallo è il dict {"prezzo", "fascia", "unita_misura"} di
// _parse_intervallo_prezzo.
type intervallo struct {
	prezzo      float64
	fascia      *string
	unitaMisura *string
}

// _parse_intervallo_prezzo: nil (None) se PREZZO manca.
func parseIntervalloPrezzo(iv *element) (*intervallo, error) {
	prezzoElem := iv.find("PREZZO")
	if prezzoElem == nil {
		return nil, nil
	}
	fasciaElem := iv.find("FASCIA_COMPONENTE")
	unitaElem := iv.find("UNITA_MISURA")
	prezzo, err := pyFloatOf(prezzoElem.text)
	if err != nil {
		return nil, err
	}
	return &intervallo{prezzo: prezzo, fascia: textOf(fasciaElem), unitaMisura: textOf(unitaElem)}, nil
}

// _extract_intervalli_prezzi: intervalli validi del componente.
func extractIntervalliPrezzi(comp *element) ([]intervallo, error) {
	var out []intervallo
	for _, iv := range comp.findAllDesc("IntervalloPrezzi") {
		parsed, err := parseIntervalloPrezzo(iv)
		if err != nil {
			return nil, err
		}
		if parsed != nil {
			out = append(out, *parsed)
		}
	}
	return out, nil
}

// componente è il dict di _build_componente_result: "nome" presente solo se
// esiste l'elemento NOME (hasNome), "intervalli" solo se non vuota.
type componente struct {
	hasNome    bool
	nome       *string
	intervalli []intervallo
}

// _build_componente_result.
func buildComponenteResult(comp *element, intervalli []intervallo) *componente {
	result := &componente{}
	if nome := comp.find("NOME"); nome != nil {
		result.hasNome = true
		result.nome = nome.text
	}
	if len(intervalli) > 0 {
		result.intervalli = intervalli
	}
	return result
}

// truthy replica `if comp and "intervalli" in comp and len(...) > 0`.
func (c *componente) firstPrezzo() (float64, bool) {
	if c == nil || len(c.intervalli) == 0 {
		return 0, false
	}
	return c.intervalli[0].prezzo, true
}

// _extract_componente_impresa: primo ComponenteImpresa con la MACROAREA
// indicata (confronto senza zeri iniziali); nil (None) se non trovato.
func extractComponenteImpresa(offerta *element, macroarea string) (*componente, error) {
	target, _ := normalizedCode(&macroarea)
	for _, comp := range offerta.findAllDesc("ComponenteImpresa") {
		macro := comp.find("MACROAREA")
		if macro == nil {
			continue
		}
		code, err := normalizedCode(macro.text)
		if err != nil {
			return nil, err
		}
		if code != nil && target != nil && *code == *target {
			intervalli, err := extractIntervalliPrezzi(comp)
			if err != nil {
				return nil, err
			}
			return buildComponenteResult(comp, intervalli), nil
		}
	}
	return nil, nil
}

// parsedOffer è la tupla restituita da _parse_offerta_luce
// (tipo_offerta, tipo_fascia, dati) o _parse_offerta_gas (tipo_offerta, dati;
// tipoFascia vuoto).
type parsedOffer struct {
	tipoOfferta string
	tipoFascia  string
	dati        Rate
}

type luceMetadata struct {
	tipoOfferta, tipoFascia string
	codOfferta              *string
}

func isOctopus(offerta *element) bool {
	piva := offerta.findDesc("PIVA_UTENTE")
	return piva != nil && piva.text != nil && *piva.text == OctopusPIVA
}

// tipoOffertaOf: (01=fissa, altro=variabile); false se TIPO_OFFERTA manca.
func tipoOffertaOf(offerta *element) (string, bool, error) {
	code, ok, err := normalizedCodeOf(offerta.findDesc("TIPO_OFFERTA"))
	if err != nil || !ok {
		return "", false, err
	}
	if code == "1" {
		return "fissa", true, nil
	}
	return "variabile", true, nil
}

// _validate_and_extract_luce_metadata: nil (None) se l'offerta non è luce
// Octopus.
func validateAndExtractLuceMetadata(offerta *element) (*luceMetadata, error) {
	// Verifica P.IVA Octopus
	if !isOctopus(offerta) {
		return nil, nil
	}

	// Verifica che sia offerta luce (TIPO_MERCATO=01)
	tipoMercato, ok, err := normalizedCodeOf(offerta.findDesc("TIPO_MERCATO"))
	if err != nil {
		return nil, err
	}
	if !ok || tipoMercato != "1" {
		return nil, nil
	}

	// Determina tipo offerta (01=fissa, 02=variabile)
	tipoOfferta, ok, err := tipoOffertaOf(offerta)
	if err != nil || !ok {
		return nil, err
	}

	// Determina tipo fascia (01=monoraria, 02=bioraria, 03=trioraria)
	// Se il campo manca/è non standard, usa fallback monoraria
	tipoFasciaCode, _, err := normalizedCodeOf(offerta.findDesc("TIPOLOGIA_FASCE"))
	if err != nil {
		return nil, err
	}
	tipoFascia := "monoraria"
	switch tipoFasciaCode {
	case "3":
		tipoFascia = "trioraria"
	case "2":
		tipoFascia = "bioraria"
	}

	// Estrai codice offerta (opzionale)
	codOfferta := textOf(offerta.findDesc("COD_OFFERTA"))

	return &luceMetadata{tipoOfferta: tipoOfferta, tipoFascia: tipoFascia, codOfferta: codOfferta}, nil
}

// commercializzazioneOf: costo di commercializzazione (MACROAREA=01, €/anno).
func commercializzazioneOf(offerta *element) (*float64, error) {
	comp, err := extractComponenteImpresa(offerta, "01")
	if err != nil {
		return nil, err
	}
	if p, ok := comp.firstPrezzo(); ok {
		return &p, nil
	}
	return nil, nil
}

// nomeOffertaText replica offerta_elem.find('.//NOME_OFFERTA').text, che
// solleva AttributeError se l'elemento manca.
func nomeOffertaText(offerta *element) (string, error) {
	nome := offerta.findDesc("NOME_OFFERTA")
	if nome == nil {
		return "", newPyException("AttributeError", "'NoneType' object has no attribute 'text'")
	}
	return pyOptStr(nome.text), nil
}

// _parse_offerta_luce: nil (None) se l'offerta non è luce Octopus valida.
func parseOffertaLuce(offerta *element, log logSink) (*parsedOffer, error) {
	// Valida ed estrai metadati base
	meta, err := validateAndExtractLuceMetadata(offerta)
	if err != nil || meta == nil {
		return nil, err
	}

	// Estrai costo commercializzazione (MACROAREA=01)
	commercializzazione, err := commercializzazioneOf(offerta)
	if err != nil {
		return nil, err
	}

	// Estrai prezzo energia (MACROAREA=04 legacy, MACROAREA=06 nuovo tracciato)
	compEnergia, err := extractComponenteImpresa(offerta, "04")
	if err != nil {
		return nil, err
	}
	if compEnergia == nil {
		if compEnergia, err = extractComponenteImpresa(offerta, "06"); err != nil {
			return nil, err
		}
	}
	energia, ok := compEnergia.firstPrezzo()
	if !ok {
		nome, err := nomeOffertaText(offerta)
		if err != nil {
			return nil, err
		}
		log(slog.LevelWarn, "Energia non trovata per offerta "+nome)
		return nil, nil
	}

	if meta.tipoOfferta == "fissa" {
		log(slog.LevelInfo, fmt.Sprintf("✅ Luce fissa %s: %s €/kWh, comm: %s €/anno",
			meta.tipoFascia, pyFloatRepr(energia), pyOptFloatStr(commercializzazione)))
	} else {
		log(slog.LevelInfo, fmt.Sprintf("✅ Luce variabile %s: PUN + %s €/kWh, comm: %s €/anno",
			meta.tipoFascia, pyFloatRepr(energia), pyOptFloatStr(commercializzazione)))
	}

	return &parsedOffer{
		tipoOfferta: meta.tipoOfferta,
		tipoFascia:  meta.tipoFascia,
		dati:        Rate{Energia: energia, Commercializzazione: commercializzazione, CodOfferta: meta.codOfferta},
	}, nil
}

// _parse_offerta_gas: nil (None) se l'offerta non è gas Octopus valida.
func parseOffertaGas(offerta *element, log logSink) (*parsedOffer, error) {
	// Verifica P.IVA Octopus
	if !isOctopus(offerta) {
		return nil, nil
	}

	// Estrai codice offerta (opzionale)
	codOfferta := textOf(offerta.findDesc("COD_OFFERTA"))

	// Verifica che sia offerta gas (TIPO_MERCATO=02)
	tipoMercato, ok, err := normalizedCodeOf(offerta.findDesc("TIPO_MERCATO"))
	if err != nil {
		return nil, err
	}
	if !ok || tipoMercato != "2" {
		return nil, nil
	}

	// Determina tipo offerta (01=fissa, 02=variabile)
	tipoOfferta, ok, err := tipoOffertaOf(offerta)
	if err != nil || !ok {
		return nil, err
	}

	// Estrai costo commercializzazione (MACROAREA=01)
	commercializzazione, err := commercializzazioneOf(offerta)
	if err != nil {
		return nil, err
	}

	// Estrai prezzo energia (MACROAREA=04)
	compEnergia, err := extractComponenteImpresa(offerta, "04")
	if err != nil {
		return nil, err
	}
	energia, ok := compEnergia.firstPrezzo()
	if !ok {
		nome, err := nomeOffertaText(offerta)
		if err != nil {
			return nil, err
		}
		log(slog.LevelWarn, "Energia non trovata per offerta gas "+nome)
		return nil, nil
	}

	if tipoOfferta == "fissa" {
		log(slog.LevelInfo, fmt.Sprintf("✅ Gas fisso monorario: %s €/Smc, comm: %s €/anno",
			pyFloatRepr(energia), pyOptFloatStr(commercializzazione)))
	} else {
		log(slog.LevelInfo, fmt.Sprintf("✅ Gas variabile monorario: PSV + %s €/Smc, comm: %s €/anno",
			pyFloatRepr(energia), pyOptFloatStr(commercializzazione)))
	}

	return &parsedOffer{
		tipoOfferta: tipoOfferta,
		dati:        Rate{Energia: energia, Commercializzazione: commercializzazione, CodOfferta: codOfferta},
	}, nil
}

// areraData è il dict parziale restituito da _parse_arera_xml:
// {"luce": ...}, {"gas": ...} oppure {} (servizio "").
type areraData struct {
	servizio string
	rates    ServiceRates
}

// get replica data.get(servizio, {"fissa": {}, "variabile": {}}).
func (d areraData) get(servizio string) ServiceRates {
	if d.servizio == servizio {
		return d.rates
	}
	return ServiceRates{}
}

// _empty_structure: struttura vuota per un servizio ("E" luce, "G" gas,
// altrimenti {}).
func emptyStructure(service string) areraData {
	switch service {
	case "E":
		return areraData{servizio: "luce"}
	case "G":
		return areraData{servizio: "gas"}
	}
	return areraData{}
}

func serviceName(service string) string {
	if service == "E" {
		return ServiceNameElectricity
	}
	return ServiceNameGas
}

// offerOutcome sono gli effetti del parsing di una singola offerta (log,
// risultato, eccezione), registrati durante la scansione e riprodotti solo
// se il documento è ben formato e la lista di offerte viene usata.
type offerOutcome struct {
	logs   []logEntry
	parsed *parsedOffer
	err    error
}

// offerList raccoglie, in ordine di documento, le offerte con un tag
// (<offerta> o <Offerta>): count le conta tutte (per il log "Trovate N
// offerte"), outcomes conserva solo quelle con effetti, fino alla prima
// eccezione (dopo la quale Python non elaborerebbe le successive).
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
	if out.err != nil {
		l.failed = true
	}
	if out.parsed != nil || out.err != nil || len(out.logs) > 0 {
		l.outcomes = append(l.outcomes, out)
	}
}

// areraParse è lo stato di _parse_arera_xml dopo la scansione del documento.
type areraParse struct {
	service      string
	err          error // *xmlSyntaxError (ParseError), *PyException (defusedxml) o errore di lettura
	lower, upper offerList
}

// scanAreraXML scansiona il documento ed elabora le offerte una alla volta,
// senza emettere log: gli effetti vengono riprodotti da result.
func scanAreraXML(r io.Reader, service string) *areraParse {
	p := &areraParse{service: service}
	capture := func(tag string, depth int) bool {
		return depth >= 2 && (tag == "offerta" || tag == "Offerta")
	}
	onTree := func(e *element) {
		// L'offerta catturata e le eventuali offerte annidate, in ordine di
		// documento (root.findall(".//offerta") le include tutte).
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

// result completa _parse_arera_xml: emette i log nell'ordine di Python e
// restituisce la struttura parziale, oppure l'eccezione non gestita
// sollevata dal parsing di un'offerta. head sono i primi 500 caratteri del
// contenuto (per il log di debug sugli XML malformati).
func (p *areraParse) result(head string) (areraData, error) {
	if p.err != nil {
		name := serviceName(p.service)
		if exc, ok := p.err.(*PyException); ok {
			logger.Error(fmt.Sprintf("❌ Errore inaspettato parsing XML %s: %s: %s", name, exc.Type, exc.Msg))
			return emptyStructure(p.service), nil
		}
		logger.Error(fmt.Sprintf("❌ XML malformato per %s: %v", name, p.err))
		logger.Debug("   Primi 500 caratteri: " + head)
		return emptyStructure(p.service), nil
	}

	// Trova tutte le offerte (supporto tag legacy <offerta> e formato <Offerta>)
	offerte := &p.lower
	if offerte.count == 0 {
		offerte = &p.upper
	}
	logger.Debug(fmt.Sprintf("Trovate %d offerte nel file XML", offerte.count))

	var servizio string
	switch p.service {
	case "E":
		servizio = "luce"
	case "G":
		servizio = "gas"
	default:
		return areraData{}, nil
	}
	var tariffe ServiceRates
	for _, o := range offerte.outcomes {
		for _, l := range o.logs {
			logger.Log(context.Background(), l.level, l.msg)
		}
		if o.err != nil {
			return areraData{}, o.err
		}
		if o.parsed == nil {
			continue
		}
		fascia := o.parsed.tipoFascia
		if servizio == "gas" {
			fascia = "monoraria"
		}
		tariffe.tipo(o.parsed.tipoOfferta).set(fascia, o.parsed.dati)
	}
	return areraData{servizio: servizio, rates: tariffe}, nil
}

// parseAreraXML è _parse_arera_xml(xml_content, service) su una stringa.
func parseAreraXML(xmlContent, service string) (areraData, error) {
	return scanAreraXML(strings.NewReader(xmlContent), service).result(truncateRunes(xmlContent, 500))
}
