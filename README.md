# 🔌 OctoTracker

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram 24/7** per registrare e gestire le tue tariffe (luce e gas)
- **Scraping automatico** delle tariffe Octopus Energy (solo mono-orarie fisse)
- **Controllo giornaliero** e notifica se ci sono tariffe più convenienti
- **Keep-alive** configurabile per evitare sleep del worker
- **Zero costi**: hosting gratuito su Render
- **Zero manutenzione**: tutto automatico, un solo servizio

## 🚀 Setup

Puoi scegliere tra **due modalità** di hosting:
- **Opzione A**: Docker su Raspberry Pi / tua macchina (dati persistenti) ⭐ **Consigliato**
- **Opzione B**: Render cloud (gratuito, ma dati effimeri)

---

## 🐳 Opzione A: Docker (Raspberry Pi / Locale)

### Requisiti
- Raspberry Pi 3+ (consigliato RPi 4 con 2GB+ RAM)
- Docker e Docker Compose installati
- Connessione internet stabile

### Setup (2 minuti)

**1. Crea il Bot Telegram**

1. Apri Telegram e cerca `@BotFather`
2. Invia `/newbot` e segui le istruzioni
3. Copia il **token** (tipo: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`)

**2. Clona e Configura**

```bash
# Clona il repository
git clone https://github.com/dstmrk/octotracker.git
cd octotracker

# Crea file .env dalla template
cp .env.example .env

# Modifica .env e inserisci il tuo token
nano .env
# Imposta: TELEGRAM_BOT_TOKEN=il_tuo_token_qui
```

**3. Avvia con Docker**

```bash
# Build e avvio (prima volta: 5-10 min per scaricare dipendenze)
docker-compose up -d

# Verifica logs
docker-compose logs -f
```

Dovresti vedere:
```
🤖 Avvio OctoTracker...
⏰ Scraper schedulato: 9:00
⏰ Checker schedulato: 10:00
💓 Keep-alive: disabilitato
✅ Bot avviato e in ascolto!
```

**4. Usa il Bot!**

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
- `last_scrape.png` - screenshot debug

**Backup**: Copia semplicemente la cartella `data/`!

### Vantaggi Docker Locale
✅ **Dati persistenti** (non si perdono mai)
✅ **Nessun IP fisso necessario** (il bot usa polling, non webhook)
✅ **Controllo totale**
✅ **Zero costi** (oltre elettricità RPi)
✅ **Auto-restart** con `restart: unless-stopped`

---

## ☁️ Opzione B: Render Cloud

### 1. Crea il Bot Telegram

1. Apri Telegram e cerca `@BotFather`
2. Invia `/newbot` e segui le istruzioni
3. Copia il **token** che ti viene dato (tipo: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`)

### 2. Deploy su Render

