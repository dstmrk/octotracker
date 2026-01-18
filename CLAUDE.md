# Linee Guida per lo Sviluppo - OctoTracker

Questo documento contiene le linee guida e le best practices per contribuire al progetto OctoTracker.

## 📦 Gestione Dipendenze

**Usa `uv` invece di `pip`** per tutte le operazioni con le dipendenze:

```bash
# Installa dipendenze
uv sync

# Installa dipendenze di sviluppo
uv sync --extra dev

# Esegui comandi nell'ambiente virtuale
uv run <comando>

# Attiva l'ambiente virtuale manualmente
source .venv/bin/activate
```

## 🎨 Formattazione e Linting

### Ruff
Formattatore e linter veloce (configurato in `pyproject.toml`, line-length 100):

```bash
# Formatta i file modificati
uv run ruff format <file1.py> <file2.py>

# Formatta tutto il progetto
uv run ruff format .

# Check formattazione senza modificare
uv run ruff format --check .

# Linting con auto-fix
uv run ruff check <file.py> --fix

# Linting tutto il progetto
uv run ruff check .

# Solo check senza modifiche
uv run ruff check . --no-fix
```

**⚠️ IMPORTANTE**: Esegui sempre `ruff format` e `ruff check` prima di committare!

## ✅ Testing

### Eseguire i Test

```bash
# Tutti i test
source .venv/bin/activate && pytest

# Test specifico file
source .venv/bin/activate && pytest tests/test_bot.py

# Test specifico con verbose
source .venv/bin/activate && pytest tests/test_bot.py -v

# Test con pattern
source .venv/bin/activate && pytest -k "test_status"
```

### Code Coverage

**Requisito: Coverage > 80% per SonarCloud**

```bash
# Coverage per un modulo specifico
source .venv/bin/activate && pytest tests/test_registration.py \
  --cov=registration \
  --cov-report=term-missing

# Coverage completa
source .venv/bin/activate && pytest \
  --cov=. \
  --cov-report=term-missing \
  --cov-report=html

# Visualizza report HTML
open htmlcov/index.html  # macOS
xdg-open htmlcov/index.html  # Linux
```

**Verifica sempre che il nuovo codice abbia coverage > 80%!**

## 🏗️ Struttura del Progetto

```
octotracker/
├── bot.py                    # Main bot: orchestrazione, setup, scheduler
├── handlers/                 # Handler bot organizzati
│   ├── __init__.py
│   ├── registration.py      # Conversazione registrazione tariffe
│   ├── feedback.py          # Sistema feedback utenti
│   └── commands.py          # Comandi utility (status, help, etc.)
├── checker.py               # Verifica tariffe e notifiche
├── database.py              # Gestione database SQLite
├── data_reader.py           # Scraper tariffe Octopus Energy
├── formatters.py            # Formattatori output
├── constants.py             # Costanti globali
├── health_handler.py        # Health check endpoint
├── broadcast.py             # Sistema broadcast messaggi
├── tests/
│   ├── test_bot.py          # Test comandi bot
│   ├── test_registration.py # Test conversazione registrazione
│   ├── test_feedback.py     # Test sistema feedback
│   └── ...
├── Dockerfile               # Container produzione
├── docker-compose.yml       # Orchestrazione Docker
└── pyproject.toml           # Configurazione progetto
```

**Principi architetturali:**
- `bot.py` contiene SOLO orchestrazione (main, setup, scheduler, error handler)
- Handler conversazioni → `handlers/` subdirectory
- Logica business → moduli root (checker.py, database.py, etc.)
- Tests → `tests/` con naming `test_<modulo>.py`

## 📝 Workflow di Sviluppo

### 1. Prima di iniziare

```bash
# Assicurati che l'ambiente sia aggiornato
uv sync --extra dev

# Crea/cambia branch
git checkout -b feature/nome-feature
```

### 2. Durante lo sviluppo

```bash
# Formatta il codice frequentemente
uv run ruff format <file.py>
uv run ruff check <file.py> --fix

# Esegui i test rilevanti
source .venv/bin/activate && pytest tests/test_<modulo>.py -v
```

### 3. Prima del commit

**CHECKLIST OBBLIGATORIA:**

