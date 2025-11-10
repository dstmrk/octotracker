# 🚀 Ottimizzazioni per OctoTracker

Questo documento traccia le ottimizzazioni identificate per migliorare il codice.

**Note**: Il bot è già production-ready. Le ottimizzazioni qui elencate sono miglioramenti incrementali, non critici per il funzionamento.

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

### 1. Refactor Funzioni Lunghe
**Categoria:** Maintainability | **Sforzo:** Alto | **Impatto:** Medio

**Problema:**
- `scrape_octopus_tariffe()` - 184 righe (troppo lunga!)
- `format_notification()` - 110 righe (troppo lunga!)

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

### 2. Error Handling Specifico
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

### 3. Estrarre Magic Numbers/Strings
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
| 2 | Refactor funzioni | 🟡 Media | Alto | Medio | Per migliorare manutenibilità |
| 3 | Error handling | 🟡 Media | Medio | Medio | Quando si debugga spesso |
| 4 | Magic numbers | 🟢 Bassa | Basso | Basso | Mai urgente |

*Solo per bot con molti utenti (50+)

---

## 💡 Nota Finale

**Il codice attuale è già production-ready!** Queste ottimizzazioni sono miglioramenti incrementali, non critici per il funzionamento.

**Implementazioni completate**:
- ✅ Unit tests (20 test pytest: scraper + checker)
- ✅ CI/CD con GitHub Actions (unit tests + Docker build su PR)
- ✅ Migrazione a uv (10-100x più veloce di pip)
- ✅ Type hints completi (tutti i file con annotazioni complete)
- ✅ Nested JSON structure (3 livelli: utility → tipo → fascia)
- ✅ Variable rates support (tariffe PUN/PSV + spread)
- ✅ Graceful degradation (dati parziali gestiti correttamente)
- ✅ JSONDecodeError handling con backup automatico

Data ultima revisione: 2025-11-10
