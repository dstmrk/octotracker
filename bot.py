#!/usr/bin/env python3
"""
Bot Telegram OctoTracker - Tutto in uno
Gestisce bot, scraper schedulato e checker schedulato
"""
import asyncio
import logging
import os
from datetime import datetime, timedelta
from enum import IntEnum
from typing import Any
from warnings import filterwarnings

from dotenv import load_dotenv
from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
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
from checker import check_and_notify_users, format_number
from constants import ERROR_VALUE_NEGATIVE
from data_reader import fetch_octopus_tariffe
from database import init_db, load_user, remove_user, save_user, user_exists
from formatters import format_utility_header

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


# Costanti messaggi (specifiche per bot)
MSG_HAS_GAS = "Hai anche una fornitura gas attiva con Octopus Energy?"
MSG_GAS_CONSUMO = "Inserisci il tuo consumo annuo di gas in Smc.\n\n💬 Esempio: 1200"


# Stati conversazione
class ConversationState(IntEnum):
    """Stati del conversation handler per registrazione tariffe"""

    TIPO_TARIFFA = 0
    LUCE_TIPO_VARIABILE = 1
    LUCE_ENERGIA = 2
    LUCE_COMM = 3
    VUOI_CONSUMI_LUCE = 7
    LUCE_CONSUMO_F1 = 8
    LUCE_CONSUMO_F2 = 9
    LUCE_CONSUMO_F3 = 10
    HA_GAS = 4
    GAS_ENERGIA = 5
    GAS_COMM = 6
    VUOI_CONSUMI_GAS = 11
    GAS_CONSUMO = 12


# Backward compatibility: mantieni le costanti per i test e il codice esistente
TIPO_TARIFFA = ConversationState.TIPO_TARIFFA
LUCE_TIPO_VARIABILE = ConversationState.LUCE_TIPO_VARIABILE
LUCE_ENERGIA = ConversationState.LUCE_ENERGIA
LUCE_COMM = ConversationState.LUCE_COMM
VUOI_CONSUMI_LUCE = ConversationState.VUOI_CONSUMI_LUCE
LUCE_CONSUMO_F1 = ConversationState.LUCE_CONSUMO_F1
LUCE_CONSUMO_F2 = ConversationState.LUCE_CONSUMO_F2
LUCE_CONSUMO_F3 = ConversationState.LUCE_CONSUMO_F3
HA_GAS = ConversationState.HA_GAS
GAS_ENERGIA = ConversationState.GAS_ENERGIA
GAS_COMM = ConversationState.GAS_COMM
VUOI_CONSUMI_GAS = ConversationState.VUOI_CONSUMI_GAS
GAS_CONSUMO = ConversationState.GAS_CONSUMO

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

# ========== BOT COMMANDS ==========


