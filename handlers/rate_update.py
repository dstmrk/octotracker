#!/usr/bin/env python3
"""
Handler per aggiornamento tariffe via pulsanti inline nelle notifiche.

Gestisce i callback dei pulsanti "Aggiorna tariffe" e "No grazie"
mostrati nelle notifiche di tariffe migliori.
"""

import logging

from telegram import Update
from telegram.constants import ParseMode
from telegram.ext import ContextTypes

from database import apply_pending_rates, clear_pending_rates

# Setup logger
logger = logging.getLogger(__name__)

# Testo che viene sostituito nel messaggio dopo la scelta dell'utente
PROMPT_TEXT = "👇 Vuoi aggiornare le tariffe memorizzate su OctoTracker con quelle nuove?"
CONFIRMED_TEXT = "✅ Tariffe aggiornate!"
DECLINED_TEXT = "🔧 Puoi sempre aggiornare le tariffe con /update."


async def rate_update_yes(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Callback quando l'utente clicca 'Aggiorna tariffe'"""
    query = update.callback_query
    await query.answer()

    user_id = str(update.effective_user.id)

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
