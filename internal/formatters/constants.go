package formatters

// Costanti condivise tra i moduli di OctoTracker (port di constants.py).

// Labels per tipi di tariffa.
const (
	// LabelFixedPrice è la label delle tariffe a prezzo fisso (LABEL_FIXED_PRICE).
	LabelFixedPrice = "Prezzo fisso"
	// LabelVariableElectricity è la label dello spread luce (LABEL_VARIABLE_ELECTRICITY).
	LabelVariableElectricity = "Spread (PUN +)"
	// LabelVariableGas è la label dello spread gas (LABEL_VARIABLE_GAS).
	LabelVariableGas = "Spread (PSV +)"
)

// Decimali per formattazione numeri.
const (
	// MaxDecimalsEnergy: decimali per prezzi energia e spread (es. 0.0088 €/kWh).
	MaxDecimalsEnergy = 4
	// MaxDecimalsCost: decimali per costi commercializzazione (€/anno).
	MaxDecimalsCost = 2
)

// MaxNumericInputLength è la lunghezza massima per input numerici (protezione
// attacchi). In Python è confrontata con len(text), cioè con il numero di
// caratteri Unicode (rune in Go), non di byte.
const MaxNumericInputLength = 10

// Messaggi di errore comuni.
const (
	// ErrorValueNegative è il messaggio per valori negativi (ERROR_VALUE_NEGATIVE).
	ErrorValueNegative = "❌ Il valore deve essere maggiore o uguale a zero"
	// ErrorInputTooLong è il messaggio per input troppo lunghi (ERROR_INPUT_TOO_LONG).
	ErrorInputTooLong = "❌ Il valore inserito è troppo lungo (max 10 caratteri)"
)
