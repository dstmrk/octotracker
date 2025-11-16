#!/usr/bin/env python3
"""
Bot Telegram OctoTracker - Tutto in uno
Gestisce bot, scraper schedulato e checker schedulato
"""
import asyncio
import logging
import os
from datetime import datetime, timedelta
from warnings import filterwarnings

from dotenv import load_dotenv
from telegram import Update
from telegram.constants import ParseMode
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut
from telegram.ext import (
    Application,
    CallbackQueryHandler,
    CommandHandler,
    ContextTypes,
    ConversationHandler,
    MessageHandler,
    filters,
)
from telegram.warnings import PTBUserWarning

# Silenzia warning ConversationHandler per CallbackQueryHandler con per_message=False
# Questo è il comportamento corretto per il nostro caso d'uso (flusso lineare, non menu interattivo)
filterwarnings(action="ignore", message=r".*CallbackQueryHandler", category=PTBUserWarning)

# Import moduli interni
from checker import check_and_notify_users
from data_reader import fetch_octopus_tariffe
from database import init_db
from handlers.commands import (
    cancel_conversation,
    help_command,
    remove_data,
    status,
    unknown_command,
)
from handlers.feedback import (
    COMMENT,
    RATING,
    feedback_cancel,
    feedback_command,
    feedback_comment,
    feedback_rating,
    feedback_skip_comment,
)
from handlers.registration import (
    GAS_COMM,
    GAS_CONSUMO,
    GAS_ENERGIA,
    HA_GAS,
    LUCE_COMM,
    LUCE_CONSUMO_F1,
    LUCE_CONSUMO_F2,
    LUCE_CONSUMO_F3,
    LUCE_ENERGIA,
    LUCE_TIPO_VARIABILE,
    TIPO_TARIFFA,
    VUOI_CONSUMI_GAS,
    VUOI_CONSUMI_LUCE,
    gas_comm,
    gas_consumo,
    gas_energia,
    ha_gas,
    luce_comm,
    luce_consumo_f1,
    luce_consumo_f2,
    luce_consumo_f3,
    luce_energia,
    luce_tipo_variabile,
    start,
    tipo_tariffa,
    vuoi_consumi_gas,
    vuoi_consumi_luce,
)

load_dotenv()

# Configurazione logging
LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
logging.basicConfig(
    level=getattr(logging, LOG_LEVEL, logging.INFO),
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)

# Setup logger per questo modulo
logger = logging.getLogger(__name__)

# Riduci verbosità librerie esterne
logging.getLogger("telegram").setLevel(logging.WARNING)
logging.getLogger("httpx").setLevel(logging.WARNING)
logging.getLogger("httpcore").setLevel(logging.WARNING)

# Configurazione scheduler
SCRAPER_HOUR = int(os.getenv("SCRAPER_HOUR", "9"))  # Default: 9:00 ora italiana
CHECKER_HOUR = int(os.getenv("CHECKER_HOUR", "10"))  # Default: 10:00 ora italiana

# Configurazione webhook
WEBHOOK_URL = os.getenv("WEBHOOK_URL", "")  # Es: https://octotracker.tuodominio.xyz
WEBHOOK_PORT = int(os.getenv("WEBHOOK_PORT", "8443"))
HEALTH_PORT = int(os.getenv("HEALTH_PORT", "8444"))  # Porta health check separata

# Validazione WEBHOOK_SECRET obbligatorio (protezione da webhook spoofing)
WEBHOOK_SECRET = os.getenv("WEBHOOK_SECRET")
if not WEBHOOK_SECRET:
    raise ValueError(
        "WEBHOOK_SECRET è obbligatorio per sicurezza del webhook.\n"
        "Genera un token sicuro con:\n"
        "  python -c 'import secrets; print(secrets.token_urlsafe(32))'\n"
        "Poi aggiungilo al file .env:\n"
        "  WEBHOOK_SECRET=<token_generato>"
    )

# Configurazione admin (opzionale - per alert errori critici)
ADMIN_USER_ID = os.getenv("ADMIN_USER_ID")  # ID Telegram dell'admin


