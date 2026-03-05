#!/usr/bin/env python3
"""
Comandi utility per OctoTracker
Gestisce comandi semplici (status, remove, help, cancel, unknown)
"""

import logging
import os

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update, WebAppInfo
from telegram.constants import ParseMode
from telegram.ext import ContextTypes, ConversationHandler

from database import load_user, remove_user, user_exists
from formatters import format_luce_consumption, format_number, format_utility_header

# Setup logger
logger = logging.getLogger(__name__)

# Leggi configurazione per help (evita import circolari)
CHECKER_HOUR = int(os.getenv("CHECKER_HOUR", "10"))

# URL della Mini App per grafici (es. https://octotracker.example.com/app/)
WEBAPP_URL = os.getenv("WEBAPP_URL", "")


# ========== BOT COMMANDS ==========


async def history_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Apre la Mini App con i grafici dello storico tariffe"""
    user_id = str(update.effective_user.id)
    logger.info(f"User {user_id}: /history command")

    # Pulisci eventuali dati di conversazione in corso
    context.user_data.clear()

    # Verifica che l'utente abbia tariffe registrate
    user_data = load_user(user_id)
    if not user_data:
        await update.message.reply_text(
            "ℹ️ Non hai ancora registrato le tue tariffe.\n\n"
            "Per visualizzare i grafici dello storico tariffe, "
            "devi prima inserire i tuoi dati con il comando /start.\n\n"
            "🐙 Ti guiderò passo passo: ci vogliono meno di 60 secondi!"
        )
        return ConversationHandler.END

    if not WEBAPP_URL:
        await update.message.reply_text(
            "La Mini App non è ancora configurata.\nContatta l'amministratore del bot.",
        )
        return ConversationHandler.END

    # Crea bottone che apre la Mini App
    keyboard = InlineKeyboardMarkup(
        [
            [
                InlineKeyboardButton(
                    text="📊 Apri Grafici Tariffe",
                    web_app=WebAppInfo(url=WEBAPP_URL),
                )
            ]
        ]
    )

    await update.message.reply_text(
        "📈 <b>Storico Tariffe</b>\n\n"
        "Clicca il pulsante qui sotto per aprire i grafici interattivi "
        "con l'andamento delle tariffe Octopus Energy.\n\n"
        "Puoi filtrare per:\n"
        "• Servizio (Luce/Gas)\n"
        "• Tipo tariffa (Fissa/Variabile)\n"
        "• Fascia oraria\n"
        "• Periodo (7, 30, 90, 365 giorni)",
        parse_mode=ParseMode.HTML,
        reply_markup=keyboard,
    )
    return ConversationHandler.END


async def status(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Mostra dati salvati"""
    user_id = str(update.effective_user.id)

    # Pulisci eventuali dati di conversazione in corso
    context.user_data.clear()

    data = load_user(user_id)

    if not data:
        await update.message.reply_text(
            "ℹ️ Non hai ancora registrato le tue tariffe.\n\n"
            "Per iniziare a usare OctoTracker, inserisci i tuoi dati con il comando /start.\n\n"
            "🐙 Ti guiderò passo passo: ci vogliono meno di 60 secondi!"
        )
        return ConversationHandler.END

    # Formatta numeri rimuovendo zeri trailing
    luce_energia_fmt = format_number(data["luce"]["energia"], max_decimals=4)
    luce_comm_fmt = format_number(data["luce"]["commercializzazione"], max_decimals=2)

    # Formatta header luce
    tipo_display, luce_label, luce_unit = format_utility_header("luce", data["luce"])

    messaggio = (
        "📊 <b>I tuoi dati:</b>\n\n"
        f"💡 <b>Luce ({tipo_display}):</b>\n"
        f"  - {luce_label}: {luce_energia_fmt} {luce_unit}\n"
        f"  - Commercializzazione: {luce_comm_fmt} €/anno\n"
    )

    # Mostra consumi luce se presenti
    messaggio += format_luce_consumption(data["luce"], prefix="  - ")

    if data.get("gas") is not None:
        gas_energia_fmt = format_number(data["gas"]["energia"], max_decimals=4)
        gas_comm_fmt = format_number(data["gas"]["commercializzazione"], max_decimals=2)

        # Formatta header gas
        tipo_display_gas, gas_label, gas_unit = format_utility_header("gas", data["gas"])

        messaggio += (
            f"\n🔥 <b>Gas ({tipo_display_gas}):</b>\n"
            f"  - {gas_label}: {gas_energia_fmt} {gas_unit}\n"
            f"  - Commercializzazione: {gas_comm_fmt} €/anno\n"
        )

        # Mostra consumo gas se presente
        consumo_gas = data["gas"].get("consumo_annuo")
        if consumo_gas is not None:
            messaggio += (
                f"  - Consumo: <b>{format_number(consumo_gas, max_decimals=0)}</b> Smc/anno\n"
            )

    messaggio += "\nPer modificarli usa /update"
    await update.message.reply_text(messaggio, parse_mode=ParseMode.HTML)
    return ConversationHandler.END


