#!/usr/bin/env python3
"""
Bot Telegram OctoTracker - Tutto in uno
Gestisce bot, scraper schedulato, checker schedulato e keep-alive
"""
import os
import json
import asyncio
from datetime import datetime, time
from pathlib import Path
from telegram import Update, InlineKeyboardButton, InlineKeyboardMarkup, Bot
from telegram.constants import ParseMode
from telegram.ext import Application, CommandHandler, MessageHandler, CallbackQueryHandler, filters, ContextTypes, ConversationHandler
from dotenv import load_dotenv

# Import moduli interni
from scraper import scrape_octopus_tariffe
from checker import check_and_notify_users

load_dotenv()

# Stati conversazione
LUCE_ENERGIA, LUCE_COMM, HA_GAS, GAS_ENERGIA, GAS_COMM = range(5)

# File dati
DATA_DIR = Path(__file__).parent / "data"
USERS_FILE = DATA_DIR / "users.json"

# Configurazione scheduler
SCRAPER_HOUR = int(os.getenv('SCRAPER_HOUR', '9'))  # Default: 9:00 ora italiana
CHECKER_HOUR = int(os.getenv('CHECKER_HOUR', '10'))  # Default: 10:00 ora italiana
KEEPALIVE_INTERVAL = int(os.getenv('KEEPALIVE_INTERVAL_MINUTES', '5'))  # Default: 5 minuti

# Configurazione webhook
BOT_MODE = os.getenv('BOT_MODE', 'polling').lower()  # 'polling' o 'webhook'
WEBHOOK_URL = os.getenv('WEBHOOK_URL', '')  # Es: https://octotracker.tuodominio.xyz
WEBHOOK_PORT = int(os.getenv('WEBHOOK_PORT', '8443'))
WEBHOOK_SECRET = os.getenv('WEBHOOK_SECRET', '')  # Token segreto per validazione

def load_users():
    """Carica dati utenti"""
    if USERS_FILE.exists():
        with open(USERS_FILE, 'r') as f:
            return json.load(f)
    return {}

def save_users(users):
    """Salva dati utenti"""
    DATA_DIR.mkdir(exist_ok=True)
    with open(USERS_FILE, 'w') as f:
        json.dump(users, f, indent=2)
    print(f"💾 Dati utenti salvati ({len(users)} utenti)")

# ========== BOT COMMANDS ==========

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Avvia registrazione tariffe"""
    users = load_users()
    user_id = str(update.effective_user.id)
    is_update = user_id in users

    if is_update:
        messaggio = (
            "♻️ <b>Aggiorniamo le tue tariffe!</b>\n\n"
            "Inserisci di nuovo i valori attuali così OctoTracker potrà confrontarli "
            "con le nuove offerte di Octopus Energy.\n\n"
            "Ti guiderò passo passo come la prima volta: prima la luce, poi (se ce l'hai) il gas.\n\n"
            "👉 Partiamo: quanto paghi ora per la materia energia luce (€/kWh)?"
        )
    else:
        messaggio = (
            "🐙 <b>Benvenuto su OctoTracker!</b>\n\n"
            "Questo bot controlla ogni giorno le tariffe di Octopus Energy e ti avvisa "
            "se ne trova di più convenienti rispetto alle tue attuali.\n\n"
            "Ti farò qualche semplice domanda per registrare le tue tariffe luce e (se ce l'hai) gas.\n"
            "Rispondi passo passo ai messaggi: ci vorrà meno di un minuto. ⚡️\n\n"
            "👉 Iniziamo con la luce: quanto paghi per la materia energia (€/kWh)?"
        )

    await update.message.reply_text(messaggio, parse_mode=ParseMode.HTML)
    return LUCE_ENERGIA

async def luce_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia luce"""
    try:
        context.user_data['luce_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "Perfetto! Ora indica il costo di commercializzazione luce, in euro/anno.\n\n"
            "💬 Esempio: 72 (se paghi 6 €/mese)"
        )
        return LUCE_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.12)")
        return LUCE_ENERGIA

async def luce_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo commercializzazione luce e chiedi se ha gas"""
    try:
        context.user_data['luce_comm'] = float(update.message.text.replace(',', '.'))

        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no")
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await update.message.reply_text(
            "Hai anche una fornitura gas attiva con Octopus Energy?",
            reply_markup=reply_markup
        )
        return HA_GAS
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 96.50)")
        return LUCE_COMM

async def ha_gas(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Gestisci risposta se ha gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "gas_si":
        await query.edit_message_text(
            "Perfetto!\n"
            "👉 Inserisci il costo materia energia gas (€/Smc)."
        )
        return GAS_ENERGIA
    else:
        return await salva_e_conferma(query, context, solo_luce=True)

async def gas_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia gas"""
    try:
        context.user_data['gas_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "Perfetto! Ora indica il costo di commercializzazione gas, in euro/anno.\n\n"
            "💬 Esempio: 84 (se paghi 7 €/mese)"
        )
        return GAS_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.45)")
        return GAS_ENERGIA