- [ ] `uv run ruff format .` → Nessun file modificato
- [ ] `uv run ruff check . --fix` → Nessun errore
- [ ] `source .venv/bin/activate && pytest` → Tutti i test passano
- [ ] `source .venv/bin/activate && pytest --cov=<nuovo_modulo>` → Coverage > 80%

### 4. Commit

```bash
# Aggiungi i file
git add <files>

# Commit con messaggio descrittivo
git commit -m "tipo: Descrizione breve

Descrizione dettagliata del cambiamento.

- Punto 1
- Punto 2
"

# Push
git push -u origin <branch-name>
```

Tipi di commit:
- `feat`: Nuova funzionalità
- `fix`: Correzione bug
- `refactor`: Refactoring codice
- `test`: Aggiunta/modifica test
- `docs`: Documentazione
- `chore`: Manutenzione/configurazione

## 🧪 Best Practices per i Test

### Struttura Test

```python
"""
Docstring che descrive cosa testa questo modulo
"""
import pytest
from unittest.mock import AsyncMock, MagicMock

# Import modulo da testare
from modulo import funzione


@pytest.fixture
def fixture_name():
    """Descrizione fixture"""
    # Setup
    yield oggetto
    # Teardown (opzionale)


@pytest.mark.asyncio
async def test_nome_descrittivo():
    """Descrizione del test"""
    # Arrange (setup)
    input_data = ...

    # Act (esecuzione)
    result = await funzione(input_data)

    # Assert (verifica)
    assert result == expected
```

### Coverage Requisiti

- **Nuovo codice**: > 80% coverage obbligatorio
- **File modificati**: Mantieni o migliora la coverage esistente
- **Casi da testare**:
  - Happy path (caso normale)
  - Edge cases (valori limite)
  - Error handling (gestione errori)
  - Input validation (validazione input)

## 🐙 Pattern Consolidati

### Moduli Conversazione (handlers/)

Quando crei un nuovo conversation handler:

