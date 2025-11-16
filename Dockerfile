# Dockerfile per OctoTracker - Ottimizzato per dimensione ridotta
FROM python:3.11-slim

# Directory di lavoro
WORKDIR /app

# Installa uv (copiando binary da immagine ufficiale - metodo più veloce)
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# Copia file dipendenze
COPY pyproject.toml .

# Installa solo dipendenze di produzione (senza installare il progetto come package)
# Il progetto è un'applicazione standalone, non una libreria da installare
RUN uv pip install --system \
    "python-telegram-bot[webhooks]>=20.7" \
    "python-dotenv>=1.0.0" && \
    # Pulizia per ridurre dimensione
    rm -rf ~/.cache/uv && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

# Copia tutto il codice dell'applicazione
# (.dockerignore escluderà tests/, .github/, documentazione, etc.)
COPY . .

# Crea directory per dati (sarà montata come volume)
RUN mkdir -p /app/data

# Variabili d'ambiente di default (possono essere sovrascritte)
ENV TELEGRAM_BOT_TOKEN=""
ENV SCRAPER_HOUR="11"
ENV CHECKER_HOUR="12"
ENV TZ="Europe/Rome"
ENV LOG_LEVEL="INFO"

# Avvia il bot direttamente con python (non serve uv run per app standalone)
CMD ["python", "-u", "bot.py"]