# ========== SCHEDULER ==========


async def run_scraper() -> None:
    """Esegue scraper delle tariffe"""
    logger.info("🕷️  Avvio scraper...")
    try:
        result = await fetch_octopus_tariffe()
        logger.info(f"✅ Scraper completato: {result}")
    except ConnectionError as e:
        logger.error(f"🌐 Errore di connessione scraper: {e}")
    except OSError as e:
        logger.error(f"💾 Errore I/O scraper: {e}")
    except Exception as e:
        logger.error(f"❌ Errore inatteso scraper: {e}")


async def run_checker(bot_token: str) -> None:
    """Esegue checker e invia notifiche"""
    logger.info("🔍 Avvio checker...")
    try:
        await check_and_notify_users(bot_token)
        logger.info("✅ Checker completato")
    except TelegramError as e:
        logger.error(f"❌ Errore Telegram checker: {e}")
    except NetworkError as e:
        logger.error(f"🌐 Errore di rete checker: {e}")
    except OSError as e:
        logger.error(f"💾 Errore I/O checker: {e}")
    except Exception as e:
        logger.error(f"❌ Errore inatteso checker: {e}")


def calculate_seconds_until_next_run(target_hour: int) -> float:
    """
    Calcola secondi fino alla prossima esecuzione all'ora target.

    Args:
        target_hour: Ora del giorno (0-23) in cui eseguire il task

    Returns:
        Secondi fino alla prossima esecuzione
    """
    now = datetime.now()
    target = now.replace(hour=target_hour, minute=0, second=0, microsecond=0)

    # Se l'orario è già passato oggi, schedula per domani
    if now >= target:
        target += timedelta(days=1)

    delta = (target - now).total_seconds()
    return delta


async def scraper_daily_task() -> None:
    """Task giornaliero per lo scraper - si esegue una volta al giorno"""
    # Calcola quanto dormire fino alla prima esecuzione
    seconds_until_run = calculate_seconds_until_next_run(SCRAPER_HOUR)
    hours_until_run = seconds_until_run / 3600

    logger.info(f"🕷️  Scraper schedulato per le {SCRAPER_HOUR}:00 (tra {hours_until_run:.1f} ore)")
    await asyncio.sleep(seconds_until_run)

    # Loop infinito: esegui e ricalcola il prossimo run time
    while True:
        await run_scraper()

        # Ricalcola secondi fino alla prossima esecuzione (previene drift temporale)
        seconds_until_next = calculate_seconds_until_next_run(SCRAPER_HOUR)
        hours_until_next = seconds_until_next / 3600

        logger.info(f"⏰ Prossimo scraper tra {hours_until_next:.1f} ore (alle {SCRAPER_HOUR}:00)")
        await asyncio.sleep(seconds_until_next)


async def checker_daily_task(bot_token: str) -> None:
    """Task giornaliero per il checker - si esegue una volta al giorno"""
    # Calcola quanto dormire fino alla prima esecuzione
    seconds_until_run = calculate_seconds_until_next_run(CHECKER_HOUR)
    hours_until_run = seconds_until_run / 3600

    logger.info(f"🔍 Checker schedulato per le {CHECKER_HOUR}:00 (tra {hours_until_run:.1f} ore)")
    await asyncio.sleep(seconds_until_run)

    # Loop infinito: esegui e ricalcola il prossimo run time
    while True:
        await run_checker(bot_token)

        # Ricalcola secondi fino alla prossima esecuzione (previene drift temporale)
        seconds_until_next = calculate_seconds_until_next_run(CHECKER_HOUR)
        hours_until_next = seconds_until_next / 3600

        logger.info(f"⏰ Prossimo checker tra {hours_until_next:.1f} ore (alle {CHECKER_HOUR}:00)")
        await asyncio.sleep(seconds_until_next)


# ========== ERROR HANDLER ==========