1. **Crea file in handlers/** (es. `handlers/nuova_feature.py`)
2. **Struttura standard**:
   ```python
   #!/usr/bin/env python3
   """
   Docstring descrittivo del modulo
   """
   import logging
   from telegram import Update
   from telegram.ext import ContextTypes, ConversationHandler

   # Setup logger
   logger = logging.getLogger(__name__)

   # Costanti e stati conversazione
   STATO_1, STATO_2 = range(2)

   # Handler functions (async)
   async def handler_1(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
       """Docstring funzione"""
       logger.info(f"User {update.effective_user.id}: azione")
       # ...
   ```

3. **Import in bot.py**:
   ```python
   from handlers.nuova_feature import (
       STATO_1,
       STATO_2,
       handler_1,
       handler_2,
   )
   ```

4. **Test completi**:
   - File `tests/test_nuova_feature.py`
   - Test per ogni handler
   - Test validazione input
   - Test flussi completi
   - Test edge cases
   - Coverage > 80%

### Logging Best Practices

**Ogni modulo deve avere il proprio logger:**

```python
import logging

logger = logging.getLogger(__name__)  # NON usare root logger!

# Log appropriati
logger.debug("Info dettagliate per debug")
logger.info("Operazione normale completata")
logger.warning("Situazione anomala ma gestibile")
logger.error("Errore che richiede attenzione")
```

**Quando loggare:**
- `INFO`: Operazioni importanti (es. "User X registrato")
- `WARNING`: Situazioni anomale (es. "Tentativo login fallito")
- `ERROR`: Errori gestiti (es. "Database error durante save")
- `DEBUG`: Solo per sviluppo (disabilitato in produzione)

### Docstring Obbligatorie

**Ogni modulo e funzione pubblica deve avere docstring:**

```python
"""
Modulo per gestione tariffe Octopus Energy

Responsabilità:
- Scraping dati ARERA
- Parsing XML offerte
- Salvataggio tariffe in JSON
"""

async def fetch_rates(service: str) -> dict:
    """
    Recupera tariffe Octopus per servizio specificato

    Args:
        service: "elettricita" o "gas"

    Returns:
        Dict con tariffe parsate

    Raises:
        ValueError: Se service non valido
    """
```

### Import da Altri Moduli

**Pattern standard per import:**

```python
# Import standard library
import asyncio
import logging
import os

# Import terze parti
from telegram import Update
from telegram.ext import ContextTypes

# Import moduli interni (ordine alfabetico)
from checker import format_number
from database import load_user
from handlers.registration import LUCE_ENERGIA
```

**Uso di `from handlers.*`:**

```python
# ✅ CORRETTO - Import da handlers/
from handlers.commands import status, help_command
from handlers.feedback import feedback_command
from handlers.registration import start

# ❌ SBAGLIATO - Import vecchio stile (pre-refactoring)
from registration import start  # Questo non funziona più!
```

## 🔍 Debugging

```bash
# Test con output dettagliato
source .venv/bin/activate && pytest tests/test_file.py -v -s

# Test con print statements visibili
source .venv/bin/activate && pytest tests/test_file.py -s

# Test con breakpoint
# Aggiungi `breakpoint()` nel codice, poi:
source .venv/bin/activate && pytest tests/test_file.py -s
```

## 🐳 Docker Best Practices

### Build Locale

```bash
# Build immagine (dalla root del progetto)
docker build -t octotracker:latest .

# Verifica dimensione immagine
docker images octotracker:latest

# Run container locale
docker run -d \
  --name octotracker \
  -e TELEGRAM_BOT_TOKEN="your_token" \
  -e WEBHOOK_SECRET="your_secret" \
  -v ./data:/app/data \
  -p 8443:8443 \
  -p 8444:8444 \
  octotracker:latest
```

### Docker Compose

```bash
# Crea file .env con variabili necessarie
cp .env.example .env
# Edita .env con i tuoi valori

# Avvia servizi
docker compose up -d

# Verifica logs
docker compose logs -f

# Stop servizi
docker compose down
```

### Ottimizzazione Dimensione

**Il Dockerfile è ottimizzato per dimensioni minime:**

1. **Base image slim**: `python:3.11-slim` invece di `python:3.11`
2. **Multi-stage build**: Copia solo `uv` binary necessario
3. **Solo dipendenze runtime**: `uv pip install --system` installa solo le dipendenze necessarie (no pytest, ruff, etc.)
4. **Pulizia cache**: `rm -rf ~/.cache/uv` dopo install
5. **.dockerignore completo**: Esclude test, docs, cache, .venv

**⚠️ IMPORTANTE - Progetto Standalone:**
Il progetto è un'**applicazione bot standalone**, non una libreria Python da installare come package.
- Dockerfile usa `uv pip install --system` per installare solo dipendenze esterne
- NON usa `uv sync` (che installerebbe il progetto stesso come package)
- Il codice viene copiato in `/app` e eseguito direttamente con `python bot.py`
- Il `[build-system]` in `pyproject.toml` serve solo per compatibilità con tool di sviluppo

**File esclusi da .dockerignore:**
- `tests/` - Test non servono in produzione
- `.venv/` - Virtual env locale
- `.pytest_cache/`, `.ruff_cache/` - Cache tools dev
- `.coverage`, `htmlcov/` - Report coverage
- `CLAUDE.md`, `README.md` - Documentazione
- `.github/`, `.git/` - CI/CD e versioning

**Verifica sempre che nuovi file non aumentino dimensione inutilmente!**

### Troubleshooting Docker

```bash
# Entra nel container per debug
docker exec -it octotracker /bin/bash

# Verifica file copiati
docker exec -it octotracker ls -la /app

# Verifica dipendenze installate
docker exec -it octotracker uv pip list

# Rebuild forzato (senza cache)
docker build --no-cache -t octotracker:latest .
```

## 🚀 CI/CD

Il progetto usa GitHub Actions e SonarCloud per:
- Eseguire i test su ogni PR
- Verificare la code coverage (> 80%)
- Analizzare la code quality

**Non bypassare mai i check CI/CD!**

## 📚 Riferimenti

- [uv documentation](https://github.com/astral-sh/uv)
- [Ruff documentation](https://docs.astral.sh/ruff/)
- [pytest documentation](https://docs.pytest.org/)
- [python-telegram-bot documentation](https://docs.python-telegram-bot.org/)

---

**Ricorda**: Qualità del codice > Velocità di sviluppo. Prenditi il tempo per scrivere codice pulito e ben testato! 🎯