1. Vai su [render.com](https://render.com) e crea un account gratuito
2. Collega il tuo account GitHub
3. Dalla [Dashboard Render](https://dashboard.render.com):
   - Clicca **"New +"** → **"Blueprint"**
   - Seleziona questo repository
   - Clicca **"Apply"**

4. Render creerà automaticamente **1 solo Worker** chiamato `octotracker` che include:
   - Bot Telegram (sempre attivo)
   - Scraper schedulato (ore 9:00)
   - Checker schedulato (ore 10:00)
   - Keep-alive (ogni 5 minuti)

### 3. Configura Token Telegram

Nel servizio `octotracker` creato, vai su **Environment** e aggiungi:

- **Nome**: `TELEGRAM_BOT_TOKEN`
- **Valore**: il token che hai ricevuto da BotFather

**Nota**: Le altre variabili (`SCRAPER_HOUR`, `CHECKER_HOUR`, `KEEPALIVE_INTERVAL_MINUTES`) hanno già valori di default nel `render.yaml`. Puoi modificarle se vuoi.

### 4. Aspetta il Deploy e Usa il Bot

1. Il primo deploy richiede 5-10 minuti (installa Playwright e browser Chromium)
2. Controlla i logs per verificare che tutto sia ok
3. Cerca il tuo bot su Telegram e invia `/start`

⚠️ **Nota Importante**: Su Render free tier, i dati (`users.json`) sono **effimeri** e si cancellano ad ogni deploy/restart. Per dati persistenti, usa Docker locale.

## 🤖 Comandi Bot

- `/start` - Registra le tue tariffe (prima volta)
- `/update` - Aggiorna le tue tariffe
- `/status` - Visualizza i tuoi dati salvati
- `/remove` - Cancella i tuoi dati
- `/help` - Mostra tutti i comandi
- `/cancel` - Annulla registrazione in corso

## ⚙️ Configurazione Avanzata

Puoi personalizzare il comportamento tramite variabili d'ambiente su Render:

| Variabile | Default | Descrizione |
|-----------|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | - | Token da BotFather (obbligatorio) |
| `SCRAPER_HOUR` | 9 | Ora dello scraping (0-23, ora italiana) |
| `CHECKER_HOUR` | 10 | Ora del controllo tariffe (0-23, ora italiana) |
| `KEEPALIVE_INTERVAL_MINUTES` | 5 | Intervallo keep-alive (minuti, 0 = disabilitato) |

**Esempio**: Per cambiare l'ora dello scraping alle 8:00 e disabilitare il keep-alive:
- `SCRAPER_HOUR` = `8`
- `KEEPALIVE_INTERVAL_MINUTES` = `0`

## ⚡️ Come Funziona

### Architettura

OctoTracker usa un **singolo Worker** con scheduler integrato:

```
Worker Render (sempre attivo)
├── Bot Telegram (gestisce comandi utente)
├── Scheduler interno (controlla l'ora ogni 30 secondi)
│   ├── Scraper (alle ore specificate)
│   └── Checker (alle ore specificate)
└── Keep-alive (ping periodico)
```

**Vantaggi**:
- ✅ Filesystem condiviso (i JSON sono accessibili a tutti i componenti)
- ✅ Nessuna sincronizzazione git necessaria
- ✅ Setup semplicissimo (solo `TELEGRAM_BOT_TOKEN`)
- ✅ Un solo servizio da monitorare

### Dati

I dati sono salvati localmente in file JSON:

**data/users.json** - Utenti e loro tariffe
```json
{
  "123456789": {
    "luce_energia": 0.12,
    "luce_comm": 96.00,
    "gas_energia": 0.45,
    "gas_comm": 144.00,
    "last_notified_rates": { ... }
  }
}
```

**data/current_rates.json** - Tariffe Octopus aggiornate
```json
{
  "luce": {
    "energia": 0.115,
    "commercializzazione": 96.00,
    "nome_tariffa": "Mono-oraria Fissa"
  },
  "gas": { ... },
  "data_aggiornamento": "2025-11-07"
}
```

**Nota**: Su Render free tier, il filesystem è effimero (i dati si perdono al restart). Questo va bene per un bot personale con pochi utenti. Se vuoi persistenza completa, considera di usare Render PostgreSQL (gratuito) o un database esterno.

## 🛠️ Sviluppo Locale (senza Docker)

Se vuoi sviluppare o testare senza Docker:

```bash
# Installa dipendenze
pip install -r requirements.txt
playwright install chromium

# Crea .env
cp .env.example .env
# Modifica .env con il tuo token

# Avvia bot (include scheduler)
python bot.py
```

### Test componenti singoli

```bash
# Test solo scraper
python scraper.py

# Test solo checker
python checker.py
```

## 🐳 Docker: Installazione su Raspberry Pi

Se non hai ancora Docker installato sul tuo RPi:

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
- `render.yaml` - Configurazione Blueprint Render (1 worker)
- `build.sh` - Script build per Playwright
- `requirements.txt` - Dipendenze Python

## 📝 Note

- **Tariffe supportate**: solo mono-orarie fisse
- **Fonte**: https://octopusenergy.it/le-nostre-tariffe
- **Automazione**: scraping ore 9:00, controllo ore 10:00 (configurabile)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Anti-spam**: ricevi notifica solo quando le tariffe Octopus cambiano
- **Privacy**: dati salvati localmente sul worker Render
- **Unità**: costi commercializzazione in €/anno
- **Costo**: 100% gratuito (Render free tier)

## 🔧 Troubleshooting

### Bot non risponde su Telegram
1. Verifica che il servizio `octotracker` sia "Live" su Render
2. Controlla i logs per errori
3. Verifica che `TELEGRAM_BOT_TOKEN` sia corretto

### Scraper non funziona
1. Controlla i logs alle ore dello scraping
2. Il primo build richiede tempo (installa Playwright)
3. Verifica screenshot in `data/last_scrape.png` per debug

### Worker va in sleep
1. Aumenta `KEEPALIVE_INTERVAL_MINUTES` (es: da 5 a 3)
2. Controlla nei logs i ping keep-alive
3. I Worker su Render free *non dovrebbero* andare in sleep (solo i Web Services)

### Dati persi dopo restart
- Render free tier ha filesystem effimero
- Normale per restart/redeploy
- Gli utenti devono registrarsi di nuovo
- Soluzione: usare database PostgreSQL (gratuito su Render)

## 🔮 Possibili Miglioramenti Futuri

- [ ] PostgreSQL per persistenza dati
- [ ] Supporto tariffe bi-orarie e variabili
- [ ] Stima risparmio annuale basata su consumi
- [ ] Storico tariffe con grafici
- [ ] Notifiche personalizzate per orario
- [ ] Dashboard web per visualizzare statistiche

## 📄 Licenza

MIT

---

⚠️ **Disclaimer**: OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy. È un progetto indipendente di monitoraggio tariffe.
