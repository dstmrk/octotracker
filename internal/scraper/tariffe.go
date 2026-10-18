package scraper

import (
	"strings"

	"github.com/dstmrk/octotracker/internal/database"
)

// Rate è il dict di una singola tariffa letta da ARERA:
// {"energia": float, "commercializzazione": float | None, "cod_offerta": str | None}.
// Per le tariffe fisse Energia è il prezzo (€/kWh o €/Smc), per le variabili
// è lo spread da sommare a PUN/PSV.
type Rate struct {
	Energia             float64
	Commercializzazione *float64
	CodOfferta          *string
}

// FasciaRate è una coppia fascia → tariffa di Fasce.
type FasciaRate struct {
	Fascia string
	Rate   Rate
}

// Fasce è il dict fascia → tariffa ("monoraria", "bioraria", "trioraria")
// con la semantica dei dict Python: ordine della prima inserzione, l'ultima
// assegnazione vince.
type Fasce []FasciaRate

// Get replica fasce.get(fascia).
func (f Fasce) Get(fascia string) (Rate, bool) {
	for _, fr := range f {
		if fr.Fascia == fascia {
			return fr.Rate, true
		}
	}
	return Rate{}, false
}

// set replica fasce[fascia] = rate.
func (f *Fasce) set(fascia string, r Rate) {
	for i := range *f {
		if (*f)[i].Fascia == fascia {
			(*f)[i].Rate = r
			return
		}
	}
	*f = append(*f, FasciaRate{Fascia: fascia, Rate: r})
}

// ServiceRates è la struttura {"fissa": {...}, "variabile": {...}} di un
// servizio.
type ServiceRates struct {
	Fissa     Fasce
	Variabile Fasce
}

// tipo restituisce il dict del tipo di offerta ("fissa" o "variabile").
func (s *ServiceRates) tipo(tipo string) *Fasce {
	if tipo == "fissa" {
		return &s.Fissa
	}
	return &s.Variabile
}

// Tariffe è il dict restituito da fetch_octopus_tariffe:
// {"luce": ..., "gas": ..., "data_aggiornamento": "YYYY-MM-DD"} più
// "data_fonte_xml" quando almeno un file è stato scaricato.
type Tariffe struct {
	Luce              ServiceRates
	Gas               ServiceRates
	DataAggiornamento string
	// DataFonteXML è nil quando la chiave "data_fonte_xml" è assente.
	DataFonteXML *string
}

// _flatten_rates: converte la struttura nested in righe per
// Store.SaveRatesBatch, nell'ordine luce/gas → fissa/variabile → fasce in
// ordine di inserimento.
func flattenRates(luce, gas ServiceRates) []database.RateRecord {
	var rates []database.RateRecord
	for _, s := range []struct {
		servizio string
		data     ServiceRates
	}{{"luce", luce}, {"gas", gas}} {
		for _, t := range []struct {
			tipo  string
			fasce Fasce
		}{{"fissa", s.data.Fissa}, {"variabile", s.data.Variabile}} {
			for _, fr := range t.fasce {
				rates = append(rates, database.RateRecord{
					Servizio:            s.servizio,
					Tipo:                t.tipo,
					Fascia:              fr.Fascia,
					Energia:             fr.Rate.Energia,
					Commercializzazione: fr.Rate.Commercializzazione,
					CodOfferta:          fr.Rate.CodOfferta,
				})
			}
		}
	}
	return rates
}

// ========== repr() e json.dumps ==========

// pyValue è un valore del dict Python con ordine delle chiavi, usato per
// generare repr() e json.dumps del risultato.
type pyValue struct {
	kind  byte // 'd' dict, 's' str, 'f' float, 'n' None
	keys  []string
	vals  []pyValue
	str   string
	float float64
}

func pyDict() pyValue { return pyValue{kind: 'd'} }

func (d *pyValue) put(k string, v pyValue) {
	d.keys = append(d.keys, k)
	d.vals = append(d.vals, v)
}

func pyStrVal(s string) pyValue { return pyValue{kind: 's', str: s} }

func pyOptStrVal(s *string) pyValue {
	if s == nil {
		return pyValue{kind: 'n'}
	}
	return pyStrVal(*s)
}

func pyOptFloatVal(f *float64) pyValue {
	if f == nil {
		return pyValue{kind: 'n'}
	}
	return pyValue{kind: 'f', float: *f}
}

func (r Rate) pyValue() pyValue {
	d := pyDict()
	d.put("energia", pyValue{kind: 'f', float: r.Energia})
	d.put("commercializzazione", pyOptFloatVal(r.Commercializzazione))
	d.put("cod_offerta", pyOptStrVal(r.CodOfferta))
	return d
}

func (f Fasce) pyValue() pyValue {
	d := pyDict()
	for _, fr := range f {
		d.put(fr.Fascia, fr.Rate.pyValue())
	}
	return d
}

func (s ServiceRates) pyValue() pyValue {
	d := pyDict()
	d.put("fissa", s.Fissa.pyValue())
	d.put("variabile", s.Variabile.pyValue())
	return d
}

func (t Tariffe) pyValue() pyValue {
	d := pyDict()
	d.put("luce", t.Luce.pyValue())
	d.put("gas", t.Gas.pyValue())
	d.put("data_aggiornamento", pyStrVal(t.DataAggiornamento))
	if t.DataFonteXML != nil {
		d.put("data_fonte_xml", pyStrVal(*t.DataFonteXML))
	}
	return d
}

func (v pyValue) repr(b *strings.Builder) {
	switch v.kind {
	case 'd':
		b.WriteByte('{')
		for i, k := range v.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(pyStrRepr(k))
			b.WriteString(": ")
			v.vals[i].repr(b)
		}
		b.WriteByte('}')
	case 's':
		b.WriteString(pyStrRepr(v.str))
	case 'f':
		b.WriteString(pyFloatRepr(v.float))
	default:
		b.WriteString("None")
	}
}

// dumps replica json.dumps(v, indent=indent) (indent < 0: senza indent, con
// i separatori di default ", " e ": ").
func (v pyValue) dumps(b *strings.Builder, indent, level int) {
	switch v.kind {
	case 'd':
		if len(v.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range v.keys {
			switch {
			case indent >= 0:
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteByte('\n')
				b.WriteString(strings.Repeat(" ", indent*(level+1)))
			case i > 0:
				b.WriteString(", ")
			}
			b.WriteString(pyJSONString(k))
			b.WriteString(": ")
			v.vals[i].dumps(b, indent, level+1)
		}
		if indent >= 0 {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat(" ", indent*level))
		}
		b.WriteByte('}')
	case 's':
		b.WriteString(pyJSONString(v.str))
	case 'f':
		b.WriteString(pyJSONFloat(v.float))
	default:
		b.WriteString("null")
	}
}

// String restituisce repr() del dict Python, così
// fmt.Sprintf("✅ Scraper completato: %v", result) produce la stessa riga di
// log di bot.py.
func (t Tariffe) String() string {
	var b strings.Builder
	t.pyValue().repr(&b)
	return b.String()
}

// JSONIndent restituisce json.dumps(result, indent=2), come stampato dal
// blocco __main__ di data_reader.py.
func (t Tariffe) JSONIndent() string {
	var b strings.Builder
	t.pyValue().dumps(&b, 2, 0)
	return b.String()
}
