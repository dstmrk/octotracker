# 🚀 Ottimizzazioni Future per OctoTracker

Questo documento traccia le ottimizzazioni identificate ma non ancora implementate.

---

## ✅ Implementate

- [x] **Scheduler efficiente** - Sleep-based scheduling con esecuzione esatta a orari configurati
- [x] **Smart number formatting** - Rimozione zeri trailing, virgola italiana
- [x] **Notifiche intelligenti** - Gestione casi mixed (migliorato/peggiorato)
- [x] **Webhook-only mode** - Rimosso supporto polling, semplificato codebase
- [x] **Nested structure** - Struttura JSON a 3 livelli (luce/gas → fissa/variabile → monoraria/trioraria)
- [x] **Variable rates support** - Supporto tariffe variabili (PUN/PSV + spread)
- [x] **JSONDecodeError handling** - Gestione robusta errori parsing JSON con backup automatico

---

## 🔴 Alta Priorità

### 1. Scraper Data Validation
**Categoria:** Reliability | **Sforzo:** Basso | **Impatto:** Alto

**Problema:** Se lo scraping fallisce parzialmente, salviamo comunque il JSON con dizionari vuoti `{}`. Il checker potrebbe crashare o inviare notifiche errate.

**Soluzione:**
```python
def validate_scraped_data(tariffe_data: dict) -> bool:
    """
    Verifica che abbiamo estratto almeno le tariffe essenziali
    Minimo richiesto: luce fissa monoraria + gas fisso monorario
    """
    luce_fissa = tariffe_data.get('luce', {}).get('fissa', {}).get('monoraria')
    gas_fisso = tariffe_data.get('gas', {}).get('fissa', {}).get('monoraria')

    if not luce_fissa or not gas_fisso:
        return False

    # Verifica presenza campi essenziali
    if luce_fissa.get('energia') is None or luce_fissa.get('commercializzazione') is None:
        return False
    if gas_fisso.get('energia') is None or gas_fisso.get('commercializzazione') is None:
        return False

    return True

# In scrape_octopus_tariffe(), prima di salvare:
if not validate_scraped_data(tariffe_data):
    raise ValueError("Scraping incompleto: mancano tariffe essenziali")
```

**Benefici:**
- Previene salvataggio di dati incompleti
- Errore chiaro se lo scraping fallisce
- Protezione per il checker (evita KeyError)

**File da modificare:** `scraper.py`

---

### 2. Checker Sanity Check
**Categoria:** Reliability | **Sforzo:** Basso | **Impatto:** Medio

**Problema:** Il checker non verifica se current_rates.json ha dati validi prima di confrontare.

**Soluzione:**
```python
def validate_current_rates(current_rates: dict) -> bool:
    """Verifica che current_rates abbia almeno alcune tariffe valide"""
    if not current_rates or not isinstance(current_rates, dict):
        return False

    # Verifica presenza struttura minima
    luce = current_rates.get('luce', {})
    gas = current_rates.get('gas', {})

    # Almeno una tariffa luce e una gas devono esistere
    has_luce = any(
        luce.get(tipo, {}).get(fascia)
        for tipo in ['fissa', 'variabile']
        for fascia in ['monoraria', 'trioraria']
    )
    has_gas = any(
        gas.get(tipo, {}).get(fascia)
        for tipo in ['fissa', 'variabile']
        for fascia in ['monoraria']
    )

    return has_luce and has_gas

# In check_and_notify_users(), dopo load:
if not validate_current_rates(current_rates):
    print("❌ current_rates.json non valido o incompleto")
    return
```

**Benefici:**
- Evita confronti con dati vuoti/corrotti
- Log chiaro se il file è problematico

**File da modificare:** `checker.py`

---

### 3. Input Validation con Range Realistici
**Categoria:** Security | **Sforzo:** Medio | **Impatto:** Alto

**Problema:** Gli utenti possono inserire valori negativi, zero, o numeri assurdi (es. 999 €/kWh).

**Soluzione:**
```python
def validate_price(value: str, price_type: str) -> Tuple[bool, Optional[float], str]:
    """
    Valida input prezzo utente con range realistici

    Range:
    - luce_energia: 0.01 - 2.0 €/kWh
    - luce_comm: 0.0 - 500.0 €/anno
    - gas_energia: 0.01 - 5.0 €/Smc
    - gas_comm: 0.0 - 500.0 €/anno
    """
    # Implementazione da aggiungere in bot.py
```

