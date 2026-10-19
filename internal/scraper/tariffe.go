package scraper

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/dstmrk/octotracker/internal/database"
)

// Rate è una singola tariffa letta da ARERA. Per le tariffe fisse Energia è
// il prezzo (€/kWh o €/Smc), per le variabili è lo spread da sommare a
// PUN/PSV.
type Rate struct {
	Energia             float64  `json:"energia"`
	Commercializzazione *float64 `json:"commercializzazione"`
	CodOfferta          *string  `json:"cod_offerta"`
}

// FasciaRate è una coppia fascia → tariffa di Fasce.
type FasciaRate struct {
	Fascia string
	Rate   Rate
}

// Fasce associa la fascia ("monoraria", "bioraria", "trioraria") alla
// tariffa, nell'ordine della prima inserzione; l'ultima assegnazione vince.
type Fasce []FasciaRate

// Get restituisce la tariffa della fascia.
func (f Fasce) Get(fascia string) (Rate, bool) {
	for _, fr := range f {
		if fr.Fascia == fascia {
			return fr.Rate, true
		}
	}
	return Rate{}, false
}

// set assegna la tariffa alla fascia.
func (f *Fasce) set(fascia string, r Rate) {
	for i := range *f {
		if (*f)[i].Fascia == fascia {
			(*f)[i].Rate = r
			return
		}
	}
	*f = append(*f, FasciaRate{Fascia: fascia, Rate: r})
}

// MarshalJSON scrive le fasce come oggetto JSON, nell'ordine di inserimento.
func (f Fasce) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, fr := range f {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(fr.Fascia)
		v, err := json.Marshal(fr.Rate)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// ServiceRates sono le tariffe fisse e variabili di un servizio.
type ServiceRates struct {
	Fissa     Fasce `json:"fissa"`
	Variabile Fasce `json:"variabile"`
}

// tipo restituisce le fasce del tipo di offerta ("fissa" o "variabile").
func (s *ServiceRates) tipo(tipo string) *Fasce {
	if tipo == "fissa" {
		return &s.Fissa
	}
	return &s.Variabile
}

// Tariffe è il risultato di FetchOctopusTariffe.
type Tariffe struct {
	Luce              ServiceRates `json:"luce"`
	Gas               ServiceRates `json:"gas"`
	DataAggiornamento string       `json:"data_aggiornamento"`
	// DataFonteXML è la data del file più recente, nil se nessun download è
	// riuscito.
	DataFonteXML *string `json:"data_fonte_xml,omitempty"`
}

// String restituisce le tariffe in JSON (per i log).
func (t Tariffe) String() string {
	return t.marshal(json.Marshal(t))
}

// JSONIndent restituisce le tariffe in JSON indentato (output di
// `octotracker scrape`).
func (t Tariffe) JSONIndent() string {
	return t.marshal(json.MarshalIndent(t, "", "  "))
}

func (t Tariffe) marshal(b []byte, err error) string {
	if err != nil {
		return fmt.Sprintf("tariffe non serializzabili: %v", err)
	}
	return string(b)
}

// flattenRates converte le tariffe nelle righe per Store.SaveRatesBatch,
// nell'ordine luce/gas → fissa/variabile → fasce in ordine di inserimento.
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