async def error_handler(update: object, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Gestisce errori con retry, alert admin e logging migliorato"""
    error = context.error

    # Log base con stack trace completo
    logger.error(f"❌ Errore: {error}", exc_info=True)

    # Gestione per tipo di errore
    if isinstance(error, (TimedOut, NetworkError)):
        # Errori temporanei - python-telegram-bot gestisce retry automaticamente
        logger.warning(
            "⏱️  Timeout/errore di rete - probabilmente connessione lenta. Il bot riproverà automaticamente."
        )

    elif isinstance(error, RetryAfter):
        # Rate limit di Telegram - dobbiamo aspettare
        logger.warning(f"⏱️  Rate limit: attendo {error.retry_after}s")
        await asyncio.sleep(error.retry_after)

    else:
        # Errore non gestito - potenzialmente critico
        logger.error(f"⚠️  Errore non gestito: {type(error).__name__}")

        # Invia alert all'admin se configurato
        if ADMIN_USER_ID and context.application:
            try:
                error_msg = (
                    f"🚨 <b>Errore Bot OctoTracker</b>\n\n"
                    f"<b>Tipo:</b> {type(error).__name__}\n"
                    f"<b>Messaggio:</b> {str(error)[:200]}\n"
                    f"<b>Update:</b> {update}"
                )
                await context.application.bot.send_message(
                    chat_id=ADMIN_USER_ID, text=error_msg, parse_mode=ParseMode.HTML
                )
                logger.info(f"📨 Alert errore inviato all'admin {ADMIN_USER_ID}")
            except Exception as e:
                logger.error(f"❌ Errore invio alert admin: {e}")


# ========== MAIN ==========


async def run_health_server(application_data: dict) -> None:
    """
    Avvia server HTTP separato per health check endpoint.

    Usa Tornado per servire /health su porta 8444 (default).
    Separato dal webhook per evitare conflitti con python-telegram-bot.
    """
    from tornado.httpserver import HTTPServer
    from tornado.web import Application as TornadoApp

    from health_handler import HealthHandler

    # Crea applicazione Tornado con health handler
    health_app = TornadoApp(
        [
            (r"/health", HealthHandler, {"application_data": application_data}),
        ]
    )

    # Avvia server sulla porta health
    server = HTTPServer(health_app)
    server.listen(HEALTH_PORT, address="0.0.0.0")

    logger.info(f"🏥 Health check endpoint attivo su porta {HEALTH_PORT}")

    # Keep alive - tornado usa il suo event loop
    # asyncio.sleep(0) per permettere scheduling
    while True:
        await asyncio.sleep(3600)  # Check ogni ora che il loop sia vivo


async def post_init(application: Application) -> None:
    """Avvia scheduler dopo l'inizializzazione del bot

    Note: Questa funzione deve essere async perché è richiesta dal framework
    python-telegram-bot come callback di post_init.
    """
    bot_token = application.bot.token

    # Avvia i due task giornalieri separati in background
    # Salva i task per evitare garbage collection prematura
    scraper_task = asyncio.create_task(scraper_daily_task())
    checker_task = asyncio.create_task(checker_daily_task(bot_token))

    # Avvia health check server separato
    health_task = asyncio.create_task(run_health_server(application.bot_data))

    # Salva i task nell'application per mantenerli vivi
    application.bot_data["scraper_task"] = scraper_task
    application.bot_data["checker_task"] = checker_task
    application.bot_data["health_task"] = health_task

    # Yield control per permettere all'event loop di schedulare i task
    await asyncio.sleep(0)


def main() -> None:
    """Avvia il bot con scheduler integrato"""
    token = os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato")

    # Inizializza database
    init_db()
    logger.info("💾 Database inizializzato")

    logger.info("🤖 Avvio OctoTracker...")
    logger.info("📡 Modalità: WEBHOOK")
    logger.info(f"⏰ Scraper schedulato: {SCRAPER_HOUR}:00")
    logger.info(f"⏰ Checker schedulato: {CHECKER_HOUR}:00")
    logger.info(f"🌐 Webhook URL: {WEBHOOK_URL}")
    logger.info(f"🔌 Porta webhook: {WEBHOOK_PORT}")
    logger.info(f"🏥 Porta health check: {HEALTH_PORT}")

    # Costruisci app con timeout ottimizzati per bilanciare performance e affidabilità
    app = (
        Application.builder()
        .token(token)
        .post_init(post_init)
        .connect_timeout(10.0)  # Timeout connessione - più breve per fail-fast (default: 5.0)
        .read_timeout(30.0)  # Timeout lettura - alto per upload/operazioni lunghe (default: 5.0)
        .write_timeout(15.0)  # Timeout scrittura - medio (default: 5.0)
        .pool_timeout(10.0)  # Timeout pool connessioni - breve (default: 1.0)
        .build()
    )

    # Handler conversazione registrazione
    conv_handler = ConversationHandler(
        entry_points=[CommandHandler("start", start), CommandHandler("update", start)],
        states={
            TIPO_TARIFFA: [CallbackQueryHandler(tipo_tariffa)],
            LUCE_TIPO_VARIABILE: [CallbackQueryHandler(luce_tipo_variabile)],
            LUCE_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_energia)],
            LUCE_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_comm)],
            VUOI_CONSUMI_LUCE: [CallbackQueryHandler(vuoi_consumi_luce)],
            LUCE_CONSUMO_F1: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_consumo_f1)],
            LUCE_CONSUMO_F2: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_consumo_f2)],
            LUCE_CONSUMO_F3: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_consumo_f3)],
            HA_GAS: [CallbackQueryHandler(ha_gas)],
            GAS_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_energia)],
            GAS_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_comm)],
            VUOI_CONSUMI_GAS: [CallbackQueryHandler(vuoi_consumi_gas)],
            GAS_CONSUMO: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_consumo)],
        },
        fallbacks=[
            CommandHandler("cancel", cancel_conversation),
            CommandHandler("help", help_command),
        ],
        per_message=False,  # CallbackQueryHandler non tracciato per ogni messaggio
    )

    app.add_handler(conv_handler)

    # Handler conversazione feedback
    feedback_handler = ConversationHandler(
        entry_points=[CommandHandler("feedback", feedback_command)],
        states={
            RATING: [CallbackQueryHandler(feedback_rating, pattern=r"^rating_\d$")],
            COMMENT: [
                MessageHandler(filters.TEXT & ~filters.COMMAND, feedback_comment),
                CallbackQueryHandler(feedback_skip_comment, pattern=r"^skip_comment$"),
            ],
        },
        fallbacks=[CommandHandler("cancel", feedback_cancel)],
        per_message=False,
    )

    app.add_handler(feedback_handler)
    app.add_handler(CommandHandler("cancel", cancel_conversation))
    app.add_handler(CommandHandler("status", status))
    app.add_handler(CommandHandler("remove", remove_data))
    app.add_handler(CommandHandler("help", help_command))

    # Handler per comandi non riconosciuti (deve essere dopo tutti gli altri CommandHandler)
    app.add_handler(MessageHandler(filters.COMMAND, unknown_command))

    # Registra error handler per gestire timeout e errori di rete
    app.add_error_handler(error_handler)

    logger.info("✅ Bot configurato!")

    # Verifica webhook URL
    if not WEBHOOK_URL:
        raise ValueError("WEBHOOK_URL richiesto")

    logger.info(f"🚀 Avvio webhook su {WEBHOOK_URL}...")

    # Configura webhook con retry per Docker
    # Health endpoint su porta separata (vedi run_health_server in post_init)
    app.run_webhook(
        listen="0.0.0.0",
        port=WEBHOOK_PORT,
        url_path=token,  # Usa il token come path per sicurezza
        webhook_url=f"{WEBHOOK_URL}/{token}",
        secret_token=WEBHOOK_SECRET,  # Validato all'avvio (protezione webhook spoofing)
        allowed_updates=Update.ALL_TYPES,
        drop_pending_updates=True,  # Evita messaggi vecchi
        bootstrap_retries=3,  # Retry se setWebhook fallisce al primo tentativo
    )


if __name__ == "__main__":
    main()
