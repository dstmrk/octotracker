# 🚀 Setup Completo su Render (Avanzato)

Se preferisci eseguire **tutto su Render** (bot, scraper e checker) invece di usare GitHub Actions, questa guida fa per te.

⚠️ **Nota**: Questa configurazione è più complessa perché richiede la gestione di git push da Render. La configurazione ibrida (Render + GitHub Actions) descritta nel README principale è più semplice e consigliata.

## Prerequisiti

1. Account Render gratuito
2. Bot Telegram creato con BotFather
3. GitHub Personal Access Token con permessi `repo`

## File da modificare

### 1. Aggiorna `render.yaml`

Sostituisci il contenuto del file `render.yaml` con:

```yaml
services:
  # Bot Telegram - gira 24/7
  - type: worker
    name: octotracker-bot
    runtime: python
    plan: free
    buildCommand: "pip install -r requirements.txt"
    startCommand: "python bot.py"
    envVars:
      - key: TELEGRAM_BOT_TOKEN
        sync: false
      - key: PYTHON_VERSION
        value: 3.11.0

  # Scraper - cron giornaliero alle 9:00 (ora italiana)
  - type: cron
    name: octotracker-scraper
    runtime: python
    plan: free
    schedule: "0 8 * * *"  # 8:00 UTC = 9:00 Italia
    buildCommand: "./build.sh"
    startCommand: "python scraper.py && ./push_changes.sh"
    envVars:
      - key: PYTHON_VERSION
        value: 3.11.0
      - key: GITHUB_TOKEN
        sync: false
      - key: GITHUB_REPO
        value: "dstmrk/octotracker"
      - key: GITHUB_BRANCH
        value: "main"

  # Checker - cron giornaliero alle 10:00 (ora italiana)
  - type: cron
    name: octotracker-checker
    runtime: python
    plan: free
    schedule: "0 9 * * *"  # 9:00 UTC = 10:00 Italia
    buildCommand: "pip install -r requirements.txt"
    startCommand: "python checker.py"
    envVars:
      - key: TELEGRAM_BOT_TOKEN
        sync: false
      - key: PYTHON_VERSION
        value: 3.11.0
```

### 2. Aggiorna `push_changes.sh`

Il file è già pronto, ma verifica che contenga:

```bash
#!/bin/bash
set -e

if [ -z "$GITHUB_TOKEN" ]; then
    echo "⚠️ GITHUB_TOKEN non impostato, skip push"
    exit 0
fi

git config --global user.email "render-bot@octotracker.app"
git config --global user.name "Render Bot"

if git diff --quiet && git diff --staged --quiet; then
    echo "ℹ️ Nessuna modifica da committare"
    exit 0
fi

echo "📤 Pushing changes to GitHub..."
git add data/current_rates.json data/last_scrape.png
git commit -m "Update tariffe $(date +'%Y-%m-%d') [Render]"

REPO="${GITHUB_REPO:-dstmrk/octotracker}"
BRANCH="${GITHUB_BRANCH:-main}"
git push https://${GITHUB_TOKEN}@github.com/${REPO}.git HEAD:${BRANCH}

echo "✅ Changes pushed successfully!"
```

## Setup su Render

### 1. Deploy Blueprint

1. Vai su [Dashboard Render](https://dashboard.render.com)
2. Clicca **"New +"** → **"Blueprint"**
3. Seleziona il tuo repository
4. Render creerà 3 servizi:
   - **octotracker-bot** (Background Worker)
   - **octotracker-scraper** (Cron Job)
   - **octotracker-checker** (Cron Job)

### 2. Configura Variabili d'Ambiente

**Per octotracker-bot:**
- `TELEGRAM_BOT_TOKEN`: il tuo token da BotFather

**Per octotracker-scraper:**
- `GITHUB_TOKEN`: [crea qui](https://github.com/settings/tokens) con scope `repo`
- `GITHUB_REPO`: `tuo-username/octotracker`
- `GITHUB_BRANCH`: `main` (o il tuo branch principale)

**Per octotracker-checker:**
- `TELEGRAM_BOT_TOKEN`: il tuo token da BotFather

### 3. Verifica i Deploy

1. Controlla che tutti e 3 i servizi si avviino correttamente
2. Il primo build dello scraper richiede più tempo (installa Playwright)
3. Verifica i logs per eventuali errori

## Limitazioni Cron Jobs su Render

⚠️ **Importante**: I cron job su Render hanno alcune limitazioni:

1. **Nessun accesso git nativo**: Ogni esecuzione parte da zero, senza la cronologia git
2. **Ambiente isolato**: Devi clonare il repo ogni volta o usare le API GitHub
3. **Complessità**: Più difficile da debuggare rispetto a GitHub Actions

### Soluzione: Clone Repository

Modifica `scraper.py` per clonare prima il repository:

```python
import subprocess
import os

# All'inizio dello script
if not os.path.exists('.git'):
    subprocess.run(['git', 'clone', f'https://{os.getenv("GITHUB_TOKEN")}@github.com/{os.getenv("GITHUB_REPO")}.git', '/tmp/repo'])
    os.chdir('/tmp/repo')
```

## Alternative

Se trovi questa configurazione troppo complessa, considera:

1. **Setup ibrido** (consigliato): Render per il bot, GitHub Actions per scraper/checker
2. **Database esterno**: Salva i dati su Supabase/Firebase invece che su GitHub
3. **API GitHub**: Usa l'API GitHub per creare commit invece di git push

## Troubleshooting

### Scraper fallisce con errore Playwright

Verifica che `build.sh` sia eseguibile:
```bash
chmod +x build.sh
```

### Git push fallisce

1. Verifica che `GITHUB_TOKEN` sia valido
2. Controlla i permessi del token (scope `repo`)
3. Verifica che `GITHUB_REPO` e `GITHUB_BRANCH` siano corretti

### Cron job non si esegue

1. Verifica lo schedule nella dashboard Render
2. Controlla i logs del cron job
3. Aspetta fino alla prossima esecuzione schedulata