async def gas_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva gas e conferma"""
    try:
        context.user_data['gas_comm'] = float(update.message.text.replace(',', '.'))
        return await salva_e_conferma(update, context, solo_luce=False)
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 144.00)")
        return GAS_COMM

async def salva_e_conferma(update_or_query, context: ContextTypes.DEFAULT_TYPE, solo_luce: bool):
    """Salva dati utente e mostra conferma"""
    users = load_users()

    # Distingui tra Update (con message) e CallbackQuery
    if hasattr(update_or_query, 'effective_user'):
        # È un Update
        user_id = str(update_or_query.effective_user.id)
        send_message = lambda text, **kwargs: update_or_query.message.reply_text(text, **kwargs)
    else:
        # È un CallbackQuery
        user_id = str(update_or_query.from_user.id)
        send_message = lambda text, **kwargs: update_or_query.edit_message_text(text, **kwargs)

    user_data = {
        'luce_energia': context.user_data['luce_energia'],
        'luce_comm': context.user_data['luce_comm'],
    }

    if not solo_luce:
        user_data['gas_energia'] = context.user_data['gas_energia']
        user_data['gas_comm'] = context.user_data['gas_comm']
    else:
        user_data['gas_energia'] = None
        user_data['gas_comm'] = None

    users[user_id] = user_data
    save_users(users)

    messaggio = (
        "✅ <b>Abbiamo finito!</b>\n\n"
        "Ecco i dati che hai inserito:\n\n"
        f"💡 <b>Luce</b>\n"
        f"- Materia energia: {user_data['luce_energia']:.4f} €/kWh\n"
        f"- Commercializzazione: {user_data['luce_comm']:.4f} €/anno\n"
    )

    if not solo_luce:
        messaggio += (
            f"\n🔥 <b>Gas</b>\n"
            f"- Materia energia: {user_data['gas_energia']:.4f} €/Smc\n"
            f"- Commercializzazione: {user_data['gas_comm']:.4f} €/anno\n"
        )

    messaggio += (
        "\nTutto corretto?\n"
        "Se in futuro vuoi modificare qualcosa, puoi usare il comando /update.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
    )

    await send_message(messaggio, parse_mode=ParseMode.HTML)
    return ConversationHandler.END

async def cancel(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Annulla registrazione"""
    await update.message.reply_text("❌ Registrazione annullata. Usa /start per ricominciare.")
    return ConversationHandler.END

