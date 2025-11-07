# 🔌 OctoTracker

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram 24/7** per registrare e gestire le tue tariffe (luce e gas)
- **Scraping automatico** delle tariffe Octopus Energy (solo mono-orarie fisse)
- **Notifiche intelligenti** con 3 modalità:
  - ✅ **Tutto migliorato**: conferma che la nuova tariffa conviene
  - ⚖️ **Mix migliorato/peggiorato**: avviso quando una componente migliora e l'altra peggiora
  - 🎯 **Evidenziazione visiva**: grassetto per valori migliorati, sottolineato per peggiorati
- **Deduplica notifiche**: non ti invia lo stesso messaggio più volte
- **Supporto webhook o polling**: scegli tra latenza zero (webhook) o semplicità (polling)
- **Persistenza dati** tramite Docker volumes
- **Scheduler ottimizzato**: zero polling, task indipendenti che dormono 24 ore tra esecuzioni
- **Resiliente**: error handler per gestire timeout di rete senza crashare

## 🚀 Setup con Docker

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

### Vantaggi
✅ **Dati persistenti** (non si perdono mai)
✅ **Nessun IP fisso necessario** (polling di default)
✅ **Controllo totale** sul tuo server
✅ **Zero costi** (oltre elettricità RPi)
✅ **Auto-restart** con `restart: unless-stopped`
✅ **Ottimizzato per Raspberry Pi** con timeout aumentati per connessioni lente

### 🌐 Opzionale: Modalità Webhook (con Cloudflare Tunnel)

Se hai già un tunnel Cloudflare configurato, puoi usare **webhook** invece di polling per **latenza zero**.

**Vantaggi webhook**:
- ⚡ Latenza istantanea (vs ~1 secondo polling)
- 🔋 Meno CPU/RAM (idle quando non ci sono messaggi)
- 📉 Meno traffico di rete

**Setup webhook**:

**1. Configura Cloudflare Tunnel**

Nel tuo tunnel Cloudflare, aggiungi un "Public Hostname":
- **Subdomain**: `octotracker`
- **Domain**: `tuodominio.xyz`
- **Service**: `http://localhost:8443`

Questo renderà il bot raggiungibile su `https://octotracker.tuodominio.xyz`

**2. Genera Secret Token (opzionale ma consigliato)**

```bash
# Genera token random per sicurezza
openssl rand -hex 32
```

**3. Modifica `.env`**

```bash
# Cambia da polling a webhook
BOT_MODE=webhook

# Imposta URL pubblico (quello configurato su Cloudflare)
WEBHOOK_URL=https://octotracker.tuodominio.xyz

# Porta locale (default: 8443)
WEBHOOK_PORT=8443

# Secret token (quello generato sopra)
WEBHOOK_SECRET=il_tuo_token_random_qui
```

**4. Riavvia container**

```bash
docker-compose down
docker-compose up -d
docker-compose logs -f
```

Dovresti vedere:
```
📡 Modalità: WEBHOOK
🌐 Webhook URL: https://octotracker.tuodominio.xyz
🔌 Porta: 8443
💓 Keep-alive: non necessario (webhook)
✅ Bot configurato!
🚀 Avvio webhook su https://octotracker.tuodominio.xyz...
```

**5. Test**

Invia un messaggio al bot su Telegram: la risposta sarà **istantanea**! ⚡

**Nota sicurezza**:
- Il bot usa il token Telegram come path: `https://octotracker.tuodominio.xyz/{token}`
- Solo Telegram conosce questo URL
- Il `WEBHOOK_SECRET` valida che le richieste vengano da Telegram
- Non serve autenticazione aggiuntiva

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
| `BOT_MODE` | `polling` | Modalità bot: `polling` o `webhook` |
| `WEBHOOK_URL` | - | URL pubblico per webhook (richiesto se `BOT_MODE=webhook`) |
| `WEBHOOK_PORT` | `8443` | Porta locale per webhook |
| `WEBHOOK_SECRET` | - | Token segreto per validazione webhook (opzionale) |
| `SCRAPER_HOUR` | `9` | Ora dello scraping (0-23, ora italiana) |
| `CHECKER_HOUR` | `10` | Ora del controllo tariffe (0-23, ora italiana) |
| `KEEPALIVE_INTERVAL_MINUTES` | `0` | Intervallo keep-alive (minuti, 0 = disabilitato) |

