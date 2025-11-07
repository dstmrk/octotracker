# Dockerfile per OctoTracker - Ottimizzato per dimensione ridotta
FROM python:3.11-slim

# Directory di lavoro
WORKDIR /app

# Installa dipendenze Python con uv (molto più veloce) + Playwright in un layer
COPY requirements.txt .
RUN pip install --no-cache-dir uv && \
    uv pip install --system --no-cache -r requirements.txt && \
    playwright install chromium && \
    playwright install-deps chromium && \
    # Pulizia aggressiva per ridurre dimensione
    rm -rf /root/.cache/ms-playwright/webkit* /root/.cache/ms-playwright/firefox* && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* /root/.cache/pip && \
    pip uninstall -y uv  # Rimuovi uv, non serve a runtime

# Copia tutto il codice
COPY . .

# Crea directory per dati (sarà montata come volume)
RUN mkdir -p /app/data

# Variabili d'ambiente di default (possono essere sovrascritte)
ENV TELEGRAM_BOT_TOKEN=""
ENV SCRAPER_HOUR="9"
ENV CHECKER_HOUR="10"
ENV KEEPALIVE_INTERVAL_MINUTES="0"
ENV TZ="Europe/Rome"

# Healthcheck opzionale
HEALTHCHECK --interval=5m --timeout=10s --start-period=30s --retries=3 \
    CMD pgrep -f "python bot.py" || exit 1

# Avvia il bot
CMD ["python", "-u", "bot.py"]
