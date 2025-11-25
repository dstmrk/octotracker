# 🚀 Deployment OctoTracker con Docker

Questa guida spiega come deployare OctoTracker sul tuo server usando le immagini Docker pre-built pubblicate su GitHub Container Registry.

## 📦 Prerequisiti

- Docker e Docker Compose installati sul server
- Porta 8443 aperta per il webhook Telegram
- (Opzionale) Reverse proxy (nginx, Caddy) per HTTPS

## 🔧 Setup Iniziale

### 1. Clona il repository (o scarica solo i file necessari)

```bash
# Clone completo
git clone https://github.com/dstmrk/octotracker.git
cd octotracker

# OPPURE scarica solo i file necessari:
# - docker-compose.yml
# - .env.example
```

### 2. Configura le variabili d'ambiente

```bash
# Copia il template
cp .env.example .env

# Edita con i tuoi valori
nano .env  # o vim, o il tuo editor preferito
```

**Variabili OBBLIGATORIE:**
- `TELEGRAM_BOT_TOKEN`: Token del bot (da @BotFather)
- `WEBHOOK_URL`: URL pubblico del tuo server (es. `https://octotracker.tuodominio.xyz`)
- `WEBHOOK_SECRET`: Token segreto per sicurezza (genera con: `python -c 'import secrets; print(secrets.token_urlsafe(32))'`)

**Variabili OPZIONALI:**
- `ADMIN_USER_ID`: Tuo ID Telegram per alert errori
- `SCRAPER_HOUR`: Ora scraping tariffe (default: 11)
- `CHECKER_HOUR`: Ora controllo e notifiche (default: 12)
- `LOG_LEVEL`: Verbosità log (default: INFO)

### 3. Verifica docker-compose.yml

Il file è già configurato per usare l'immagine da GHCR:

```yaml
services:
  octotracker:
    image: ghcr.io/dstmrk/octotracker:latest
    # ... resto della configurazione
```

Se preferisci fare build locale (es. per sviluppo):
```yaml
services:
  octotracker:
    # image: ghcr.io/dstmrk/octotracker:latest
    build: .
    # ... resto della configurazione
```

## 🎬 Primo Avvio

```bash
# Scarica l'immagine e avvia il container
docker compose pull
docker compose up -d

# Verifica che funzioni
docker compose logs -f
```

Dovresti vedere nel log:
```
INFO:bot:Bot avviato con successo!
INFO:bot:Webhook configurato: https://...
```

## 🔄 Aggiornamento alla Nuova Release

Quando viene pubblicata una nuova release su GitHub, l'immagine Docker viene automaticamente costruita e pubblicata su GHCR.

### Processo di aggiornamento:

```bash
# 1. Scarica la nuova immagine
docker compose pull

# 2. Ricrea il container con la nuova immagine
docker compose up -d

# 3. Verifica che tutto funzioni
docker compose logs -f

# 4. (Opzionale) Rimuovi immagini vecchie
docker image prune -f
```

**IMPORTANTE:** Il file `.env` e i dati in `./data/` vengono preservati durante l'aggiornamento!

## 📊 Comandi Utili

### Gestione Container

```bash
# Verifica stato container
docker compose ps

# Visualizza log in tempo reale
docker compose logs -f

# Visualizza solo ultimi 100 log
docker compose logs --tail=100

# Riavvia il container
docker compose restart

# Ferma il container
docker compose down

# Ferma e rimuovi tutto (ATTENZIONE: mantiene i volumi)
docker compose down
```

### Maintenance

```bash
# Backup dati (database e JSON)
cp -r ./data ./data-backup-$(date +%Y%m%d)

# Pulizia spazio disco (rimuove immagini vecchie)
docker system prune -a

# Verifica dimensione immagine
docker images ghcr.io/dstmrk/octotracker
```

### Debug

```bash
# Accedi al container in esecuzione
docker compose exec octotracker /bin/bash

# Verifica variabili d'ambiente nel container
docker compose exec octotracker env | grep TELEGRAM

# Health check manuale
curl http://localhost:8444/health
```

## 🔒 Sicurezza

### Best Practices

1. **WEBHOOK_SECRET**: Sempre impostato e complesso (32+ caratteri random)
2. **Firewall**: Limita accesso porta 8443 solo a IP di Telegram
3. **HTTPS**: Usa reverse proxy (nginx/Caddy) per terminazione TLS
4. **Backup**: Backup regolari di `./data/` (contiene database utenti e tariffe)

### IP Telegram per Firewall

Se vuoi limitare l'accesso webhook solo a Telegram:

```bash
# Ottieni range IP Telegram
curl https://core.telegram.org/bots/webhooks#the-short-version

# Esempio firewall rule (ufw)
sudo ufw allow from 149.154.160.0/20 to any port 8443
sudo ufw allow from 91.108.4.0/22 to any port 8443
```

## 🆘 Troubleshooting

### Bot non riceve messaggi

1. Verifica webhook configurato:
   ```bash
   curl "https://api.telegram.org/bot<TOKEN>/getWebhookInfo"
   ```

2. Verifica che l'URL sia raggiungibile da Telegram

3. Controlla i log:
   ```bash
   docker compose logs -f | grep -i error
   ```

### Errori di permessi su ./data

```bash
# Assicurati che la directory sia scrivibile
sudo chown -R $USER:$USER ./data
```

### Container si riavvia continuamente

```bash
# Verifica errori
docker compose logs --tail=50

# Problemi comuni:
# - TELEGRAM_BOT_TOKEN non valido
# - WEBHOOK_SECRET mancante
# - Porta 8443 già in uso
```

## 📝 Esempio Configurazione Reverse Proxy

### Caddy (consigliato - HTTPS automatico)

```caddy
octotracker.tuodominio.xyz {
    reverse_proxy localhost:8443
}
```

### Nginx

```nginx
server {
    listen 443 ssl http2;
    server_name octotracker.tuodominio.xyz;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://localhost:8443;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

## 🎯 Workflow Consigliato

### Setup Iniziale
```bash
# 1. Clone + configurazione
git clone https://github.com/dstmrk/octotracker.git
cd octotracker
cp .env.example .env
nano .env

# 2. Primo avvio
docker compose pull
docker compose up -d
docker compose logs -f
```

### Aggiornamenti Regolari
```bash
# Quando vedi nuova release su GitHub
docker compose pull
docker compose up -d

# Verifica dopo pochi secondi
docker compose ps
docker compose logs --tail=50
```

### Manutenzione Mensile
```bash
# Backup dati
cp -r ./data ./data-backup-$(date +%Y%m%d)

# Pulizia spazio disco
docker system prune -a

# Verifica salute bot
curl http://localhost:8444/health
```

## ℹ️ Note

- **Persistenza dati**: Il database e i JSON delle tariffe sono in `./data/` (volume Docker)
- **Timezone**: Configurato su `Europe/Rome` nel docker-compose.yml
- **Log rotation**: Configurato automaticamente (max 10MB × 3 file)
- **Restart policy**: `unless-stopped` (si riavvia automaticamente dopo reboot)

## 📚 Link Utili

- [Repository GitHub](https://github.com/dstmrk/octotracker)
- [GHCR Package](https://github.com/dstmrk/octotracker/pkgs/container/octotracker)
- [Telegram Bot API](https://core.telegram.org/bots/api)
- [Docker Compose Docs](https://docs.docker.com/compose/)

---

Per domande o problemi, apri una issue su GitHub!
