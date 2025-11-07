#!/bin/bash
# Script di build per Render - installa dipendenze Python e Playwright

set -e

echo "📦 Installazione dipendenze Python..."
pip install -r requirements.txt

echo "🎭 Installazione Playwright browsers..."
playwright install chromium

echo "📚 Installazione dipendenze di sistema Playwright..."
playwright install-deps chromium

echo "✅ Build completato!"
