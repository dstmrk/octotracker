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

### Black
Formattatore di codice (configurato per line-length 100):

```bash
# Formatta i file modificati
uv run black <file1.py> <file2.py>

# Formatta tutto il progetto
uv run black .

# Check senza modificare
uv run black --check .
```

### Ruff
Linter e fixer veloce (configurato in `pyproject.toml`):

```bash
# Check e auto-fix
uv run ruff check <file.py> --fix

# Check tutto il progetto
uv run ruff check .

# Solo check senza modifiche
uv run ruff check . --no-fix
```

**⚠️ IMPORTANTE**: Esegui sempre `black` e `ruff` prima di committare!

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
├── bot.py                    # Main bot + comandi utility + scheduler
├── registration.py           # Conversazione registrazione tariffe
├── feedback.py               # Sistema feedback utenti
├── checker.py                # Verifica tariffe e notifiche
├── database.py               # Gestione database SQLite
├── data_reader.py            # Scraper tariffe Octopus Energy
├── formatters.py             # Formattatori output
├── constants.py              # Costanti globali
├── health_handler.py         # Health check endpoint
├── tests/
│   ├── test_bot.py          # Test comandi bot
│   ├── test_registration.py # Test conversazione registrazione
│   ├── test_feedback.py     # Test sistema feedback
│   └── ...
└── pyproject.toml           # Configurazione progetto
```

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
uv run black <file.py>
uv run ruff check <file.py> --fix

# Esegui i test rilevanti
source .venv/bin/activate && pytest tests/test_<modulo>.py -v
```

### 3. Prima del commit

**CHECKLIST OBBLIGATORIA:**

- [ ] `uv run black .` → Nessun file modificato
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

### Moduli Conversazione (es. registration.py, feedback.py)

Quando crei un nuovo conversation handler:

1. **Separa in file dedicato** (come registration.py e feedback.py)
2. **Struttura standard**:
   - Import
   - Costanti messaggi
   - Stati conversazione (IntEnum)
   - Handler functions (async)
   - Helper functions (private con `_`)
   - Export esplicito delle costanti per backward compatibility

3. **Test completi**:
   - Test per ogni handler
   - Test validazione input
   - Test flussi completi
   - Test edge cases

### Import da Altri Moduli

```python
# bot.py importa da registration.py
from registration import (
    STATO_1,
    STATO_2,
    handler_1,
    handler_2,
)
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

## 🚀 CI/CD

Il progetto usa GitHub Actions e SonarCloud per:
- Eseguire i test su ogni PR
- Verificare la code coverage (> 80%)
- Analizzare la code quality

**Non bypassare mai i check CI/CD!**

## 📚 Riferimenti

- [uv documentation](https://github.com/astral-sh/uv)
- [Black documentation](https://black.readthedocs.io/)
- [Ruff documentation](https://docs.astral.sh/ruff/)
- [pytest documentation](https://docs.pytest.org/)
- [python-telegram-bot documentation](https://docs.python-telegram-bot.org/)

---

**Ricorda**: Qualità del codice > Velocità di sviluppo. Prenditi il tempo per scrivere codice pulito e ben testato! 🎯