async def status(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Mostra dati salvati"""
    users = load_users()
    user_id = str(update.effective_user.id)

    if user_id not in users:
        await update.message.reply_text(
            "❌ Non hai ancora salvato i tuoi dati.\n"
            "Usa /start per registrarti."
        )
        return

    data = users[user_id]
    messaggio = (
        "📊 <b>I tuoi dati:</b>\n\n"
        f"💡 <b>Luce:</b>\n"
        f"  - Energia: €{data['luce_energia']:.4f}/kWh\n"
        f"  - Commercializzazione: €{data['luce_comm']:.4f}/anno\n"
    )

    if data.get('gas_energia') is not None:
        messaggio += (
            f"\n🔥 <b>Gas:</b>\n"
            f"  - Energia: €{data['gas_energia']:.4f}/Smc\n"
            f"  - Commercializzazione: €{data['gas_comm']:.4f}/anno\n"
        )

    messaggio += "\nPer modificarli usa /update"
    await update.message.reply_text(messaggio, parse_mode=ParseMode.HTML)

async def remove_data(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Cancella dati utente"""
    users = load_users()
    user_id = str(update.effective_user.id)

    if user_id in users:
        del users[user_id]
        save_users(users)
        await update.message.reply_text(
            "✅ <b>Dati cancellati con successo</b>\n\n"
            "Tutte le informazioni che avevi registrato (tariffe e preferenze) sono state rimosse.\n"
            "Da questo momento non riceverai più notifiche da OctoTracker.\n\n"
            "🐙 Ti ringrazio per averlo provato!\n\n"
            "Se in futuro vuoi ricominciare a monitorare le tariffe, ti basta usare il comando /start.",
            parse_mode=ParseMode.HTML
        )
    else:
        await update.message.reply_text(
            "❌ Non hai dati da cancellare.\n"
            "Usa /start per registrarti."
        )

async def help_command(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Mostra messaggio di aiuto"""
    help_text = (
        "👋 <b>Benvenuto su OctoTracker!</b>\n\n"
        "Questo bot ti aiuta a monitorare le tariffe luce e gas di Octopus Energy "
        "e ti avvisa quando ci sono offerte più convenienti rispetto alle tue.\n\n"
        "<b>Comandi disponibili:</b>\n"
        "• /start – Inizia e registra le tue tariffe attuali\n"
        "• /update – Aggiorna le tariffe che hai impostato\n"
        "• /status – Mostra le tariffe e lo stato attuale\n"
        "• /remove – Cancella i tuoi dati e disattiva il servizio\n"
        "• /help – Mostra questo messaggio di aiuto\n\n"
        f"💡 Il bot controlla le tariffe ogni giorno alle {CHECKER_HOUR}:00.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
    )
    await update.message.reply_text(help_text, parse_mode=ParseMode.HTML)

# ========== SCHEDULER ==========

async def run_scraper():
    """Esegue scraper delle tariffe"""
    print(f"🕷️  [{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}] Avvio scraper...")
    try:
        result = scrape_octopus_tariffe()
        print(f"✅ Scraper completato: {result}")
    except Exception as e:
        print(f"❌ Errore scraper: {e}")

async def run_checker(bot_token: str):
    """Esegue checker e invia notifiche"""
    print(f"🔍 [{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}] Avvio checker...")
    try:
        await check_and_notify_users(bot_token)
        print(f"✅ Checker completato")
    except Exception as e:
        print(f"❌ Errore checker: {e}")

async def daily_scheduler(bot_token: str):
    """Scheduler giornaliero per scraper e checker"""
    print(f"📅 Scheduler attivo - Scraper: {SCRAPER_HOUR}:00, Checker: {CHECKER_HOUR}:00")

    while True:
        now = datetime.now()

        # Controlla se è ora di eseguire lo scraper
        if now.hour == SCRAPER_HOUR and now.minute == 0:
            await run_scraper()
            await asyncio.sleep(60)  # Aspetta 1 minuto per evitare esecuzioni multiple

        # Controlla se è ora di eseguire il checker
        elif now.hour == CHECKER_HOUR and now.minute == 0:
            await run_checker(bot_token)
            await asyncio.sleep(60)  # Aspetta 1 minuto per evitare esecuzioni multiple

        # Controlla ogni 30 secondi
        await asyncio.sleep(30)

async def keep_alive():
    """Keep-alive per evitare che il worker vada in sleep (solo in modalità polling)"""
    # Keep-alive non necessario in modalità webhook
    if BOT_MODE == 'webhook':
        print("⏸️  Keep-alive non necessario (modalità webhook)")
        return

    if KEEPALIVE_INTERVAL <= 0:
        print("⏸️  Keep-alive disabilitato")
        return

    print(f"💓 Keep-alive attivo (ogni {KEEPALIVE_INTERVAL} minuti)")

    while True:
        await asyncio.sleep(KEEPALIVE_INTERVAL * 60)
        print(f"💓 [{datetime.now().strftime('%Y-%m-%d %H:%M:%S')}] Keep-alive ping")

# ========== MAIN ==========

async def post_init(application: Application) -> None:
    """Avvia scheduler e keep-alive dopo l'inizializzazione del bot"""
    bot_token = application.bot.token

    # Avvia scheduler in background
    asyncio.create_task(daily_scheduler(bot_token))

    # Avvia keep-alive in background
    asyncio.create_task(keep_alive())

def main():
    """Avvia il bot con scheduler integrato"""
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato")

    print("🤖 Avvio OctoTracker...")
    print(f"📡 Modalità: {BOT_MODE.upper()}")
    print(f"⏰ Scraper schedulato: {SCRAPER_HOUR}:00")
    print(f"⏰ Checker schedulato: {CHECKER_HOUR}:00")

    if BOT_MODE == 'webhook':
        print(f"🌐 Webhook URL: {WEBHOOK_URL}")
        print(f"🔌 Porta: {WEBHOOK_PORT}")
        print(f"💓 Keep-alive: non necessario (webhook)")
    else:
        print(f"💓 Keep-alive: ogni {KEEPALIVE_INTERVAL} minuti" if KEEPALIVE_INTERVAL > 0 else "💓 Keep-alive: disabilitato")

    app = Application.builder().token(token).post_init(post_init).build()

    # Handler conversazione registrazione
    conv_handler = ConversationHandler(
        entry_points=[
            CommandHandler('start', start),
            CommandHandler('update', start)
        ],
        states={
            LUCE_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_energia)],
            LUCE_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_comm)],
            HA_GAS: [CallbackQueryHandler(ha_gas)],
            GAS_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_energia)],
            GAS_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_comm)],
        },
        fallbacks=[CommandHandler('cancel', cancel)]
    )

    app.add_handler(conv_handler)
    app.add_handler(CommandHandler('status', status))
    app.add_handler(CommandHandler('remove', remove_data))
    app.add_handler(CommandHandler('help', help_command))

    print("✅ Bot configurato!")

    # Avvia in modalità webhook o polling
    if BOT_MODE == 'webhook':
        if not WEBHOOK_URL:
            raise ValueError("WEBHOOK_URL richiesto per modalità webhook")

        print(f"🚀 Avvio webhook su {WEBHOOK_URL}...")

        # Configura webhook con retry per Docker
        app.run_webhook(
            listen="0.0.0.0",
            port=WEBHOOK_PORT,
            url_path=token,  # Usa il token come path per sicurezza
            webhook_url=f"{WEBHOOK_URL}/{token}",
            secret_token=WEBHOOK_SECRET if WEBHOOK_SECRET else None,
            allowed_updates=Update.ALL_TYPES,
            drop_pending_updates=True,  # Evita messaggi vecchi
            bootstrap_retries=3  # Retry se setWebhook fallisce al primo tentativo
        )
    else:
        print("🚀 Avvio polling...")
        app.run_polling(allowed_updates=Update.ALL_TYPES)

if __name__ == '__main__':
    main()
