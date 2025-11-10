# Dockerfile per OctoTracker - Ottimizzato per dimensione ridotta
FROM python:3.11-slim

# Directory di lavoro
WORKDIR /app

# Installa uv (copiando binary da immagine ufficiale - metodo più veloce)
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# Copia file dipendenze e sincronizza
COPY pyproject.toml .
RUN uv sync --no-dev && \
    uv run playwright install chromium && \
    uv run playwright install-deps chromium && \
    # Pulizia aggressiva per ridurre dimensione
    rm -rf /root/.cache/ms-playwright/webkit* /root/.cache/ms-playwright/firefox* && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Copia tutto il codice
COPY . .

# Crea directory per dati (sarà montata come volume)
RUN mkdir -p /app/data

# Variabili d'ambiente di default (possono essere sovrascritte)
ENV TELEGRAM_BOT_TOKEN=""
ENV SCRAPER_HOUR="9"
ENV CHECKER_HOUR="10"
ENV TZ="Europe/Rome"

# Avvia il bot con uv
CMD ["uv", "run", "python", "-u", "bot.py"]
