#!/bin/bash
# Script per fare push dei cambiamenti da Render a GitHub

set -e

# Verifica che GITHUB_TOKEN sia impostato
if [ -z "$GITHUB_TOKEN" ]; then
    echo "⚠️ GITHUB_TOKEN non impostato, skip push"
    exit 0
fi

# Configura git
git config --global user.email "render-bot@octotracker.app"
git config --global user.name "Render Bot"

# Verifica se ci sono modifiche
if git diff --quiet && git diff --staged --quiet; then
    echo "ℹ️ Nessuna modifica da committare"
    exit 0
fi

# Commit e push
echo "📤 Pushing changes to GitHub..."
git add data/current_rates.json data/last_scrape.png
git commit -m "Update tariffe $(date +'%Y-%m-%d') [Render]"

# Usa GITHUB_TOKEN per autenticazione
git push https://${GITHUB_TOKEN}@github.com/dstmrk/octotracker.git HEAD:main

echo "✅ Changes pushed successfully!"