**Benefici:**
- Previene dati corrotti nel sistema
- UX migliorata con errori chiari
- Protezione contro input malevoli

**File da modificare:** `bot.py` (funzioni: luce_energia, luce_comm, gas_energia, gas_comm)

---

### 4. Unit Tests per Struttura Dati
**Categoria:** Testing | **Sforzo:** Medio | **Impatto:** Alto

**Problema:** Nessun test automatico per verificare la coerenza della struttura nested.

**Soluzione:** Creare `tests/test_data_structure.py`:
```python
import unittest
from pathlib import Path
import json

class TestDataStructure(unittest.TestCase):
    """Test struttura JSON nested per current_rates e users"""

    def test_current_rates_structure(self):
        """Verifica struttura luce/gas → fissa/variabile → monoraria/trioraria"""
        rates = {
            "luce": {
                "fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}},
                "variabile": {"monoraria": {"energia": 0.0088, "commercializzazione": 72.0}}
            },
            "gas": {
                "fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}}
            }
        }

        # Test accesso nested
        self.assertIn('luce', rates)
        self.assertIn('fissa', rates['luce'])
        self.assertIn('monoraria', rates['luce']['fissa'])
        self.assertEqual(rates['luce']['fissa']['monoraria']['energia'], 0.145)

    def test_user_structure(self):
        """Verifica struttura utente con tipo/fascia separati"""
        user = {
            "luce": {
                "tipo": "variabile",
                "fascia": "monoraria",
                "energia": 0.0088,
                "commercializzazione": 72.0
            },
            "gas": None
        }

        self.assertEqual(user['luce']['tipo'], 'variabile')
        self.assertEqual(user['luce']['fascia'], 'monoraria')
        self.assertIsNone(user['gas'])

    def test_last_notified_rates_structure(self):
        """Verifica struttura last_notified_rates nested"""
        last_notified = {
            "luce": {"energia": 0.0088, "commercializzazione": 72.0},
            "gas": {"energia": 0.08, "commercializzazione": 84.0}
        }

        self.assertIn('luce', last_notified)
        self.assertIn('energia', last_notified['luce'])
        self.assertNotIn('tipo', last_notified['luce'])  # Non serve salvare tipo

if __name__ == '__main__':
    unittest.main()
```

**Benefici:**
- Verifica automatica struttura dati
- Previene regressioni su refactoring
- Documentazione struttura JSON

**File da creare:** `tests/test_data_structure.py`, `tests/test_checker.py`, `tests/test_scraper.py`

---

### 5. Cache In-Memory per users.json
**Categoria:** Performance | **Sforzo:** Medio | **Impatto:** Alto (solo con 50+ utenti)

**Problema:** Ogni comando legge il file dal disco (load_users()).

**Soluzione:**
```python
class UsersCache:
    """Cache thread-safe con TTL di 5 minuti"""
    def __init__(self, ttl_seconds: int = 300):
        self._cache: Optional[Dict] = None
        self._last_load: Optional[datetime] = None
        self._lock = Lock()
        self._ttl = timedelta(seconds=ttl_seconds)

    def get(self) -> Dict:
        # Cache hit/miss logic

    def invalidate(self):
        # Invalida dopo save_users()
```

**⚠️ NOTA:** Implementare solo se il bot supera i 50 utenti. Per uso personale (1-10 utenti) è overkill.

**Benefici (solo con molti utenti):**
- Riduzione I/O del 90%+
- Risposta istantanea ai comandi
- Thread-safe per accessi concorrenti

**File da modificare:** `bot.py`, `checker.py`

---

## 🟡 Media Priorità

### 6. Type Hints Completi
**Categoria:** Best Practices | **Sforzo:** Alto | **Impatto:** Medio

**Problema:** Nessuna funzione ha type hints completi → codice meno robusto.

**Esempio:**
```python
from typing import Dict, Optional, TypedDict

class TariffaDetail(TypedDict):
    """Dettaglio singola tariffa"""
    energia: float
    commercializzazione: float

class Fornitura(TypedDict, total=False):
    """Struttura luce o gas utente"""
    tipo: str  # "fissa" | "variabile"
    fascia: str  # "monoraria" | "trioraria"
    energia: float
    commercializzazione: float

class UserRates(TypedDict, total=False):
    """Schema dati tariffe utente (nuova struttura nested)"""
    luce: Fornitura
    gas: Optional[Fornitura]
    last_notified_rates: Optional[Dict[str, TariffaDetail]]

def load_users() -> Dict[str, UserRates]:
    """Carica dati utenti con type safety"""
    # ...
```

