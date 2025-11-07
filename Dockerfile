# Dockerfile per OctoTracker - Ottimizzato per Raspberry Pi (ARM)
FROM python:3.11-slim

# Installa dipendenze di sistema per Playwright
RUN apt-get update && apt-get install -y \
    wget \
    gnupg \
    ca-certificates \
    fonts-liberation \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libatspi2.0-0 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libgbm1 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libwayland-client0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxkbcommon0 \
    libxrandr2 \
    xdg-utils \
    && rm -rf /var/lib/apt/lists/*

# Directory di lavoro
WORKDIR /app

# Copia requirements e installa dipendenze Python
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Installa Playwright e browser Chromium
RUN playwright install chromium
RUN playwright install-deps chromium

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