async def remove_data(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Cancella dati utente"""
    user_id = str(update.effective_user.id)

    # Pulisci eventuali dati di conversazione in corso
    context.user_data.clear()

    if user_exists(user_id):
        remove_user(user_id)
        await update.message.reply_text(
            "✅ <b>Dati cancellati con successo</b>\n\n"
            "Tutte le informazioni che avevi registrato (tariffe e preferenze) sono state rimosse.\n"
            "Da questo momento non riceverai più notifiche da OctoTracker.\n\n"
            "🐙 Ti ringrazio per averlo provato!\n\n"
            "Se in futuro vuoi ricominciare a monitorare le tariffe, ti basta usare il comando /start.",
            parse_mode=ParseMode.HTML,
        )
    else:
        await update.message.reply_text(
            "ℹ️ Non hai ancora registrato le tue tariffe.\n\n"
            "Per iniziare a usare OctoTracker, inserisci i tuoi dati con il comando /start.\n\n"
            "🐙 Ti guiderò passo passo: ci vogliono meno di 60 secondi!"
        )

    return ConversationHandler.END


async def help_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Mostra messaggio di aiuto"""
    # Pulisci eventuali dati di conversazione in corso
    context.user_data.clear()

    # Link canale Telegram (se configurato)
    telegram_channel = os.getenv("TELEGRAM_CHANNEL", "").strip()
    channel_info = ""
    if telegram_channel:
        # Rimuovi @ iniziale per URL Telegram (https://t.me/ richiede solo il nome)
        channel_name = telegram_channel.lstrip("@")
        channel_info = (
            f"Per avere aggiornamenti sulle nuove funzionalità, iscriviti al canale "
            f'<a href="https://t.me/{channel_name}">@{channel_name}</a>!\n\n'
        )

    help_text = (
        "👋 <b>Benvenuto su OctoTracker!</b>\n\n"
        "Questo bot ti aiuta a monitorare le tariffe luce e gas di Octopus Energy "
        "e ti avvisa quando ci sono offerte più convenienti rispetto alle tue.\n\n"
        "<b>Comandi disponibili:</b>\n"
        "• /start – Inizia e registra le tue tariffe attuali\n"
        "• /update – Aggiorna le tariffe che hai impostato\n"
        "• /status – Mostra le tariffe e lo stato attuale\n"
        "• /history – Visualizza i grafici dello storico tariffe\n"
        "• /remove – Cancella i tuoi dati e disattiva il servizio\n"
        "• /feedback – Invia un feedback per migliorare il bot\n"
        "• /cancel – Annulla la registrazione in corso\n"
        "• /help – Mostra questo messaggio di aiuto\n\n"
        f"💡 Il bot controlla le tariffe ogni giorno alle {CHECKER_HOUR}:00.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy.\n\n"
        f"{channel_info}"
    )
    await update.message.reply_text(help_text, parse_mode=ParseMode.HTML)
    return ConversationHandler.END


async def cancel_conversation(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Annulla la conversazione in corso e resetta lo stato"""
    await update.message.reply_text(
        "❌ <b>Registrazione annullata</b>\n\n"
        "Nessun problema! Hai annullato la procedura di registrazione.\n\n"
        "Quando vuoi riprovarci, usa il comando /start.\n"
        "Se hai bisogno di aiuto, puoi usare /help.",
        parse_mode=ParseMode.HTML,
    )
    # Pulisci i dati della conversazione
    context.user_data.clear()
    return ConversationHandler.END


async def unknown_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Gestisce comandi non riconosciuti"""
    await update.message.reply_text(
        "Comando non riconosciuto 🤷‍♂️\n"
        "Dai un'occhiata a /help per vedere cosa puoi fare con OctoTracker."
    )
