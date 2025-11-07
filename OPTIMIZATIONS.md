# 🚀 Ottimizzazioni Future per OctoTracker

Questo documento traccia le ottimizzazioni identificate ma non ancora implementate.

---

## ✅ Implementate

- [x] **Scheduler efficiente** - Ridotto polling da ogni 30s a ogni 60s con sleep dinamico
- [x] **Smart number formatting** - Rimozione zeri trailing, virgola italiana
- [x] **Notifiche intelligenti** - Gestione casi mixed (migliorato/peggiorato)

---

## 🔴 Alta Priorità

### 1. Input Validation con Range Realistici
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

### 2. Cache In-Memory per users.json
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

### 3. Type Hints Completi
**Categoria:** Best Practices | **Sforzo:** Alto | **Impatto:** Medio

**Problema:** Nessuna funzione ha type hints completi → codice meno robusto.

**Esempio:**
```python
from typing import Dict, Optional, TypedDict

class UserRates(TypedDict, total=False):
    """Schema dati tariffe utente"""
    luce_energia: float
    luce_comm: float
    gas_energia: Optional[float]
    gas_comm: Optional[float]
    last_notified_rates: Optional[Dict[str, float]]

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

### 4. Refactor Funzioni Lunghe
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

### 5. Error Handling Specifico
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

### 6. Estrarre Magic Numbers/Strings
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

### 7. Screenshot Solo in Debug Mode
**Categoria:** Performance | **Sforzo:** Basso | **Impatto:** Basso

**Problema:** Lo screenshot viene sempre salvato, anche in produzione.

**Soluzione:**
```python
DEBUG = os.getenv('DEBUG', 'false').lower() == 'true'

if DEBUG:
    screenshot_path = DATA_DIR / "last_scrape.png"
    page.screenshot(path=str(screenshot_path))
```

---

### 8. Code Duplication - Logica Luce/Gas
**Categoria:** Maintainability | **Sforzo:** Medio | **Impatto:** Basso

**Problema:** La logica per controllare luce e gas è quasi identica in `checker.py`.

**Soluzione:** Estrarre in funzione generica parametrica.

---

### 9. Rimuovere Keep-Alive in Polling Mode
**Categoria:** Code Quality | **Sforzo:** Basso | **Impatto:** Basso

**Problema:** Il keep-alive in modalità polling è ridondante (il bot è già sempre attivo con il polling).

**Soluzione:** Disabilitare completamente in polling mode o rimuovere del tutto.

---

## 📊 Riepilogo Priorità

| # | Ottimizzazione | Priorità | Sforzo | Impatto | Quando |
|---|---------------|----------|--------|---------|---------|
| 1 | Input validation | 🔴 Alta | Medio | Alto | Prossimo sprint |
| 2 | Cache users.json | 🔴 Alta* | Medio | Alto* | Solo se 50+ utenti |
| 3 | Type hints | 🟡 Media | Alto | Medio | Quando si aggiungono features |
| 4 | Refactor funzioni | 🟡 Media | Alto | Medio | Se diventa difficile mantenere |
| 5 | Error handling | 🟡 Media | Medio | Medio | Quando si debugga spesso |
| 6 | Magic numbers | 🟢 Bassa | Basso | Basso | Mai urgente |
| 7 | Screenshot debug | 🟢 Bassa | Basso | Basso | Mai urgente |
| 8 | Code duplication | 🟢 Bassa | Medio | Basso | Mai urgente |
| 9 | Rimuovi keep-alive | 🟢 Bassa | Basso | Basso | Mai urgente |

*Solo per bot con molti utenti (50+)

---

## 💡 Nota Finale

**Il codice attuale è già production-ready!** Queste ottimizzazioni sono miglioramenti incrementali, non critici per il funzionamento.

Priorità suggerita:
1. **Input validation** - Previene problemi reali
2. Tutto il resto - Nice-to-have, implementare solo se necessario

Data ultima revisione: 2025-11-07
