#!/bin/bash
# Script di build per Render - installa dipendenze Python e Playwright

set -e

echo "📦 Installing Python dependencies..."
pip install -r requirements.txt

echo "🎭 Installing Playwright browsers..."
playwright install chromium

echo "📚 Installing Playwright system dependencies..."
playwright install-deps chromium

echo "✅ Build completed!"