async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Avvia registrazione tariffe"""
    user_id = str(update.effective_user.id)
    is_update = user_exists(user_id)

    # Reset context per nuova conversazione
    context.user_data.clear()

    keyboard = [
        [
            InlineKeyboardButton("📊 Fissa", callback_data="tipo_fissa"),
            InlineKeyboardButton("📈 Variabile", callback_data="tipo_variabile"),
        ]
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)

    if is_update:
        messaggio = (
            "♻️ <b>Aggiorniamo le tue tariffe!</b>\n\n"
            "Inserisci di nuovo i valori attuali così OctoTracker potrà confrontarli "
            "con le nuove offerte di Octopus Energy.\n\n"
            "Ti guiderò passo passo come la prima volta: prima la luce, poi (se ce l'hai) il gas.\n\n"
            "👉 Iniziamo: che tipo di tariffa hai?"
        )
    else:
        messaggio = (
            "🐙 <b>Benvenuto su OctoTracker!</b>\n\n"
            "Questo bot controlla ogni giorno le tariffe di Octopus Energy e ti avvisa "
            "se ne trova di più convenienti rispetto alle tue attuali.\n\n"
            "Ti farò qualche semplice domanda per registrare le tue tariffe luce e (se ce l'hai) gas.\n"
            "Rispondi passo passo ai messaggi: ci vorrà meno di un minuto. ⚡️\n\n"
            "👉 Iniziamo: che tipo di tariffa hai?"
        )

    await update.message.reply_text(messaggio, reply_markup=reply_markup, parse_mode=ParseMode.HTML)
    return TIPO_TARIFFA


async def tipo_tariffa(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci scelta tipo tariffa (Fissa/Variabile)"""
    query = update.callback_query
    await query.answer()

    if query.data == "tipo_fissa":
        context.user_data["is_variabile"] = False
        context.user_data["luce_tipo"] = "fissa"
        context.user_data["luce_fascia"] = "monoraria"
        context.user_data["gas_tipo"] = "fissa"  # Se ha gas, sarà fissa
        context.user_data["gas_fascia"] = "monoraria"

        await query.edit_message_text(
            "📊 <b>Tariffa Fissa</b>\n\n"
            "Perfetto! Ora inserisci i dati della tua tariffa luce.\n\n"
            "👉 Quanto paghi per la materia energia luce (€/kWh)?\n\n"
            "💬 Esempio: 0,145",
            parse_mode=ParseMode.HTML,
        )
        return LUCE_ENERGIA

    else:  # tipo_variabile
        context.user_data["is_variabile"] = True

        keyboard = [
            [
                InlineKeyboardButton("⏱️ Monoraria", callback_data="luce_mono"),
                InlineKeyboardButton("⏱️⏱️⏱️ Trioraria", callback_data="luce_tri"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await query.edit_message_text(
            "📈 <b>Tariffa Variabile</b>\n\n"
            "La tua tariffa luce è monoraria o trioraria (F1/F2/F3)?",
            reply_markup=reply_markup,
            parse_mode=ParseMode.HTML,
        )
        return LUCE_TIPO_VARIABILE


async def luce_tipo_variabile(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci scelta tipo luce variabile (Monoraria/Trioraria)"""
    query = update.callback_query
    await query.answer()

    if query.data == "luce_mono":
        context.user_data["luce_tipo"] = "variabile"
        context.user_data["luce_fascia"] = "monoraria"
        tipo_msg = "monoraria (PUN)"
    else:  # luce_tri
        context.user_data["luce_tipo"] = "variabile"
        context.user_data["luce_fascia"] = "trioraria"
        tipo_msg = "trioraria (PUN)"

    # Gas variabile è sempre monorario
    context.user_data["gas_tipo"] = "variabile"
    context.user_data["gas_fascia"] = "monoraria"

    await query.edit_message_text(
        f"⚡ <b>Luce variabile {tipo_msg}</b>\n\n"
        f"Ora inserisci lo spread della tua tariffa rispetto al PUN.\n\n"
        f"💬 Esempio: se la tua tariffa è <b>PUN + 0,0088</b> €/kWh, scrivi <code>0,0088</code>",
        parse_mode=ParseMode.HTML,
    )
    return LUCE_ENERGIA


async def luce_energia(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo energia luce (spread o prezzo fisso)"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return LUCE_ENERGIA

        context.user_data["luce_energia"] = value
        await update.message.reply_text(
            "Perfetto! Ora indica il costo di commercializzazione luce, in euro/anno.\n\n"
            "💬 Esempio: 72 (se paghi 6 €/mese)"
        )
        return LUCE_COMM
    except ValueError:
        is_variabile = context.user_data.get("is_variabile", False)
        if is_variabile:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,0088)")
        else:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,145)")
        return LUCE_ENERGIA


async def luce_comm(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo commercializzazione luce e chiedi se vuole inserire consumi"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return LUCE_COMM

        context.user_data["luce_comm"] = value

        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="consumi_luce_si"),
                InlineKeyboardButton("❌ No", callback_data="consumi_luce_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await update.message.reply_text(
            "Vuoi indicare anche il tuo consumo annuale di energia elettrica (in kWh)?\n\n"
            "💡 Serve solo per valutare meglio quando una tariffa può convenirti.",
            reply_markup=reply_markup,
        )
        return VUOI_CONSUMI_LUCE
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 96.50)")
        return LUCE_COMM


async def vuoi_consumi_luce(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se vuole inserire consumi luce"""
    query = update.callback_query
    await query.answer()

    if query.data == "consumi_luce_si":
        # Inizia raccolta consumi in base alla fascia
        luce_fascia = context.user_data.get("luce_fascia", "monoraria")

        if luce_fascia == "monoraria":
            messaggio = (
                "Inserisci il tuo consumo annuo totale di energia elettrica in kWh.\n\n"
                "💬 Esempio: 2700"
            )
        elif luce_fascia == "trioraria":
            messaggio = (
                "Inserisci il tuo consumo annuo in fascia F1 in kWh.\n\n"
                "(F1 = feriali 8–19)\n\n"
                "💬 Esempio: 900"
            )
        else:
            # Fallback (non dovrebbe mai succedere)
            messaggio = "Inserisci il tuo consumo annuo in kWh."

        await query.edit_message_text(messaggio, parse_mode=ParseMode.HTML)
        return LUCE_CONSUMO_F1
    else:
        # Non vuole inserire consumi, vai direttamente a chiedere se ha gas
        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)
        await query.edit_message_text(MSG_HAS_GAS, reply_markup=reply_markup)
        return HA_GAS


async def luce_consumo_f1(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F1 luce e procedi in base alla fascia"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return LUCE_CONSUMO_F1

        context.user_data["luce_consumo_f1"] = value

        # Se monoraria, abbiamo finito con i consumi luce → vai a HA_GAS
        luce_fascia = context.user_data.get("luce_fascia", "monoraria")
        if luce_fascia == "monoraria":
            keyboard = [
                [
                    InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                    InlineKeyboardButton("❌ No", callback_data="gas_no"),
                ]
            ]
            reply_markup = InlineKeyboardMarkup(keyboard)
            await update.message.reply_text(MSG_HAS_GAS, reply_markup=reply_markup)
            return HA_GAS

        # Se trioraria, chiedi F2
        await update.message.reply_text(
            "Ora inserisci il tuo consumo annuo in fascia F2 in kWh.\n\n"
            "(F2 = feriali 7–8 e 19–23, sabato 7–23)\n\n"
            "💬 Esempio: 900"
        )
        return LUCE_CONSUMO_F2

    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 2700)")
        return LUCE_CONSUMO_F1


async def luce_consumo_f2(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F2 luce (solo trioraria)"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return LUCE_CONSUMO_F2

        context.user_data["luce_consumo_f2"] = value

        # Chiedi F3
        await update.message.reply_text(
            "Infine, inserisci il tuo consumo annuo in fascia F3 in kWh.\n\n"
            "(F3 = notte, domeniche e festivi)\n\n"
            "💬 Esempio: 900"
        )
        return LUCE_CONSUMO_F3

    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 900)")
        return LUCE_CONSUMO_F2


async def luce_consumo_f3(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F3 luce (solo trioraria) e vai a HA_GAS"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return LUCE_CONSUMO_F3

        context.user_data["luce_consumo_f3"] = value

        # Consumi luce completati, vai a chiedere se ha gas
        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)
        await update.message.reply_text(MSG_HAS_GAS, reply_markup=reply_markup)
        return HA_GAS

    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 900)")
        return LUCE_CONSUMO_F3


async def ha_gas(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se ha gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "gas_si":
        is_variabile = context.user_data.get("is_variabile", False)

        if is_variabile:
            msg = (
                "🔥 <b>Gas variabile</b>\n\n"
                "Ora inserisci lo spread della tua tariffa rispetto al PSV.\n\n"
                "💬 Esempio: se la tua tariffa è <b>PSV + 0,08</b> €/Smc, scrivi <code>0,08</code>"
            )
        else:
            msg = (
                "🔥 <b>Gas fisso</b>\n\n"
                "Perfetto! Inserisci il costo materia energia gas (€/Smc).\n\n"
                "💬 Esempio: 0,456"
            )

        await query.edit_message_text(msg, parse_mode=ParseMode.HTML)
        return GAS_ENERGIA
    else:
        return await salva_e_conferma(query, context, solo_luce=True)


async def gas_energia(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo energia gas (spread o prezzo fisso)"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return GAS_ENERGIA

        context.user_data["gas_energia"] = value
        await update.message.reply_text(
            "Perfetto! Ora indica il costo di commercializzazione gas, in euro/anno.\n\n"
            "💬 Esempio: 84 (se paghi 7 €/mese)"
        )
        return GAS_COMM
    except ValueError:
        is_variabile = context.user_data.get("is_variabile", False)
        if is_variabile:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,08)")
        else:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,456)")
        return GAS_ENERGIA


async def gas_comm(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva commercializzazione gas e chiedi se vuole inserire consumi"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return GAS_COMM

        context.user_data["gas_comm"] = value

        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="consumi_gas_si"),
                InlineKeyboardButton("❌ No", callback_data="consumi_gas_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await update.message.reply_text(
            "Vuoi indicare anche il tuo consumo annuale di gas (in Smc)?\n\n"
            "🔥 Serve solo per valutare meglio quando una tariffa può convenirti.",
            reply_markup=reply_markup,
        )
        return VUOI_CONSUMI_GAS
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 144.00)")
        return GAS_COMM


async def vuoi_consumi_gas(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se vuole inserire consumi gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "consumi_gas_si":
        # Chiedi consumo gas
        await query.edit_message_text(MSG_GAS_CONSUMO)
        return GAS_CONSUMO
    else:
        # Non vuole inserire consumi, salva e conferma
        return await salva_e_conferma(query, context, solo_luce=False)


async def gas_consumo(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo gas e vai a conferma"""
    try:
        value = float(update.message.text.replace(",", "."))
        if value < 0:
            await update.message.reply_text(ERROR_VALUE_NEGATIVE)
            return GAS_CONSUMO

        context.user_data["gas_consumo_annuo"] = value

        # Consumi completati, salva e conferma
        return await salva_e_conferma(update, context, solo_luce=False)

    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 1200)")
        return GAS_CONSUMO


def _build_user_data(context: ContextTypes.DEFAULT_TYPE, solo_luce: bool) -> dict[str, Any]:
    """
    Costruisce la struttura dati utente da salvare nel database.

    Args:
        context: Context del conversation handler
        solo_luce: True se l'utente ha solo luce, False se ha anche gas

    Returns:
        Dizionario con struttura nested luce/gas
    """
    user_data = {
        "luce": {
            "tipo": context.user_data["luce_tipo"],
            "fascia": context.user_data["luce_fascia"],
            "energia": context.user_data["luce_energia"],
            "commercializzazione": context.user_data["luce_comm"],
        }
    }

    # Aggiungi consumi luce se presenti
    if "luce_consumo_f1" in context.user_data:
        user_data["luce"]["consumo_f1"] = context.user_data["luce_consumo_f1"]
    if "luce_consumo_f2" in context.user_data:
        user_data["luce"]["consumo_f2"] = context.user_data["luce_consumo_f2"]
    if "luce_consumo_f3" in context.user_data:
        user_data["luce"]["consumo_f3"] = context.user_data["luce_consumo_f3"]

    if not solo_luce:
        user_data["gas"] = {
            "tipo": context.user_data["gas_tipo"],
            "fascia": context.user_data["gas_fascia"],
            "energia": context.user_data["gas_energia"],
            "commercializzazione": context.user_data["gas_comm"],
        }
        # Aggiungi consumo gas se presente
        if "gas_consumo_annuo" in context.user_data:
            user_data["gas"]["consumo_annuo"] = context.user_data["gas_consumo_annuo"]
    else:
        user_data["gas"] = None

    return user_data


def _format_confirmation_message(user_data: dict[str, Any]) -> str:
    """
    Formatta il messaggio di conferma con i dati inseriti dall'utente.

    Args:
        user_data: Dizionario con struttura nested luce/gas

    Returns:
        Messaggio HTML formattato per Telegram
    """
    # Formatta numeri luce
    luce_energia_fmt = format_number(user_data["luce"]["energia"], max_decimals=4)
    luce_comm_fmt = format_number(user_data["luce"]["commercializzazione"], max_decimals=2)

    # Formatta header luce
    tipo_display, luce_label, luce_unit = format_utility_header("luce", user_data["luce"], "💡")

    messaggio = (
        "✅ <b>Abbiamo finito!</b>\n\n"
        "Ecco i dati che hai inserito:\n\n"
        f"💡 <b>Luce ({tipo_display})</b>\n"
        f"- {luce_label}: {luce_energia_fmt} {luce_unit}\n"
        f"- Commercializzazione: {luce_comm_fmt} €/anno\n"
    )

    # Aggiungi consumi luce se presenti
    consumo_f1 = user_data["luce"].get("consumo_f1")
    if consumo_f1 is not None:
        consumo_f2 = user_data["luce"].get("consumo_f2")
        consumo_f3 = user_data["luce"].get("consumo_f3")
        luce_fascia = user_data["luce"]["fascia"]

        if luce_fascia == "monoraria":
            messaggio += f"- Consumo: <b>{format_number(consumo_f1, max_decimals=0)}</b> kWh/anno\n"

        elif luce_fascia == "bioraria":
            totale = consumo_f1 + consumo_f2
            messaggio += (
                f"- Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F23: {format_number(consumo_f2, max_decimals=0)} kWh\n"
            )

        elif luce_fascia == "trioraria":
            totale = consumo_f1 + consumo_f2 + consumo_f3
            messaggio += (
                f"- Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F2: {format_number(consumo_f2, max_decimals=0)} kWh | "
                f"F3: {format_number(consumo_f3, max_decimals=0)} kWh\n"
            )

    # Aggiungi sezione gas se presente
    if user_data["gas"] is not None:
        gas_energia_fmt = format_number(user_data["gas"]["energia"], max_decimals=4)
        gas_comm_fmt = format_number(user_data["gas"]["commercializzazione"], max_decimals=2)

        # Formatta header gas
        tipo_display_gas, gas_label, gas_unit = format_utility_header("gas", user_data["gas"], "🔥")

        messaggio += (
            f"\n🔥 <b>Gas ({tipo_display_gas})</b>\n"
            f"- {gas_label}: {gas_energia_fmt} {gas_unit}\n"
            f"- Commercializzazione: {gas_comm_fmt} €/anno\n"
        )

        # Aggiungi consumo gas se presente
        consumo_gas = user_data["gas"].get("consumo_annuo")
        if consumo_gas is not None:
            messaggio += (
                f"- Consumo: <b>{format_number(consumo_gas, max_decimals=0)}</b> Smc/anno\n"
            )

    # Aggiungi footer
    messaggio += (
        "\nTutto corretto?\n"
        "Se in futuro vuoi modificare qualcosa, puoi usare il comando /update.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
    )

    return messaggio


async def salva_e_conferma(
    update_or_query: Update | Any, context: ContextTypes.DEFAULT_TYPE, solo_luce: bool
) -> int:
    """Salva dati utente e mostra conferma"""
    # Distingui tra Update (con message) e CallbackQuery
    if hasattr(update_or_query, "effective_user"):
        # È un Update
        user_id = str(update_or_query.effective_user.id)
        send_message = lambda text, **kwargs: update_or_query.message.reply_text(text, **kwargs)
    else:
        # È un CallbackQuery
        user_id = str(update_or_query.from_user.id)
        send_message = lambda text, **kwargs: update_or_query.edit_message_text(text, **kwargs)

    # Costruisci struttura dati utente
    user_data = _build_user_data(context, solo_luce)

    # Salva nel database
    save_user(user_id, user_data)

    # Formatta messaggio di conferma
    messaggio = _format_confirmation_message(user_data)

    await send_message(messaggio, parse_mode=ParseMode.HTML)
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
    tipo_display, luce_label, luce_unit = format_utility_header("luce", data["luce"], "💡")

    messaggio = (
        "📊 <b>I tuoi dati:</b>\n\n"
        f"💡 <b>Luce ({tipo_display}):</b>\n"
        f"  - {luce_label}: {luce_energia_fmt} {luce_unit}\n"
        f"  - Commercializzazione: {luce_comm_fmt} €/anno\n"
    )

    # Mostra consumi luce se presenti
    consumo_f1 = data["luce"].get("consumo_f1")
    if consumo_f1 is not None:
        consumo_f2 = data["luce"].get("consumo_f2")
        consumo_f3 = data["luce"].get("consumo_f3")
        luce_fascia = data["luce"]["fascia"]

        if luce_fascia == "monoraria":
            # Solo totale
            messaggio += (
                f"  - Consumo: <b>{format_number(consumo_f1, max_decimals=0)}</b> kWh/anno\n"
            )

        elif luce_fascia == "bioraria":
            # Totale + breakdown F1 e F23
            totale = consumo_f1 + consumo_f2
            messaggio += (
                f"  - Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F23: {format_number(consumo_f2, max_decimals=0)} kWh\n"
            )

        elif luce_fascia == "trioraria":
            # Totale + breakdown F1, F2, F3
            totale = consumo_f1 + consumo_f2 + consumo_f3
            messaggio += (
                f"  - Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F2: {format_number(consumo_f2, max_decimals=0)} kWh | "
                f"F3: {format_number(consumo_f3, max_decimals=0)} kWh\n"
            )

    if data.get("gas") is not None:
        gas_energia_fmt = format_number(data["gas"]["energia"], max_decimals=4)
        gas_comm_fmt = format_number(data["gas"]["commercializzazione"], max_decimals=2)

        # Formatta header gas
        tipo_display_gas, gas_label, gas_unit = format_utility_header("gas", data["gas"], "🔥")

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

    help_text = (
        "👋 <b>Benvenuto su OctoTracker!</b>\n\n"
        "Questo bot ti aiuta a monitorare le tariffe luce e gas di Octopus Energy "
        "e ti avvisa quando ci sono offerte più convenienti rispetto alle tue.\n\n"
        "<b>Comandi disponibili:</b>\n"
        "• /start – Inizia e registra le tue tariffe attuali\n"
        "• /update – Aggiorna le tariffe che hai impostato\n"
        "• /status – Mostra le tariffe e lo stato attuale\n"
        "• /remove – Cancella i tuoi dati e disattiva il servizio\n"
        "• /cancel – Annulla la registrazione in corso\n"
        "• /help – Mostra questo messaggio di aiuto\n\n"
        f"💡 Il bot controlla le tariffe ogni giorno alle {CHECKER_HOUR}:00.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
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
