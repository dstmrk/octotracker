# 🔌 OctoTracker

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram 24/7** per registrare e gestire le tue tariffe (luce e gas)
- **Scraping automatico** delle tariffe Octopus Energy (solo mono-orarie fisse)
- **Controllo giornaliero** e notifica se ci sono tariffe più convenienti
- **Zero costi**: hosting gratuito su Render
- **Zero manutenzione**: tutto automatico

## 🚀 Setup (5 minuti)

### 1. Crea il Bot Telegram

1. Apri Telegram e cerca `@BotFather`
2. Invia `/newbot` e segui le istruzioni
3. Copia il **token** che ti viene dato (tipo: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`)

### 2. Crea GitHub Personal Access Token

1. Vai su [github.com/settings/tokens](https://github.com/settings/tokens)
2. Clicca **Generate new token** → **Generate new token (classic)**
3. Dai un nome (es: "OctoTracker Render")
4. Seleziona scope: **repo** (tutti i permessi del repo)
5. Clicca **Generate token** e copia il token

### 3. Deploy su Render

1. Vai su [render.com](https://render.com) e crea un account gratuito
2. Collega il tuo account GitHub
3. Dalla [Dashboard Render](https://dashboard.render.com):
   - Clicca **"New +"** → **"Blueprint"**
   - Seleziona questo repository
   - Clicca **"Apply"**

4. Render creerà automaticamente **3 servizi**:
   - `octotracker-bot` (Worker - bot attivo 24/7)
   - `octotracker-scraper` (Cron - scraping giornaliero ore 9:00)
   - `octotracker-checker` (Cron - controllo giornaliero ore 10:00)

### 4. Configura Variabili d'Ambiente

Per **ogni servizio** creato, vai su **Environment** e aggiungi:

**Per tutti e 3 i servizi:**
- `GITHUB_TOKEN` = il token GitHub che hai creato al passo 2

**Per octotracker-bot e octotracker-checker:**
- `TELEGRAM_BOT_TOKEN` = il token da BotFather

**Nota**: `GITHUB_REPO` e `GITHUB_BRANCH` sono già configurati nel `render.yaml`. Cambia solo se hai fatto fork del repo.

### 5. Verifica Deploy

1. Aspetta che tutti e 3 i servizi completino il primo deploy (2-3 minuti)
2. Controlla i logs per verificare che non ci siano errori
3. Il bot dovrebbe essere online!

### 6. Usa il Bot

1. Apri Telegram e cerca il tuo bot (il nome che hai scelto con BotFather)
2. Invia `/start`
3. Segui le istruzioni per registrare le tue tariffe
4. Fatto! 🎉

## 🤖 Comandi Bot

- `/start` - Registra le tue tariffe (prima volta)
- `/update` - Aggiorna le tue tariffe
- `/status` - Visualizza i tuoi dati salvati
- `/remove` - Cancella i tuoi dati
- `/help` - Mostra tutti i comandi
- `/cancel` - Annulla registrazione in corso

## ⚡️ Come funziona

### Automazione

- **Scraper**: Ogni giorno alle **9:00** (ora italiana) scarica le tariffe Octopus Energy
- **Checker**: Ogni giorno alle **10:00** (ora italiana) controlla se ci sono risparmi e ti notifica
- **Bot**: Sempre attivo per rispondere ai tuoi comandi

### Sincronizzazione Dati

Tutti i dati (`users.json`, `current_rates.json`) sono salvati su GitHub:
- Il bot fa commit/push automatico quando registri/aggiorni le tariffe
- Scraper e checker sincronizzano i dati via git pull/push
- Questo garantisce che tutti i servizi vedano sempre dati aggiornati

### Struttura Dati

**data/users.json** - Dati degli utenti
```json
{
  "123456789": {
    "luce_energia": 0.12,
    "luce_comm": 96.00,
    "gas_energia": 0.45,
    "gas_comm": 144.00,
    "last_notified_rates": {
      "luce_energia": 0.10,
      "luce_comm": 72.00,
      "gas_energia": 0.38,
      "gas_comm": 84.00
    }
  }
}
```

**data/current_rates.json** - Tariffe Octopus
```json
{
  "luce": {
    "energia": 0.115,
    "commercializzazione": 96.00,
    "nome_tariffa": "Mono-oraria Fissa"
  },
  "gas": {
    "energia": 0.42,
    "commercializzazione": 138.00,
    "nome_tariffa": "Mono-oraria Fissa"
  },
  "data_aggiornamento": "2025-11-07"
}
```

## 🛠️ Sviluppo Locale

### Test locale

```bash
# Installa dipendenze
pip install -r requirements.txt
playwright install chromium

# Crea file .env
echo "TELEGRAM_BOT_TOKEN=il_tuo_token" > .env

# Test scraper
python scraper.py

# Test bot
python bot.py

# Test checker (richiede users.json e current_rates.json)
python checker.py
```

### File Principali

- `bot.py` - Bot Telegram con git sync
- `scraper.py` - Playwright scraper per tariffe Octopus
- `checker.py` - Controllo e invio notifiche
- `git_sync.py` - Helper per sincronizzazione GitHub
- `render.yaml` - Configurazione Blueprint Render
- `run_scraper.sh` - Script esecuzione scraper + git push
- `run_checker.sh` - Script esecuzione checker + git push
- `build.sh` - Build script per Playwright

## 📝 Note

- **Tariffe supportate**: solo mono-orarie fisse
- **Fonte**: https://octopusenergy.it/le-nostre-tariffe
- **Automazione**: scraping ore 9:00, controllo ore 10:00 (ora italiana)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Anti-spam**: ricevi notifica solo quando le tariffe Octopus cambiano
- **Privacy**: dati salvati nel tuo repository GitHub privato
- **Unità**: costi commercializzazione in €/anno
- **Costo**: 100% gratuito (Render free tier + repository pubblico/privato)

## 🔧 Troubleshooting

### Bot non risponde su Telegram
1. Verifica che il servizio `octotracker-bot` sia "Live" su Render
2. Controlla i logs per errori
3. Verifica che `TELEGRAM_BOT_TOKEN` sia corretto

### Scraper/Checker non funzionano
1. Controlla i logs dei cron jobs su Render
2. Verifica che `GITHUB_TOKEN` abbia permessi `repo`
3. Verifica che `GITHUB_REPO` punti al repo corretto (formato: `username/repo`)

### Errori git push
1. Il token GitHub deve avere scope `repo`
2. Se il repo è privato, verifica che il token abbia accesso
3. Controlla che `GITHUB_BRANCH` sia corretto (di solito `main`)

### Prima esecuzione scraper
- Il primo build dello scraper richiede 5-10 minuti (installa Playwright e browser)
- Le esecuzioni successive sono molto più veloci

## 🔮 Possibili miglioramenti futuri

- [ ] Supporto tariffe bi-orarie e variabili
- [ ] Stima risparmio annuale basata su consumi
- [ ] Storico tariffe con grafici
- [ ] Database esterno (PostgreSQL, Supabase) invece di JSON su GitHub
- [ ] Notifiche personalizzate per orario

## 📄 Licenza

MIT

---

⚠️ **Disclaimer**: OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy. È un progetto indipendente di monitoraggio tariffe.
