# Unit Tests per OctoTracker

Questi test verificano il formato dei dati e la logica di confronto senza eseguire scraping reale o operazioni di rete.

## Esecuzione Test

### Opzione 1: Container Docker (Consigliata)
```bash
# Esegui i test nel container con tutte le dipendenze
docker compose exec octotracker uv run pytest tests/ -v

# Oppure test specifici
docker compose exec octotracker uv run pytest tests/test_scraper.py -v
docker compose exec octotracker uv run pytest tests/test_checker.py -v
```

### Opzione 2: Ambiente Locale
Assicurati di avere tutte le dipendenze installate:
```bash
uv sync
uv run pytest tests/ -v
```

## Test Implementati

### test_scraper.py (6 test)
Verifica il formato JSON prodotto dallo scraper:
- ✅ Struttura completa con tutte le tariffe
- ✅ Dati parziali (solo luce o solo gas)
- ✅ Dati completamente vuoti (scraping fallito)
- ✅ Tipi di dati corretti (float per prezzi, string per date)
- ✅ Serializzabilità JSON

**Non esegue scraping reale** - simula output possibili.

### test_checker.py (14 test)
Verifica la logica di confronto tariffe:
- ✅ Nessun risparmio (tariffe uguali)
- ✅ Risparmio su energia
- ✅ Casi mixed (energia migliore, commercializzazione peggiore)
- ✅ NO cross-type comparison (fissa vs variabile)
- ✅ NO cross-fascia comparison (monoraria vs trioraria)
- ✅ Utenti con e senza gas
- ✅ current_rates parziali
- ✅ current_rates completamente vuoti
- ✅ Database SQLite con e senza last_notified_rates

**Non invia notifiche reali** - verifica solo la logica.

### test_bot.py (27 test)
Verifica flussi conversazionali Telegram e gestione input:

**Comandi:**
- ✅ `/start` - registrazione nuovo utente
- ✅ `/update` - aggiornamento tariffe (alias di /start)
- ✅ `/status` - visualizzazione dati salvati
- ✅ `/remove` - cancellazione dati utente
- ✅ `/cancel` - annullamento conversazione corrente
- ✅ `/help` - visualizzazione comandi disponibili

**Flussi conversazionali:**
- ✅ Tariffa fissa / variabile monoraria / trioraria
- ✅ Utenti con luce + gas / solo luce
- ✅ Salvataggio dati completo

**Gestione errori input:**
- ✅ Input non numerici (stringhe testuali)
- ✅ Input vuoti
- ✅ Caratteri speciali (€, virgole multiple, ecc.)
- ✅ Valori negativi e molto grandi (documentati)
- ✅ Separatori decimali (virgola e punto)

**Non invia messaggi Telegram reali** - usa mock per simulare Update/Context.

## Strutture Dati Testate

### current_rates.json
```json
{
  "luce": {
    "fissa": {
      "monoraria": {"energia": 0.145, "commercializzazione": 72.0}
    },
    "variabile": {
      "monoraria": {"energia": 0.0088, "commercializzazione": 72.0},
      "trioraria": {"energia": 0.0088, "commercializzazione": 72.0}
    }
  },
  "gas": {
    "fissa": {
      "monoraria": {"energia": 0.456, "commercializzazione": 84.0}
    },
    "variabile": {
      "monoraria": {"energia": 0.08, "commercializzazione": 84.0}
    }
  },
  "data_aggiornamento": "2025-11-10"
}
```

### users.db (SQLite)
**Schema tabella `users`:**
```sql
CREATE TABLE users (
    user_id TEXT PRIMARY KEY,
    luce_tipo TEXT NOT NULL,
    luce_fascia TEXT NOT NULL,
    luce_energia REAL NOT NULL,
    luce_commercializzazione REAL NOT NULL,
    gas_tipo TEXT,
    gas_fascia TEXT,
    gas_energia REAL,
    gas_commercializzazione REAL,
    last_notified_rates TEXT,  -- JSON: {"luce": {...}, "gas": {...}}
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Formato dict restituito da `load_user()`:**
```python
{
    "luce": {
        "tipo": "variabile",
        "fascia": "monoraria",
        "energia": 0.0088,
        "commercializzazione": 72.0
    },
    "gas": {  # None se utente non ha gas
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.456,
        "commercializzazione": 84.0
    },
    "last_notified_rates": {  # Campo opzionale
        "luce": {"energia": 0.0088, "commercializzazione": 72.0},
        "gas": {"energia": 0.456, "commercializzazione": 84.0}
    }
}
```

## Coverage

I test coprono:
- ✅ Tutti i casi di dati parziali/completi (scraper + checker)
- ✅ Logica same-type-only comparison (checker)
- ✅ Gestione utenti con/senza gas (checker + bot)
- ✅ Formato last_notified_rates (checker)
- ✅ Edge cases (dati vuoti, mismatch tipo/fascia)
- ✅ Tutti i comandi Telegram (bot)
- ✅ Flussi conversazionali completi (bot)
- ✅ Validazione input utente con error handling (bot)

**Totale: 47 test** (6 scraper + 14 checker + 27 bot)

## Aggiungere Nuovi Test

1. Crea un nuovo file `test_*.py` nella cartella `tests/`
2. Usa pytest (sintassi semplice con `assert`)
3. Per test async del bot: usa `@pytest.mark.asyncio` e mock Update/Context
4. Nomina le funzioni test con prefisso `test_`
5. Esegui con `uv run pytest tests/ -v`

## Note

- I test non richiedono Playwright (nessun browser per scraper)
- I test non richiedono token Telegram (mock per bot)
- I test sono veloci (~0.85s totali per 47 test)
- Perfetti per CI/CD pipeline
- pytest-asyncio gestisce automaticamente i test async del bot
