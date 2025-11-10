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

### test_scraper.py
Verifica il formato JSON prodotto dallo scraper:
- ✅ Struttura completa con tutte le tariffe
- ✅ Dati parziali (solo luce o solo gas)
- ✅ Dati completamente vuoti (scraping fallito)
- ✅ Tipi di dati corretti (float per prezzi, string per date)
- ✅ Serializzabilità JSON

**Non esegue scraping reale** - simula output possibili.

### test_checker.py
Verifica la logica di confronto tariffe:
- ✅ Nessun risparmio (tariffe uguali)
- ✅ Risparmio su energia
- ✅ Casi mixed (energia migliore, commercializzazione peggiore)
- ✅ NO cross-type comparison (fissa vs variabile)
- ✅ NO cross-fascia comparison (monoraria vs trioraria)
- ✅ Utenti con e senza gas
- ✅ current_rates parziali
- ✅ current_rates completamente vuoti
- ✅ Formato users.json con e senza last_notified_rates

**Non invia notifiche reali** - verifica solo la logica.

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

### users.json
```json
{
  "123456789": {
    "luce": {
      "tipo": "variabile",
      "fascia": "monoraria",
      "energia": 0.0088,
      "commercializzazione": 72.0
    },
    "gas": {
      "tipo": "fissa",
      "fascia": "monoraria",
      "energia": 0.456,
      "commercializzazione": 84.0
    },
    "last_notified_rates": {
      "luce": {"energia": 0.0088, "commercializzazione": 72.0},
      "gas": {"energia": 0.456, "commercializzazione": 84.0}
    }
  }
}
```

## Coverage

I test coprono:
- ✅ Tutti i casi di dati parziali/completi
- ✅ Logica same-type-only comparison
- ✅ Gestione utenti con/senza gas
- ✅ Formato last_notified_rates (opzionale)
- ✅ Edge cases (dati vuoti, mismatch tipo/fascia)

## Aggiungere Nuovi Test

1. Crea un nuovo file `test_*.py` nella cartella `tests/`
2. Usa pytest (sintassi semplice con `assert`)
3. Nomina le funzioni test con prefisso `test_`
4. Esegui con `uv run pytest tests/ -v`

## Note

- I test non richiedono Playwright (nessun browser)
- I test non richiedono token Telegram (nessuna API call)
- I test sono veloci (~0.01s totali)
- Perfetti per CI/CD pipeline
