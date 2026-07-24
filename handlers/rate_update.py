#!/usr/bin/env python3
"""
Handler per aggiornamento tariffe via pulsanti inline nelle notifiche.

Gestisce i callback dei pulsanti "Aggiorna tariffe" e "No grazie" mostrati
nelle notifiche di tariffe migliori.

Quando l'utente adotta una nuova offerta a prezzo fisso, la vecchia scadenza
viene azzerata (l'offerta è cambiata) e viene proposto — tramite una breve
conversazione — di registrare la nuova data di attivazione.
"""

import logging
from enum import IntEnum

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.constants import ParseMode
from telegram.ext import ContextTypes, ConversationHandler

from database import (
    apply_pending_rates,
    clear_pending_rates,
    load_pending_rates,
    update_user_scadenza,
)
from date_utils import compute_expiry_date, format_date_display, parse_activation_date
from handlers import safe_answer_callback

# Setup logger
logger = logging.getLogger(__name__)

# Testo che viene sostituito nel messaggio dopo la scelta dell'utente
PROMPT_TEXT = "👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?"
CONFIRMED_TEXT = "✅ Tariffe aggiornate!"
DECLINED_TEXT = "🔧 Puoi sempre aggiornare le tariffe con /update."

# Messaggi conversazione scadenza post-aggiornamento
MSG_DATE_INVALID = "❌ Data non valida. Usa il formato GG/MM/AAAA (es. 15/03/2025)"
SCADENZA_ASK_TEXT = (
    "📅 Hai adottato una nuova offerta a <b>prezzo fisso</b>: la scadenza precedente "
    "non è più valida.\n\nVuoi registrare la nuova data di scadenza per ricevere il "
    "promemoria un mese prima?"
)
SCADENZA_DECLINED_TEXT = (
    "👍 Ok! Se cambi idea puoi impostare la scadenza in qualsiasi momento con /update."
)
SCADENZA_DONE_TEXT = "🔔 Perfetto, ti avviserò un mese prima della scadenza!"

# Callback data
CALLBACK_YES = "rate_update_yes"
CALLBACK_NO = "rate_update_no"
CALLBACK_SCADENZA_YES = "scadenza_update_yes"
CALLBACK_SCADENZA_NO = "scadenza_update_no"
CALLBACK_SCADENZA_SKIP = "skip_scadenza_update"

# Chiave in user_data con la coda dei servizi fissi per cui chiedere la data
_SERVICES_KEY = "scadenza_update_services"


class ScadenzaUpdateState(IntEnum):
    """Stati della conversazione per impostare la scadenza dopo un aggiornamento"""

    ASK = 0
    DATE = 1


ASK_SCADENZA = ScadenzaUpdateState.ASK
UPDATE_DATE = ScadenzaUpdateState.DATE


# ========== HELPER ==========


def _updated_fissa_services(pending: dict | None) -> list[str]:
    """Restituisce i servizi fissi effettivamente aggiornati dall'offerta.

    Args:
        pending: Le tariffe pendenti (con eventuale chiave updated_services)

    Returns:
        Lista ordinata dei servizi ("luce", "gas") aggiornati e a prezzo fisso
    """
    if not pending:
        return []

    updated = pending.get("updated_services", [])
    return [
        servizio
        for servizio in ("luce", "gas")
        if servizio in updated and pending.get(servizio, {}).get("tipo") == "fissa"
    ]


def _ask_scadenza_keyboard() -> InlineKeyboardMarkup:
    """Tastiera Sì/No per la domanda sulla scadenza"""
    return InlineKeyboardMarkup(
        [
            [
                InlineKeyboardButton("✅ Sì", callback_data=CALLBACK_SCADENZA_YES),
                InlineKeyboardButton("❌ No", callback_data=CALLBACK_SCADENZA_NO),
            ]
        ]
    )


def _date_prompt(servizio: str) -> tuple[str, InlineKeyboardMarkup]:
    """Testo e tastiera per chiedere la data di attivazione di un servizio"""
    keyboard = InlineKeyboardMarkup(
        [[InlineKeyboardButton("⏭️ Salta", callback_data=CALLBACK_SCADENZA_SKIP)]]
    )
    text = (
        f"📅 Inserisci la <b>data di attivazione</b> della tua nuova offerta {servizio}.\n\n"
        "Calcolerò la scadenza a 12 mesi e ti avviserò un mese prima.\n\n"
        "💬 Formato GG/MM/AAAA (es. 15/03/2025), oppure premi «Salta»."
    )
    return text, keyboard


