"""
Costanti condivise tra i moduli di OctoTracker
"""

# Labels per tipi di tariffa
LABEL_FIXED_PRICE = "Prezzo fisso"
LABEL_VARIABLE_ELECTRICITY = "Spread (PUN +)"
LABEL_VARIABLE_GAS = "Spread (PSV +)"

# Decimali per formattazione numeri
MAX_DECIMALS_ENERGY = 4  # Per prezzi energia e spread (es. 0.0088 €/kWh)
MAX_DECIMALS_COST = 2  # Per costi commercializzazione (€/anno)

# Validazione input numerici
MAX_NUMERIC_INPUT_LENGTH = 10  # Lunghezza massima per input numerici (protezione attacchi)

# Messaggi di errore comuni
ERROR_VALUE_NEGATIVE = "❌ Il valore deve essere maggiore o uguale a zero"
ERROR_INPUT_TOO_LONG = (
    f"❌ Il valore inserito è troppo lungo (max {MAX_NUMERIC_INPUT_LENGTH} caratteri)"
)