**Esempio**: Per cambiare l'ora dello scraping alle 8:00 e abilitare keep-alive ogni 5 minuti:
```bash
SCRAPER_HOUR=8
KEEPALIVE_INTERVAL_MINUTES=5
```

**Nota**: Keep-alive è utile solo in modalità `polling` ed è disabilitato di default.

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
- **Zero polling**: ogni task calcola esattamente quanto dormire fino alla prossima esecuzione
- **Task indipendenti**: scraper e checker girano separatamente senza interferire
- **Efficienza massima**: 2 esecuzioni/giorno invece di 2880 controlli/giorno
- **Timeout aumentati**: 30 secondi per tutte le operazioni HTTP (ottimizzato per Raspberry Pi)

**Vantaggi**:
- ✅ Filesystem condiviso (i JSON sono accessibili a tutti i componenti)
- ✅ Setup semplicissimo (solo `TELEGRAM_BOT_TOKEN` richiesto)
- ✅ Un solo container da monitorare
- ✅ Resiliente a problemi di rete temporanei

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

**Persistenza**: I dati sono salvati nel volume Docker (`./data` nella cartella del progetto) e sono persistenti tra restart del container.

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

- `bot.py` - Bot Telegram con scheduler integrato e error handler
- `scraper.py` - Playwright scraper per tariffe Octopus
- `checker.py` - Controllo e invio notifiche con formattazione intelligente
- `docker-compose.yml` - Orchestrazione Docker
- `Dockerfile` - Build immagine ottimizzata per RPi
- `requirements.txt` - Dipendenze Python
- `.env.example` - Template configurazione

## 📝 Note

- **Tariffe supportate**: solo mono-orarie fisse
- **Fonte**: https://octopusenergy.it/le-nostre-tariffe
- **Automazione**: scraping ore 9:00, controllo ore 10:00 (configurabile)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Anti-spam**: ricevi notifica solo quando le tariffe Octopus cambiano
- **Privacy**: dati salvati localmente sul tuo Raspberry Pi/server
- **Unità**: costi commercializzazione in €/anno
- **Costo**: 100% gratuito (serve solo elettricità per RPi)
- **Timeout**: 30 secondi per operazioni HTTP (ottimizzato per connessioni lente)
- **Resilienza**: error handler gestisce timeout di rete senza crashare

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

# Verifica screenshot di debug
ls -lh data/last_scrape.png

# Test manuale dello scraper
docker compose exec octotracker python scraper.py
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

Vedi `OPTIMIZATIONS.md` per dettagli su ottimizzazioni tecniche del codice.

### Alta priorità
- [ ] **Calcolo automatico convenienza** nei casi "dubbi": chiedi i consumi all'utente (kWh/anno, Smc/anno) e calcola se il cambio conviene realmente

### Media priorità
- [ ] Supporto tariffe bi-orarie (F1/F23)
- [ ] Supporto tariffe variabili (indicizzate)
- [ ] Stima risparmio annuale con grafici
- [ ] Database esterno opzionale (PostgreSQL/SQLite) per scalabilità

### Bassa priorità
- [ ] Storico tariffe con trend
- [ ] Notifiche personalizzate per orario preferito
- [ ] Dashboard web per visualizzare statistiche
- [ ] Export dati in CSV/Excel

## 📄 Licenza

MIT

---

⚠️ **Disclaimer**: OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy. È un progetto indipendente di monitoraggio tariffe.
