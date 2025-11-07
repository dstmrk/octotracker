#!/bin/bash
# Script per eseguire checker con dati aggiornati da GitHub

set -e

echo "🔧 Setup git..."
git config --global user.email "render-bot@octotracker.app"
git config --global user.name "Render Bot"

# Clone repository se necessario
if [ ! -d "/tmp/octotracker" ]; then
    echo "📥 Clonando repository..."
    git clone https://${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git /tmp/octotracker
fi

cd /tmp/octotracker

echo "🔄 Pull latest changes..."
git pull origin ${GITHUB_BRANCH}

echo "🔍 Eseguendo checker..."
python checker.py

# Se checker ha modificato users.json (aggiornato last_notified_rates), pusha
echo "📤 Checking for changes..."
if git diff --quiet && git diff --staged --quiet; then
    echo "ℹ️ Nessuna modifica da committare"
else
    git add data/users.json
    git commit -m "Update user notification state $(date +'%Y-%m-%d') [Render Checker]"
    git push https://${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git HEAD:${GITHUB_BRANCH}
    echo "✅ Changes pushed!"
fi
