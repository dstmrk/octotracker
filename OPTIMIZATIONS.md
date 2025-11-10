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
- [x] **Scraper partial data** - Warning log per tariffe non trovate, salvataggio dati parziali
- [x] **Checker graceful degradation** - Gestione automatica current_rates parziali con `.get()`
- [x] **Unit Tests** - Test pytest per scraper, checker, e struttura dati (21 test totali)
- [x] **uv migration** - Migrato da pip a uv per dependency management (10-100x più veloce)
- [x] **GitHub Actions CI** - Test automatici e Docker build su PR verso main

---

## 🔴 Alta Priorità

### 1. Cache In-Memory per users.json
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

### 2. Type Hints Completi
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

### 3. Refactor Funzioni Lunghe
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

### 4. Error Handling Specifico
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

### 5. Estrarre Magic Numbers/Strings
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
| 1 | Cache users.json | 🔴 Alta* | Medio | Alto* | Solo se 50+ utenti |
| 2 | Type hints | 🟡 Media | Alto | Medio | Quando si aggiungono features |
| 3 | Refactor funzioni | 🟡 Media | Alto | Medio | Se diventa difficile mantenere |
| 4 | Error handling | 🟡 Media | Medio | Medio | Quando si debugga spesso |
| 5 | Magic numbers | 🟢 Bassa | Basso | Basso | Mai urgente |

*Solo per bot con molti utenti (50+)

---

## 💡 Nota Finale

**Il codice attuale è già production-ready!** Queste ottimizzazioni sono miglioramenti incrementali, non critici per il funzionamento.

**Note implementazioni recenti**:
- ✅ Unit tests implementati (21 test pytest)
- ✅ CI/CD con GitHub Actions (test automatici su PR)
- ✅ Migrazione a uv (10-100x più veloce di pip)
- Scraper/Checker già gestiscono dati parziali gracefully (warning logs + `.get()`)
- Input validation non necessaria (mostriamo riepilogo all'utente dopo inserimento)

Data ultima revisione: 2025-11-10
