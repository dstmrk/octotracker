#!/bin/bash
# Script per eseguire scraper e pushare su GitHub

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

echo "🕷️ Eseguendo scraper..."
python scraper.py

echo "📤 Pushing changes..."
if git diff --quiet && git diff --staged --quiet; then
    echo "ℹ️ Nessuna modifica da committare"
else
    git add data/current_rates.json data/last_scrape.png
    git commit -m "Update tariffe $(date +'%Y-%m-%d') [Render Scraper]"
    git push https://${GITHUB_TOKEN}@github.com/${GITHUB_REPO}.git HEAD:${GITHUB_BRANCH}
    echo "✅ Changes pushed!"
fi
