# 🔌 OctoTracker

Bot Telegram che monitora le tariffe Octopus Energy e ti avvisa quando ci sono offerte più convenienti.

## 🎯 Funzionalità

- **Bot Telegram** per registrare le tue tariffe attuali (luce e gas)
- **Scraping automatico** delle tariffe Octopus Energy (solo mono-orarie fisse)
- **Controllo giornaliero** e notifica se ci sono tariffe più convenienti
- **Zero costi**: gira tutto su GitHub Actions (gratis)
- **Zero manutenzione**: tutto automatico

## 🚀 Setup (5 minuti)

### 1. Crea il Bot Telegram

1. Apri Telegram e cerca `@BotFather`
2. Invia `/newbot` e segui le istruzioni
3. Copia il **token** che ti viene dato (tipo: `123456789:ABCdefGHIjklMNOpqrsTUVwxyz`)

### 2. Configura GitHub Secrets

1. Vai su **Settings** → **Secrets and variables** → **Actions**
2. Clicca **New repository secret**
3. Nome: `TELEGRAM_BOT_TOKEN`
4. Valore: incolla il token di BotFather
5. Salva

### 3. Abilita GitHub Actions

1. Vai su **Actions** nel tuo repository
2. Se chiede di abilitarle, clicca su **"I understand my workflows, go ahead and enable them"**

### 4. Dai permessi di scrittura a GitHub Actions

1. Vai su **Settings** → **Actions** → **General**
2. Scorri fino a **Workflow permissions**
3. Seleziona **"Read and write permissions"**
4. Salva

### 5. Avvia il bot (per test locale)

```bash
# Installa dipendenze
pip install -r requirements.txt
playwright install chromium

# Crea file .env
echo "TELEGRAM_BOT_TOKEN=il_tuo_token_qui" > .env

# Avvia bot
python bot.py
```

### 6. Registra le tue tariffe

1. Apri Telegram e cerca il tuo bot (il nome che hai scelto)
2. Invia `/start`
3. Inserisci le tue tariffe attuali quando richiesto

### 7. Test manuale (opzionale)

```bash
# Test scraping
python scraper.py

# Test controllo tariffe
python checker.py
```

## 🤖 Come funziona

### Comandi Bot

- `/start` - Registra le tue tariffe (prima volta)
- `/update` - Aggiorna le tue tariffe
- `/status` - Visualizza i tuoi dati salvati
- `/remove` - Cancella i tuoi dati
- `/help` - Mostra messaggio di aiuto con tutti i comandi
- `/cancel` - Annulla registrazione in corso

### Automazione GitHub Actions

**Scraping giornaliero** (`scrape-daily.yml`)
- Ogni **giorno alle 9:00** (ora italiana)
- Scarica tariffe Octopus Energy
- Commit automatico in `data/current_rates.json`

**Controllo giornaliero** (`check-daily.yml`)
- Ogni **giorno alle 10:00** (ora italiana)
- Confronta tariffe per ogni utente
- Invia notifica Telegram se trova risparmi

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
  },
  "987654321": {
    "luce_energia": 0.13,
    "luce_comm": 102.00,
    "gas_energia": null,
    "gas_comm": null
  }
}
```
Note:
- `gas_energia` e `gas_comm` sono `null` se l'utente ha solo luce
- `last_notified_rates` memorizza le ultime tariffe Octopus notificate (evita notifiche duplicate)
- Tutti i costi di commercializzazione sono in €/anno

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

## 🛠️ Sviluppo

### File Principali

- `bot.py` - Bot Telegram per registrazione utenti
- `scraper.py` - Playwright scraper per tariffe Octopus
- `checker.py` - Controllo e invio notifiche
- `.github/workflows/` - GitHub Actions

### Test locale completo

```bash
# 1. Scraping
python scraper.py

# 2. Avvia bot in background
python bot.py &

# 3. Registrati su Telegram

# 4. Testa checker
python checker.py
```

## 📝 Note

- **Tariffe supportate**: solo mono-orarie fisse
- **Fonte**: https://octopusenergy.it/le-nostre-tariffe
- **Automazione**: scraping alle 9:00, controllo alle 10:00 (ora italiana)
- **Utenti**: può avere solo luce, oppure luce + gas
- **Anti-spam**: ricevi notifica solo quando le tariffe Octopus cambiano (non ogni giorno se rimangono uguali)
- **Privacy**: dati salvati solo nel tuo repository
- **Unità**: costi commercializzazione in €/anno

## 🔮 Possibili miglioramenti futuri

**Funzionalità aggiuntive**
- [ ] **Data ultimo aggiornamento**: salva quando l'utente ha inserito/aggiornato le tariffe, per mostrare "Ultimo aggiornamento: 03/11/2025" nello `/status` e implementare reminder automatici (es. "non aggiorni da 3 mesi")
- [ ] **Orario preferito notifiche**: campo opzionale `notify_hour` per permettere agli utenti di scegliere se ricevere messaggi al mattino o alla sera
- [ ] Supporto tariffe bi-orarie e variabili
- [ ] Stima risparmio annuale basata su consumi
- [ ] Storico tariffe con grafici andamento prezzi

## 📄 Licenza

MIT