async def _prompt_next_service(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Chiede la data per il prossimo servizio in coda, o termina se non ce ne sono.

    Returns:
        UPDATE_DATE se resta un servizio da processare, ConversationHandler.END altrimenti
    """
    services = context.user_data.get(_SERVICES_KEY, [])
    if not services:
        context.user_data.pop(_SERVICES_KEY, None)
        await update.effective_message.reply_text(SCADENZA_DONE_TEXT, parse_mode=ParseMode.HTML)
        return ConversationHandler.END

    text, keyboard = _date_prompt(services[0])
    await update.effective_message.reply_text(
        text, reply_markup=keyboard, parse_mode=ParseMode.HTML
    )
    return UPDATE_DATE


# ========== HANDLERS ==========


async def rate_update_yes(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Callback quando l'utente clicca 'Aggiorna tariffe'.

    Applica le tariffe pendenti; se ha adottato un'offerta fissa avvia la
    conversazione per registrare la nuova scadenza.
    """
    query = update.callback_query
    await query.answer()

    user_id = str(update.effective_user.id)

    # Ispeziona le pending prima di applicarle (apply le rimuove)
    pending = load_pending_rates(user_id)

    # Applica tariffe pendenti in modo atomico (singola transazione DB)
    success, reason = apply_pending_rates(user_id)

    if success:
        new_text = query.message.text_html.replace(PROMPT_TEXT, CONFIRMED_TEXT)
    elif reason == "no_pending":
        logger.warning(f"⚠️ Utente {user_id}: nessuna tariffa pendente trovata")
        new_text = query.message.text_html.replace(PROMPT_TEXT, DECLINED_TEXT)
    elif reason == "no_user":
        logger.error(f"❌ Utente {user_id}: non trovato nel database")
        new_text = query.message.text_html.replace(PROMPT_TEXT, DECLINED_TEXT)
    else:
        logger.error(f"❌ Utente {user_id}: errore aggiornamento tariffe ({reason})")
        new_text = query.message.text_html.replace(
            PROMPT_TEXT, "❌ Errore nell'aggiornamento. Riprova con /update."
        )

    await query.edit_message_text(text=new_text, parse_mode=ParseMode.HTML)

    # Se ha adottato una o più offerte fisse, proponi di registrare la scadenza
    if success:
        fissa_services = _updated_fissa_services(pending)
        if fissa_services:
            context.user_data[_SERVICES_KEY] = fissa_services
            await query.message.reply_text(
                SCADENZA_ASK_TEXT,
                reply_markup=_ask_scadenza_keyboard(),
                parse_mode=ParseMode.HTML,
            )
            return ASK_SCADENZA

    return ConversationHandler.END


async def rate_update_no(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Callback quando l'utente clicca 'No grazie'"""
    query = update.callback_query
    await query.answer()

    user_id = str(update.effective_user.id)
    logger.info(f"ℹ️ Utente {user_id}: ha rifiutato aggiornamento tariffe via bottone")

    # Rimuovi tariffe pendenti
    clear_pending_rates(user_id)

    # Aggiorna messaggio rimuovendo i pulsanti
    new_text = query.message.text_html.replace(PROMPT_TEXT, DECLINED_TEXT)
    await query.edit_message_text(text=new_text, parse_mode=ParseMode.HTML)


async def scadenza_update_choice(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisce la risposta Sì/No alla proposta di registrare la scadenza"""
    query = update.callback_query
    await safe_answer_callback(query)

    if query.data == CALLBACK_SCADENZA_NO:
        context.user_data.pop(_SERVICES_KEY, None)
        await query.edit_message_text(SCADENZA_DECLINED_TEXT, parse_mode=ParseMode.HTML)
        return ConversationHandler.END

    # Sì → chiedi la data per il primo servizio (rimuove i bottoni Sì/No)
    services = context.user_data.get(_SERVICES_KEY, [])
    if not services:
        return ConversationHandler.END

    text, keyboard = _date_prompt(services[0])
    await query.edit_message_text(text, reply_markup=keyboard, parse_mode=ParseMode.HTML)
    return UPDATE_DATE


async def update_scadenza_date(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Riceve la data di attivazione e salva la scadenza del servizio corrente"""
    if update.message is None or update.message.text is None:
        return UPDATE_DATE

    services = context.user_data.get(_SERVICES_KEY, [])
    if not services:
        return ConversationHandler.END

    activation = parse_activation_date(update.message.text)
    if activation is None:
        await update.message.reply_text(MSG_DATE_INVALID)
        return UPDATE_DATE

    servizio = services[0]
    scadenza = compute_expiry_date(activation)
    update_user_scadenza(str(update.effective_user.id), servizio, scadenza.isoformat())

    await update.message.reply_text(
        f"✅ Offerta {servizio}: scadenza impostata al <b>{format_date_display(scadenza)}</b>.",
        parse_mode=ParseMode.HTML,
    )

    # Passa al servizio successivo (se presente)
    services.pop(0)
    context.user_data[_SERVICES_KEY] = services
    return await _prompt_next_service(update, context)


async def skip_scadenza_date(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salta la data del servizio corrente e passa eventualmente al successivo"""
    query = update.callback_query
    await safe_answer_callback(query)

    services = context.user_data.get(_SERVICES_KEY, [])
    if services:
        services.pop(0)
        context.user_data[_SERVICES_KEY] = services

    if services:
        return await _prompt_next_service(update, context)

    context.user_data.pop(_SERVICES_KEY, None)
    await query.edit_message_text(SCADENZA_DECLINED_TEXT, parse_mode=ParseMode.HTML)
    return ConversationHandler.END
