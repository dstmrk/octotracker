#!/usr/bin/env python3
"""
Sistema di feedback per OctoTracker
Gestisce raccolta feedback utenti con conversazione guidata
"""
import logging
from datetime import datetime, timedelta
from enum import IntEnum

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.constants import ParseMode
from telegram.ext import ContextTypes, ConversationHandler

from database import get_last_feedback_time, save_feedback

# Setup logger
logger = logging.getLogger(__name__)

# Rate limiting: max 1 feedback ogni 24 ore
FEEDBACK_COOLDOWN_HOURS = 24


# Stati conversazione feedback
class FeedbackState(IntEnum):
    """Stati del conversation handler per feedback"""

    RATING = 0
    COMMENT = 1


# Backward compatibility: costanti per import esterni
RATING = FeedbackState.RATING
COMMENT = FeedbackState.COMMENT


def _can_give_feedback(user_id: str) -> tuple[bool, str | None]:
    """
    Controlla se l'utente può dare feedback (rate limiting)

    Args:
        user_id: ID utente Telegram

    Returns:
        Tupla (can_proceed, error_message)
        - can_proceed: True se può procedere, False se in cooldown
        - error_message: Messaggio di errore se in cooldown, None altrimenti
    """
    last_feedback = get_last_feedback_time(user_id)

    if last_feedback is None:
        return True, None

    # Parse timestamp (formato SQLite: "YYYY-MM-DD HH:MM:SS")
    try:
        last_time = datetime.fromisoformat(last_feedback)
        now = datetime.now()
        time_diff = now - last_time

        if time_diff < timedelta(hours=FEEDBACK_COOLDOWN_HOURS):
            hours_remaining = FEEDBACK_COOLDOWN_HOURS - (time_diff.total_seconds() / 3600)
            error_msg = (
                f"⏰ Hai già inviato un feedback di recente!\n\n"
                f"Per evitare spam, puoi dare feedback una volta ogni {FEEDBACK_COOLDOWN_HOURS}h.\n"
                f"Riprova tra circa {int(hours_remaining)}h."
            )
            return False, error_msg

        return True, None

    except (ValueError, TypeError) as e:
        logger.warning(f"Errore parsing timestamp feedback per {user_id}: {e}")
        # In caso di errore, permetti comunque (safe default)
        return True, None


async def feedback_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """
    Handler comando /feedback - Avvia conversazione feedback

    Returns:
        RATING se può procedere, ConversationHandler.END se in cooldown
    """
    user_id = str(update.effective_user.id)

    # Controlla rate limiting
    can_proceed, error_msg = _can_give_feedback(user_id)
    if not can_proceed:
        await update.message.reply_text(error_msg)
        return ConversationHandler.END

    # Reset context
    context.user_data.clear()

    # Mostra pulsanti rating
    keyboard = [
        [
            InlineKeyboardButton("⭐", callback_data="rating_1"),
            InlineKeyboardButton("⭐⭐", callback_data="rating_2"),
            InlineKeyboardButton("⭐⭐⭐", callback_data="rating_3"),
        ],
        [
            InlineKeyboardButton("⭐⭐⭐⭐", callback_data="rating_4"),
            InlineKeyboardButton("⭐⭐⭐⭐⭐", callback_data="rating_5"),
        ],
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)

    message = (
        "💬 <b>Grazie per il tuo feedback!</b>\n\n"
        "Come valuteresti OctoTracker?\n"
        "Seleziona il numero di stelle:"
    )

    await update.message.reply_text(message, reply_markup=reply_markup, parse_mode=ParseMode.HTML)

    return RATING


async def feedback_rating(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """
    Handler selezione rating (1-5 stelle)

    Returns:
        COMMENT per raccogliere commento opzionale
    """
    query = update.callback_query
    await query.answer()

    # Estrai rating dal callback_data (formato: "rating_N")
    rating = int(query.data.split("_")[1])
    context.user_data["rating"] = rating

    # Mostra pulsanti per commento
    keyboard = [[InlineKeyboardButton("⏭️ Salta", callback_data="skip_comment")]]
    reply_markup = InlineKeyboardMarkup(keyboard)

    stars = "⭐" * rating
    message = (
        f"Grazie! Hai dato {stars}\n\n"
        f"Vuoi aggiungere un commento per aiutarci a migliorare?\n\n"
        f"💬 <i>Scrivi il tuo messaggio oppure premi Salta.</i>"
    )

    await query.edit_message_text(message, reply_markup=reply_markup, parse_mode=ParseMode.HTML)

    return COMMENT


async def feedback_comment(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """
    Handler commento testuale

    Returns:
        ConversationHandler.END (salva e termina)
    """
    user_id = str(update.effective_user.id)
    rating = context.user_data.get("rating")
    comment = update.message.text.strip()

    # Salva feedback nel database
    success = save_feedback(
        user_id=user_id, feedback_type="command", rating=rating, comment=comment
    )

    if success:
        message = (
            "✅ <b>Feedback ricevuto!</b>\n\n"
            "Grazie per il tuo contributo. Lo useremo per migliorare OctoTracker!"
        )
    else:
        message = (
            "❌ Si è verificato un errore nel salvataggio del feedback.\n" "Riprova più tardi."
        )

    await update.message.reply_text(message, parse_mode=ParseMode.HTML)

    return ConversationHandler.END


async def feedback_skip_comment(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """
    Handler skip commento (pulsante Salta)

    Returns:
        ConversationHandler.END (salva senza commento e termina)
    """
    query = update.callback_query
    await query.answer()

    user_id = str(update.effective_user.id)
    rating = context.user_data.get("rating")

    # Salva feedback senza commento
    success = save_feedback(user_id=user_id, feedback_type="command", rating=rating, comment=None)

    if success:
        message = (
            "✅ <b>Feedback ricevuto!</b>\n\n"
            "Grazie per il tuo contributo. Lo useremo per migliorare OctoTracker!"
        )
    else:
        message = (
            "❌ Si è verificato un errore nel salvataggio del feedback.\n" "Riprova più tardi."
        )

    await query.edit_message_text(message, parse_mode=ParseMode.HTML)

    return ConversationHandler.END


async def feedback_cancel(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """
    Handler annullamento conversazione feedback

    Returns:
        ConversationHandler.END
    """
    message = "❌ Feedback annullato.\n\nPuoi riavviare in qualsiasi momento con /feedback"

    if update.message:
        await update.message.reply_text(message)
    elif update.callback_query:
        await update.callback_query.edit_message_text(message)

    return ConversationHandler.END
