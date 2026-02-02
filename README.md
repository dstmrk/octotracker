# 🔌 OctoTracker

<img height="256" width="256" src="https://github.com/user-attachments/assets/95261ce6-a06e-4b3e-b980-009069568753" alt="octotracker"/>

[![MIT License](https://img.shields.io/badge/License-MIT-green.svg)](https://choosealicense.com/licenses/mit/)
[![Unit Tests](https://github.com/dstmrk/octotracker/actions/workflows/unit-test.yml/badge.svg)](https://github.com/dstmrk/octotracker/actions/workflows/unit-test.yml)
[![Lint](https://github.com/dstmrk/octotracker/actions/workflows/lint.yml/badge.svg)](https://github.com/dstmrk/octotracker/actions/workflows/lint.yml)
[![Docker Build](https://github.com/dstmrk/octotracker/actions/workflows/docker.yml/badge.svg)](https://github.com/dstmrk/octotracker/actions/workflows/docker.yml)

[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Security Rating](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=security_rating)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Vulnerabilities](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=vulnerabilities)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Bugs](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=bugs)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Code Smells](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=code_smells)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Duplicated Lines (%)](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=duplicated_lines_density)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Maintainability Rating](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=sqale_rating)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?project=dstmrk_octotracker&metric=coverage)](https://sonarcloud.io/summary/new_code?id=dstmrk_octotracker)

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram 24/7** per registrare e gestire le tue tariffe (luce e gas)
- **Lettura open-data ARERA** delle tariffe Octopus Energy da [Il Portale Offerte](https://www.ilportaleofferte.it/portaleOfferte/it/open-data.page) (fisse e variabili, mono/triorarie)
- **Supporto tariffe variabili** indicizzate a PUN (luce) e PSV (gas) + spread
- **Notifiche intelligenti** con 3 modalità:
  - ✅ **Tutto migliorato**: conferma che la nuova tariffa conviene
  - ⚖️ **Mix migliorato/peggiorato**: avviso quando una componente migliora e l'altra peggiora
  - 💰 **Calcolo risparmio stimato**: valutazione separata luce/gas basata sui tuoi consumi reali
  - 🎯 **Evidenziazione visiva**: grassetto per valori migliorati, sottolineato per peggiorati
- **Deduplica notifiche**: non ti invia lo stesso messaggio più volte
- **Consumi opzionali**: inserisci i tuoi kWh/anno e Smc/anno per calcoli precisi nei casi ambigui
- **Webhook mode**: risposte istantanee tramite Cloudflare Tunnel
- **Persistenza dati** tramite Docker volumes
- **Scheduler ottimizzato**: task indipendenti che dormono 24 ore tra esecuzioni
- **Resiliente**: error handler per gestire timeout di rete senza crashare

## 🚀 Setup con Docker

### Requisiti
- Docker e Docker Compose
- URL pubblico HTTPS per webhook (es. tramite tunnel o reverse proxy)

### Setup

**1. Crea il Bot Telegram**

1. Apri Telegram e cerca `@BotFather`
2. Invia `/newbot` e segui le istruzioni
3. Copia il **token** (tipo: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`)

**2. Configura Webhook Pubblico**

Configura un URL pubblico HTTPS che inoltri al bot sulla porta 8443:
- Servizio locale: `http://localhost:8443`
- URL pubblico: `https://tuodominio.xyz`

**3. Clona e Configura**

```bash
git clone https://github.com/dstmrk/octotracker.git
cd octotracker
cp .env.example .env
nano .env
```

Configura le variabili in `.env`:
```bash
TELEGRAM_BOT_TOKEN=il_tuo_token_qui
WEBHOOK_URL=https://tuodominio.xyz
WEBHOOK_PORT=8443
WEBHOOK_SECRET=  # Opzionale: openssl rand -hex 32
```

**4. Avvia con Docker**

```bash
# Build e avvio (prima volta: 5-10 min per scaricare dipendenze)
docker compose up -d

# Verifica logs
docker compose logs -f
```

Dovresti vedere:
```
🤖 Avvio OctoTracker...
📡 Modalità: WEBHOOK
⏰ Aggiornamento tariffe schedulato: 11:00
⏰ Checker schedulato: 12:00
🌐 Webhook URL: https://octotracker.tuodominio.xyz
🔌 Porta: 8443
✅ Bot configurato!
🚀 Avvio webhook su https://octotracker.tuodominio.xyz...
```

**5. Usa il Bot!**

Cerca il tuo bot su Telegram e invia `/start` 🎉

### Gestione

```bash
# Stop
docker compose down

# Restart
docker compose restart

# Logs in tempo reale
docker compose logs -f

# Rebuild dopo aggiornamenti codice
docker compose up -d --build
```

### Dati Persistenti

I dati sono salvati in `./data/`:
- `octotracker.db` - database SQLite con utenti registrati, tariffe e storico

**Backup**: Copia semplicemente la cartella `data/`!

---

## 🚢 Deployment Produzione

### Immagine Docker Pre-built

Ad ogni release, viene automaticamente pubblicata un'immagine Docker su GitHub Container Registry (GHCR).

**Setup iniziale sul server:**
```bash
# Scarica docker-compose.yml e .env.example dal repository
curl -O https://raw.githubusercontent.com/dstmrk/octotracker/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/dstmrk/octotracker/main/.env.example

# Configura .env
cp .env.example .env
nano .env  # Configura TELEGRAM_BOT_TOKEN, WEBHOOK_URL, WEBHOOK_SECRET

# Avvia usando immagine da GHCR
docker compose pull
docker compose up -d
```

**Aggiornamento alla nuova release:**
```bash
docker compose pull      # Scarica nuova immagine
docker compose up -d     # Riavvia con nuova versione
```

Il file `.env` e i dati in `./data/` vengono preservati durante l'aggiornamento!

**Sviluppo locale:**
Per sviluppo locale, modifica `docker-compose.yml` commentando `image:` e decommentando `build: .`

---

## 🤖 Comandi Bot

- `/start` - Registra le tue tariffe (prima volta)
- `/update` - Aggiorna le tue tariffe
- `/status` - Visualizza i tuoi dati salvati
- `/remove` - Cancella i tuoi dati
- `/help` - Mostra tutti i comandi
- `/cancel` - Annulla registrazione in corso

## 📬 Sistema di Notifiche Intelligenti

OctoTracker analizza le tariffe Octopus e ti notifica in modo intelligente:

### ✅ Caso 1: Tutto Migliorato
Quando **entrambe** le componenti (energia + commercializzazione) migliorano:

```
⚡️ Buone notizie!
OctoTracker ha trovato una tariffa Octopus Energy più conveniente...

💡 Luce:
Tua tariffa: 0.145 €/kWh, 72 €/anno
Nuova tariffa: 0.138 €/kWh, 60 €/anno
                ^^^^^^^^^^^^  ^^^^^^^^^^
              (grassetto)    (grassetto)
```

### ⚖️ Caso 2: Mix Migliorato/Peggiorato
Quando una componente migliora ma l'altra peggiora (caso ambiguo), OctoTracker usa i tuoi consumi per valutare **separatamente** luce e gas:

**Con consumi inseriti** (via `/update`):
```
⚖️ Aggiornamento tariffe Octopus Energy
...una delle due componenti è migliorata, l'altra è aumentata.

💡 Luce:
Tua tariffa: 0.145 €/kWh, 60 €/anno
Nuova tariffa: 0.138 €/kWh, 84 €/anno
              (grassetto)  (sottolineato)

💰 In base ai tuoi consumi di luce, stimiamo un risparmio di circa 47,50 €/anno.
```

**Senza consumi**:
```
📊 In questi casi la convenienza dipende dai tuoi consumi.
Se vuoi una stima più precisa, puoi indicare i tuoi consumi usando il comando /update.
```

**Logica di valutazione per-utility** (luce e gas indipendenti):
- ✅ **Non-MIXED con risparmio** → Notifica sempre
- ⚖️ **MIXED senza consumi** → Notifica con suggerimento di inserire consumi
- 📊 **MIXED con consumi e risparmio > 0** → Notifica con stima risparmio
- ❌ **MIXED con consumi e risparmio ≤ 0** → NON notifica quella utility

**Esempi**:
- Luce MIXED con +30€, Gas MIXED con -20€ → Mostra solo luce
- Luce non-MIXED (conveniente), Gas MIXED con -15€ → Mostra solo luce
- Luce MIXED con -10€, Gas MIXED con -5€ → Nessuna notifica

**Legenda**:
- **Grassetto** = valore migliorato 📉
- <u>Sottolineato</u> = valore peggiorato 📈
- Normale = nessun cambiamento

### 🚫 Niente Spam
- Non ricevi notifiche se le tariffe Octopus non cambiano
- Non ricevi notifiche duplicate per le stesse tariffe
- Ricevi notifiche solo quando c'è almeno un miglioramento

## ⚙️ Configurazione Avanzata

Puoi personalizzare il comportamento tramite variabili d'ambiente nel file `.env`:

| Variabile | Default | Descrizione |
|-----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | - | Token da BotFather (obbligatorio) |
| `WEBHOOK_URL` | - | URL pubblico per webhook (obbligatorio) |
| `WEBHOOK_PORT` | `8443` | Porta locale per webhook |
| `WEBHOOK_SECRET` | - | Token segreto per validazione webhook (obbligatorio) |
| `HEALTH_PORT` | `8444` | Porta per health check endpoint |
| `UPDATER_HOUR` | `11` | Ora aggiornamento tariffe da open-data ARERA (0-23, ora italiana) |
| `CHECKER_HOUR` | `12` | Ora del controllo tariffe (0-23, ora italiana) |

**Esempio**: Per cambiare l'ora di aggiornamento tariffe alle 8:00:
```bash
UPDATER_HOUR=8
```

## 🏥 Health Check Endpoint

OctoTracker espone un endpoint `/health` per monitoring e alerting:

```bash
# Accesso locale
curl http://localhost:8444/health

# Accesso remoto (via tunnel)
curl https://tuodominio.xyz:8444/health
```

**Response JSON:**
```json
{
  "status": "healthy",
  "timestamp": "2025-11-11T10:30:00",
  "checks": {
    "database": {
      "status": "ok",
      "users_count": 42,
      "accessible": true
    },
    "tariffe": {
      "status": "ok",
      "last_update": "2025-11-10",
      "days_old": 1
    },
    "bot": {
      "status": "ok",
      "scheduled_tasks": {
        "updater": "running",
        "checker": "running"
      }
    }
  }
}
```

**Stati:**
- `healthy` (HTTP 200): Tutto funzionante
- `degraded` (HTTP 200): Warning non critici (es: tariffe vecchie >3 giorni)
- `unhealthy` (HTTP 503): Errore critico (es: database inaccessibile)

**Uso con monitoring tools:**
- **UptimeRobot**: URL = `https://tuodominio.xyz:8444/health`, Keyword = `"healthy"`
- **Kubernetes**: `livenessProbe` su `http://localhost:8444/health`
- **Docker**: `HEALTHCHECK CMD curl -f http://localhost:8444/health`

## ⚡️ Come Funziona

### Architettura

OctoTracker usa un **singolo container Docker** con bot e scheduler integrati:

```
Container Docker (sempre attivo)
├── Bot Telegram (webhook su porta 8443, gestisce comandi utente 24/7)
├── Health Server (HTTP su porta 8444, endpoint /health per monitoring)
├── Tariffe Updater Task (legge open-data ARERA, dorme 24 ore tra esecuzioni)
├── Checker Task (indipendente, dorme 24 ore tra esecuzioni)
└── Error Handler (gestisce timeout di rete senza crashare)
```

**Scheduler ottimizzato**:
- **Sleep-based scheduling**: ogni task calcola esattamente quanto dormire fino alla prossima esecuzione
- **Task indipendenti**: updater e checker girano separatamente senza interferire
- **Efficienza massima**: 2 esecuzioni/giorno invece di controlli continui
- **Timeout aumentati**: 30 secondi per operazioni HTTP (ottimizzato per connessioni lente)

### Dati

I dati sono salvati localmente:

**data/octotracker.db** - Database SQLite con utenti e loro tariffe
- Tabella `users` con campi flat per luce e gas
- Campo `last_notified_rates` in formato JSON per tracking notifiche
- Supporta transazioni ACID per evitare race conditions con 1000+ utenti
- Schema:
  ```sql
  CREATE TABLE users (
      user_id TEXT PRIMARY KEY,
      luce_tipo TEXT NOT NULL,
      luce_fascia TEXT NOT NULL,
      luce_energia REAL NOT NULL,
      luce_commercializzazione REAL NOT NULL,
      luce_consumo_f1 REAL,           -- kWh/anno (F1 per trioraria, totale per mono/bioraria)
      luce_consumo_f2 REAL,           -- kWh/anno (solo trioraria)
      luce_consumo_f3 REAL,           -- kWh/anno (solo trioraria)
      gas_tipo TEXT,
      gas_fascia TEXT,
      gas_energia REAL,
      gas_commercializzazione REAL,
      gas_consumo_annuo REAL,         -- Smc/anno
      last_notified_rates TEXT,       -- JSON
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
      updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
  );
  ```

**Tabella rate_history** - Storico tariffe Octopus (una riga per combinazione servizio/tipo/fascia/data)
```sql
CREATE TABLE rate_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    data_fonte TEXT NOT NULL,           -- Data del file XML ARERA
    servizio TEXT NOT NULL,             -- "luce" o "gas"
    tipo TEXT NOT NULL,                 -- "fissa" o "variabile"
    fascia TEXT NOT NULL,               -- "monoraria" o "trioraria"
    energia REAL NOT NULL,              -- €/kWh, €/Smc o spread
    commercializzazione REAL,           -- €/anno
    cod_offerta TEXT,
    UNIQUE(data_fonte, servizio, tipo, fascia)
);
```

**Nota sulla struttura**:
- **octotracker.db**: Database SQLite con supporto transazioni ACID per scalare a 1000+ utenti
- **rate_history**: Le tariffe correnti sono l'ultimo record per ogni combinazione servizio/tipo/fascia
- Per tariffe variabili, `energia` rappresenta lo spread (es: PUN + 0.0088)

**Persistenza**: I dati sono salvati nel volume Docker (`./data` nella cartella del progetto) e sono persistenti tra restart del container.

**Scalabilità**: SQLite gestisce automaticamente race conditions con locking, permettendo di scalare fino a 1000+ utenti senza perdita dati.

## 🛠️ Sviluppo Locale (senza Docker)

Se vuoi sviluppare o testare senza Docker:

```bash
# Installa uv (se non già installato)
curl -LsSf https://astral.sh/uv/install.sh | sh

# Installa dipendenze
uv sync --extra dev

# Crea .env
cp .env.example .env
# Modifica .env con il tuo token

# Avvia bot (include scheduler)
uv run python bot.py
```

### Code Quality Tools

Il progetto usa **Black** (formatter) e **Ruff** (linter) per mantenere alta la qualità del codice.

#### Setup Pre-commit Hooks (Consigliato)

I pre-commit hooks formattano automaticamente il codice prima di ogni commit:

```bash
# Installa pre-commit hooks
uv run pre-commit install

# Ora ad ogni git commit:
# 1. Black formatta il codice automaticamente
# 2. Ruff controlla e auto-fixa problemi risolvibili
# 3. Commit procede solo se tutto è ok
```

#### Comandi Manuali

Se preferisci eseguire i check manualmente:

```bash
# Formatta tutto il codice con Black
uv run black .

# Controlla e auto-fixa problemi con Ruff
uv run ruff check --fix .

# Solo controllo (senza modifiche)
uv run black --check .
uv run ruff check .
```

#### GitHub Actions

I check di linting e testing girano automaticamente ad ogni push:
- ✅ **Unit Tests** - pytest su tutti i test
- ✅ **Lint** - black + ruff verificano la qualità del codice
- ✅ **Docker Build** - build dell'immagine (solo su PR)

### Test componenti singoli

```bash
# Test solo updater (lettura open-data ARERA)
uv run python updater.py

# Test solo checker
uv run python checker.py

# Run unit tests
uv run pytest tests/ -v
```

## 🐳 Installazione Docker

Se non hai ancora Docker installato:

```bash
# Installa Docker
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# Aggiungi user a gruppo docker
sudo usermod -aG docker $USER

# Logout e login per applicare

# Installa Docker Compose
sudo apt-get install docker-compose-plugin

# Verifica installazione
docker --version
docker compose version
```

### File Principali

- `bot.py` - Bot Telegram con scheduler integrato
- `updater.py` - Lettura open-data ARERA per tariffe Octopus
- `checker.py` - Controllo e invio notifiche
- `docker-compose.yml` - Orchestrazione Docker
- `Dockerfile` - Build immagine Docker
- `pyproject.toml` - Dipendenze Python (gestite con uv)
- `.env.example` - Template configurazione

## 📝 Note

- **Tariffe supportate**:
  - Luce: Fissa monoraria, Variabile monoraria (PUN + spread), Variabile trioraria (PUN + spread F1/F2/F3)
  - Gas: Fissa monoraria, Variabile monoraria (PSV + spread)
- **Fonte**: [Open-data ARERA - Il Portale Offerte](https://www.ilportaleofferte.it/portaleOfferte/it/open-data.page)
- **Automazione**: aggiornamento tariffe ore 11:00, controllo ore 12:00 (configurabile)
- **Confronti**: Solo tariffe dello stesso tipo e fascia (nessun cross-type)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Privacy**: dati salvati localmente
- **Unità**: costi commercializzazione in €/anno
- **Timeout**: 30 secondi per operazioni HTTP
- **Resilienza**: error handler gestisce timeout di rete

## 🔮 Possibili Miglioramenti Futuri

- [x] **Calcolo automatico convenienza** nei casi "dubbi": ✅ Implementato! Il bot chiede i consumi (kWh/anno, Smc/anno) e calcola il risparmio stimato per luce e gas separatamente
- [ ] **Aggiornamento tariffe con un tap**: quando arriva una notifica con tariffe più convenienti, un pulsante inline permette di aggiornare direttamente le proprie tariffe registrate senza dover reinserire tutto manualmente
- [ ] **Storico tariffe**: possibilità di consultare lo storico delle tariffe Octopus Energy degli ultimi 365 giorni, per capire l'andamento nel tempo

## 📜 Licenza

Questo progetto usa la Licenza MIT – guarda il file [LICENSE](LICENSE) per dettagli.

---

⚠️ **Disclaimer**: OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy. È un progetto indipendente di monitoraggio tariffe.
