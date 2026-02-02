import logging

from telegram import CallbackQuery
from telegram.error import BadRequest

logger = logging.getLogger(__name__)


async def safe_answer_callback(query: CallbackQuery) -> None:
    """
    Risponde a una callback query ignorando l'errore se la query è scaduta.

    Telegram impone un timeout di ~30 secondi per rispondere alle callback query.
    Se l'utente clicca un pulsante inline e il bot risponde dopo il timeout,
    l'API restituisce "Query is too old and response timeout expired".
    Questo errore è innocuo e non deve bloccare il flusso della conversazione.
    """
    try:
        await query.answer()
    except BadRequest as e:
        if "too old" in str(e).lower() or "query id is invalid" in str(e).lower():
            logger.debug(f"Callback query scaduta (ignorata): {e}")
        else:
            raise