**Benefici:**
- Type checking con mypy
- Autocompletion migliore
- Documentazione implicita
- Meno bug a runtime

**File da modificare:** Tutti (`bot.py`, `checker.py`, `scraper.py`)

---

### 7. Refactor Funzioni Lunghe
**Categoria:** Maintainability | **Sforzo:** Alto | **Impatto:** Medio

**Problema:**
- `scrape_octopus_tariffe()` - 170 righe (troppo lunga)
- `format_notification()` - 80+ righe (troppo lunga)

**Soluzione:** Estrarre in funzioni più piccole:
```python
# scraper.py
def extract_luce_from_text(text: str) -> Optional[TariffaData]:
    """Estrae tariffa luce dal testo usando regex"""
    # ...

def extract_gas_from_text(text: str) -> Optional[TariffaData]:
    """Estrae tariffa gas dal testo usando regex"""
    # ...

def extract_from_cards(page) -> tuple[Optional[TariffaData], Optional[TariffaData]]:
    """Fallback: estrai tariffe da elementi card"""
    # ...
```

**Benefici:**
- Funzioni più corte e testabili
- Responsabilità chiare
- Più facile debug

**File da modificare:** `scraper.py`, `checker.py`

---

### 8. Error Handling Specifico
**Categoria:** Best Practices | **Sforzo:** Medio | **Impatto:** Medio

**Problema:** Troppi `except Exception as e` che catturano tutto.

**Esempio migliorato:**
```python
# Invece di:
try:
    result = scrape_octopus_tariffe()
except Exception as e:  # ❌ Troppo generico
    print(f"Errore: {e}")

# Meglio:
try:
    result = scrape_octopus_tariffe()
except TimeoutError:
    print("⏱️  Timeout durante scraping")
except PlaywrightError as e:
    print(f"❌ Errore Playwright: {e}")
except json.JSONDecodeError as e:
    print(f"❌ Errore parsing JSON: {e}")
except Exception as e:
    print(f"❌ Errore inatteso: {e}")
```

**Benefici:**
- Errori più chiari nei log
- Gestione specifica per ogni tipo di errore
- Debug più semplice

**File da modificare:** Tutti

---

## 🟢 Bassa Priorità

### 9. Estrarre Magic Numbers/Strings
**Categoria:** Code Quality | **Sforzo:** Basso | **Impatto:** Basso

**Esempio:**
```python
# Invece di valori sparsi nel codice:
format_number(value, max_decimals=3)

# Estrarre in costanti:
MAX_DECIMALS_ENERGY = 3
MAX_DECIMALS_COST = 2
OCTOPUS_URL = "https://octopusenergy.it/le-nostre-tariffe"
TARIFF_NAME = "Mono-oraria Fissa"
```

---

## 📊 Riepilogo Priorità

| # | Ottimizzazione | Priorità | Sforzo | Impatto | Quando |
|---|---------------|----------|--------|---------|---------|
| 1 | Scraper validation | 🔴 Alta | Basso | Alto | Prossimo sprint |
| 2 | Checker sanity check | 🔴 Alta | Basso | Medio | Prossimo sprint |
| 3 | Input validation range | 🔴 Alta | Medio | Alto | Prossimo sprint |
| 4 | Unit tests struttura | 🔴 Alta | Medio | Alto | Quando si aggiungono features |
| 5 | Cache users.json | 🔴 Alta* | Medio | Alto* | Solo se 50+ utenti |
| 6 | Type hints | 🟡 Media | Alto | Medio | Quando si aggiungono features |
| 7 | Refactor funzioni | 🟡 Media | Alto | Medio | Se diventa difficile mantenere |
| 8 | Error handling | 🟡 Media | Medio | Medio | Quando si debugga spesso |
| 9 | Magic numbers | 🟢 Bassa | Basso | Basso | Mai urgente |

*Solo per bot con molti utenti (50+)

---

## 💡 Nota Finale

**Il codice attuale è già production-ready!** Queste ottimizzazioni sono miglioramenti incrementali, non critici per il funzionamento.

Priorità suggerita:
1. **Scraper/Checker validation** - Previene problemi reali con dati corrotti
2. **Input validation** - UX migliore e sicurezza
3. **Unit tests** - Prevenzione regressioni su refactoring futuri
4. Tutto il resto - Nice-to-have, implementare solo se necessario

Data ultima revisione: 2025-11-10
