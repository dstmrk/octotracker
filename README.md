# 🔌 OctoTracker

<img height="256" width="256" src="https://github.com/user-attachments/assets/95261ce6-a06e-4b3e-b980-009069568753" alt="octotracker"/>

[![Unit Tests](https://github.com/dstmrk/octotracker/actions/workflows/unit-test.yml/badge.svg)](https://github.com/dstmrk/octotracker/actions/workflows/unit-test.yml)
[![Docker Build](https://github.com/dstmrk/octotracker/actions/workflows/docker.yml/badge.svg)](https://github.com/dstmrk/octotracker/actions/workflows/docker.yml)

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram 24/7** per registrare e gestire le tue tariffe (luce e gas)
- **Scraping automatico** delle tariffe Octopus Energy (fisse e variabili, mono/triorarie)
- **Supporto tariffe variabili** indicizzate a PUN (luce) e PSV (gas) + spread
- **Notifiche intelligenti** con 3 modalità:
  - ✅ **Tutto migliorato**: conferma che la nuova tariffa conviene
  - ⚖️ **Mix migliorato/peggiorato**: avviso quando una componente migliora e l'altra peggiora
  - 🎯 **Evidenziazione visiva**: grassetto per valori migliorati, sottolineato per peggiorati
- **Deduplica notifiche**: non ti invia lo stesso messaggio più volte
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
docker-compose up -d

# Verifica logs
docker-compose logs -f
```

Dovresti vedere:
```
🤖 Avvio OctoTracker...
📡 Modalità: WEBHOOK
⏰ Scraper schedulato: 9:00
⏰ Checker schedulato: 10:00
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
docker-compose down

# Restart
docker-compose restart

# Logs in tempo reale
docker-compose logs -f

# Rebuild dopo aggiornamenti codice
docker-compose up -d --build
```

### Dati Persistenti

I dati sono salvati in `./data/`:
- `users.json` - utenti registrati e tariffe
- `current_rates.json` - tariffe Octopus aggiornate

**Backup**: Copia semplicemente la cartella `data/`!

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
Quando una componente migliora ma l'altra peggiora (caso ambiguo):

```
⚖️ Aggiornamento tariffe Octopus Energy
...una delle due componenti è migliorata, l'altra è aumentata.

💡 Luce:
Tua tariffa: 0.145 €/kWh, 60 €/anno
Nuova tariffa: 0.138 €/kWh, 84 €/anno
              (grassetto)  (sottolineato)

📊 In questi casi la convenienza dipende dai tuoi consumi.
Ti consiglio di fare una verifica in base ai kWh che usi...
```

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
| `WEBHOOK_SECRET` | - | Token segreto per validazione webhook (opzionale) |
| `SCRAPER_HOUR` | `9` | Ora dello scraping (0-23, ora italiana) |
| `CHECKER_HOUR` | `10` | Ora del controllo tariffe (0-23, ora italiana) |

**Esempio**: Per cambiare l'ora dello scraping alle 8:00:
```bash
SCRAPER_HOUR=8
```

## ⚡️ Come Funziona

### Architettura

OctoTracker usa un **singolo container Docker** con bot e scheduler integrati:

```
Container Docker (sempre attivo)
├── Bot Telegram (gestisce comandi utente 24/7)
├── Scraper Task (indipendente, dorme 24 ore tra esecuzioni)
├── Checker Task (indipendente, dorme 24 ore tra esecuzioni)
└── Error Handler (gestisce timeout di rete senza crashare)
```

**Scheduler ottimizzato**:
- **Sleep-based scheduling**: ogni task calcola esattamente quanto dormire fino alla prossima esecuzione
- **Task indipendenti**: scraper e checker girano separatamente senza interferire
- **Efficienza massima**: 2 esecuzioni/giorno invece di controlli continui
- **Timeout aumentati**: 30 secondi per operazioni HTTP (ottimizzato per connessioni lente)

### Dati

I dati sono salvati localmente in file JSON:

**data/users.json** - Utenti e loro tariffe (struttura nested)
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

**data/current_rates.json** - Tariffe Octopus aggiornate (struttura nested)
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

**Nota sulla struttura**:
- **users.json**: `tipo` indica "fissa" o "variabile", `fascia` indica "monoraria" o "trioraria"
- **current_rates.json**: Struttura a 3 livelli (luce/gas → fissa/variabile → monoraria/trioraria)
- Per tariffe variabili, `energia` rappresenta lo spread (es: PUN + 0.0088)

**Persistenza**: I dati sono salvati nel volume Docker (`./data` nella cartella del progetto) e sono persistenti tra restart del container.

## 🛠️ Sviluppo Locale (senza Docker)

Se vuoi sviluppare o testare senza Docker:

```bash
# Installa uv (se non già installato)
curl -LsSf https://astral.sh/uv/install.sh | sh

# Installa dipendenze
uv sync
uv run playwright install chromium

# Crea .env
cp .env.example .env
# Modifica .env con il tuo token

# Avvia bot (include scheduler)
uv run python bot.py
```

### Test componenti singoli

```bash
# Test solo scraper
uv run python scraper.py

# Test solo checker
uv run python checker.py
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
- `scraper.py` - Playwright scraper per tariffe Octopus
- `checker.py` - Controllo e invio notifiche
- `docker-compose.yml` - Orchestrazione Docker
- `Dockerfile` - Build immagine Docker
- `pyproject.toml` - Dipendenze Python (gestite con uv)
- `.env.example` - Template configurazione

## 📝 Note

- **Tariffe supportate**:
  - Luce: Fissa monoraria, Variabile monoraria (PUN + spread), Variabile trioraria (PUN + spread F1/F2/F3)
  - Gas: Fissa monoraria, Variabile monoraria (PSV + spread)
- **Fonte**: https://octopusenergy.it/le-nostre-tariffe
- **Automazione**: scraping ore 9:00, controllo ore 10:00 (configurabile)
- **Confronti**: Solo tariffe dello stesso tipo e fascia (nessun cross-type)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Privacy**: dati salvati localmente
- **Unità**: costi commercializzazione in €/anno
- **Timeout**: 30 secondi per operazioni HTTP
- **Resilienza**: error handler gestisce timeout di rete

## 🔧 Troubleshooting

### Bot non risponde su Telegram
```bash
# Controlla che il container sia attivo
docker ps

# Controlla i logs per errori
docker compose logs -f

# Verifica che TELEGRAM_BOT_TOKEN sia corretto nel .env
cat .env
```

### Timeout di rete / Bot crasha
Il bot ha timeout aumentati (30s) e error handler - non dovrebbe crashare.
```bash
# Controlla i logs per vedere errori di rete
docker compose logs -f | grep "Errore"

# Se vedi molti timeout, verifica la connessione internet
ping 8.8.8.8 -c 5
```

### Scraper non funziona
```bash
# Controlla i logs alle ore dello scraping (default: 9:00)
docker compose logs -f

# Test manuale dello scraper
docker compose exec octotracker python scraper.py

# Verifica file tariffe generato
cat data/current_rates.json
```

### Container non parte
```bash
# Verifica che non ci siano problemi di memoria
free -h

# Rebuild completo del container
docker compose down
docker compose up -d --build

# Logs dettagliati durante startup
docker compose logs -f
```

### Warning memoria sul kernel
Se vedi "Your kernel does not support memory soft limit capabilities", è normale su alcuni kernel. Il bot funziona comunque - il limite hard di 1G è attivo.

### Dati persi dopo restart
I dati dovrebbero essere persistenti in `./data/`. Se si perdono:
```bash
# Verifica che il volume sia montato correttamente
docker compose down
ls -la data/
docker compose up -d
```

## 🔮 Possibili Miglioramenti Futuri

- [ ] **Calcolo automatico convenienza** nei casi "dubbi": chiedi i consumi all'utente (kWh/anno, Smc/anno) e calcola se il cambio conviene realmente

## 📄 Licenza

MIT

---

⚠️ **Disclaimer**: OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy. È un progetto indipendente di monitoraggio tariffe.
