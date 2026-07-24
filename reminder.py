#!/usr/bin/env python3
"""
Invia un promemoria agli utenti con offerte a prezzo fisso in scadenza.

Le offerte fisse Octopus Energy durano 12 mesi dall'attivazione; alla scadenza
l'utente viene spostato in automatico sulla tariffa variabile del momento.
Questo modulo, un mese prima della scadenza, avvisa l'utente e mostra un
confronto con l'offerta fissa Octopus attualmente disponibile.
"""

import asyncio
import logging
import os
import time
from datetime import date
from typing import Any

from dotenv import load_dotenv
from telegram import Bot

from checker import send_notification
from constants import MAX_DECIMALS_COST, MAX_DECIMALS_ENERGY
from database import (
    get_current_rates,
    get_scadenza_reminded_map,
    load_users,
    mark_scadenza_reminded,
)
from date_utils import REMINDER_DAYS_BEFORE, days_until, format_date_display
from formatters import format_number, get_utility_label, get_utility_unit

load_dotenv()

# Setup logger
logger = logging.getLogger(__name__)

# Servizi che possono avere un'offerta fissa con scadenza
SERVICES = ("luce", "gas")

# Emoji per servizio
_SERVICE_EMOJI = {"luce": "💡", "gas": "🔥"}


def _get_expiring_services(
    user_rates: dict[str, Any],
    reminded: dict[str, str | None],
    today: date,
) -> list[tuple[str, str, int]]:
    """
    Determina per quali servizi va inviato il reminder di scadenza.

    Un servizio è in scadenza se: è a prezzo fisso, ha una data di scadenza,
    mancano tra 0 e REMINDER_DAYS_BEFORE giorni, e non è già stato notificato
    per quella stessa scadenza.

    Args:
        user_rates: Dati utente (con luce/gas, tipo, scadenza)
        reminded: Mappa {servizio: scadenza_già_notificata}
        today: Data odierna

    Returns:
        Lista di tuple (servizio, scadenza_iso, giorni_mancanti)
    """
    expiring = []

    for servizio in SERVICES:
        utility = user_rates.get(servizio)
        if not utility:
            continue

        if utility.get("tipo") != "fissa":
            continue

        scadenza = utility.get("scadenza")
        if not scadenza:
            continue

        # Già notificato per questa scadenza
        if reminded.get(servizio) == scadenza:
            continue

        days_left = days_until(scadenza, today)
        if days_left is None:
            continue

        # Finestra: da oggi fino a REMINDER_DAYS_BEFORE giorni prima della scadenza
        if 0 <= days_left <= REMINDER_DAYS_BEFORE:
            expiring.append((servizio, scadenza, days_left))

    return expiring


def _format_days_left(days_left: int) -> str:
    """Formatta l'anticipo in linguaggio naturale"""
    if days_left == 0:
        return "oggi"
    if days_left == 1:
        return "tra 1 giorno"
    return f"tra {days_left} giorni"


def _format_service_section(
    servizio: str,
    scadenza: str,
    days_left: int,
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
) -> str:
    """Costruisce la sezione del messaggio per un singolo servizio in scadenza"""
    utility = user_rates[servizio]
    fascia = utility["fascia"]
    emoji = _SERVICE_EMOJI[servizio]
    unit = get_utility_unit(servizio)
    label = get_utility_label("fissa", servizio)

    section = (
        f"{emoji} <b>{servizio.capitalize()}</b>\n"
        f"La tua offerta a prezzo fisso scade il <b>{format_date_display(scadenza)}</b> "
        f"({_format_days_left(days_left)}).\n"
    )

    # Tariffa attuale dell'utente
    user_energia = format_number(utility["energia"], max_decimals=MAX_DECIMALS_ENERGY)
    user_comm = format_number(utility["commercializzazione"], max_decimals=MAX_DECIMALS_COST)
    section += f"La tua tariffa: {label} {user_energia} {unit}, Comm. {user_comm} €/anno\n"

    # Offerta fissa Octopus attualmente disponibile (stesso servizio/fascia)
    octopus_rate = current_rates.get(servizio, {}).get("fissa", {}).get(fascia)
    if octopus_rate:
        new_energia = format_number(octopus_rate["energia"], max_decimals=MAX_DECIMALS_ENERGY)
        section += f"Offerta fissa Octopus oggi: {label} {new_energia} {unit}"
        new_comm = octopus_rate.get("commercializzazione")
        if new_comm is not None:
            comm_fmt = format_number(new_comm, max_decimals=MAX_DECIMALS_COST)
            section += f", Comm. {comm_fmt} €/anno"
        section += "\n"
        cod_offerta = octopus_rate.get("cod_offerta")
        if cod_offerta:
            section += f"📋 Codice offerta: <code>{cod_offerta}</code>\n"

    section += "\n"
    return section


def format_reminder_message(
    expiring: list[tuple[str, str, int]],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
) -> str:
    """
    Costruisce il messaggio di reminder scadenza.

    Args:
        expiring: Lista (servizio, scadenza_iso, giorni_mancanti)
        user_rates: Dati utente
        current_rates: Tariffe correnti Octopus

    Returns:
        Messaggio HTML formattato per Telegram
    """
    message = "⏰ <b>La tua offerta a prezzo fisso sta per scadere</b>\n\n"

    for servizio, scadenza, days_left in expiring:
        message += _format_service_section(servizio, scadenza, days_left, user_rates, current_rates)

    message += (
        "ℹ️ Alla scadenza Octopus Energy applica in automatico la tariffa variabile del "
        "momento. Se preferisci un'altra offerta, valuta le opzioni per tempo.\n\n"
        "🔎 Con /history vedi lo storico delle tariffe, con /update aggiorni i tuoi dati.\n\n"
        "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
    )

    return message


def _prepare_reminder(
    user_id: str,
    user_rates: dict[str, Any],
    reminded_map: dict[str, dict[str, str | None]],
    current_rates: dict[str, Any],
    today: date,
) -> tuple[str, list[tuple[str, str, int]]] | None:
    """
    Valuta un utente e prepara il messaggio di reminder se necessario.

    Returns:
        Tupla (message, expiring) se serve un reminder, None altrimenti
    """
    reminded = reminded_map.get(user_id, {})
    expiring = _get_expiring_services(user_rates, reminded, today)

    if not expiring:
        return None

    message = format_reminder_message(expiring, user_rates, current_rates)
    services = ", ".join(servizio for servizio, _, _ in expiring)
    logger.info(f"📅 Utente {user_id}: reminder scadenza accodato ({services})")
    return message, expiring


async def _send_reminders_parallel(
    bot: Bot,
    reminders_to_send: list[tuple[str, str, list[tuple[str, str, int]]]],
) -> int:
    """
    Invia i reminder in parallelo con rate limiting.

    Args:
        bot: Istanza Bot Telegram
        reminders_to_send: Lista (user_id, message, expiring)

    Returns:
        Numero di reminder inviati con successo
    """
    if not reminders_to_send:
        return 0

    logger.info(f"📨 Invio {len(reminders_to_send)} reminder scadenza in parallelo...")

    semaphore = asyncio.Semaphore(10)

    async def send_with_limit(
        user_id: str, message: str, expiring: list[tuple[str, str, int]]
    ) -> bool:
        async with semaphore:
            success = await send_notification(bot, user_id, message)
            if success:
                # Marca come notificato ogni servizio incluso nel reminder
                for servizio, scadenza, _ in expiring:
                    mark_scadenza_reminded(user_id, servizio, scadenza)
                logger.info(f"✅ Reminder scadenza inviato a {user_id}")
                return True
            logger.warning(f"❌ Reminder scadenza fallito per {user_id}")
            return False

    tasks = [
        send_with_limit(user_id, message, expiring)
        for user_id, message, expiring in reminders_to_send
    ]
    results = await asyncio.gather(*tasks, return_exceptions=True)
    return sum(1 for r in results if r is True)


async def check_and_send_reminders(bot_token: str, today: date | None = None) -> None:
    """
    Controlla le offerte fisse in scadenza e invia i reminder (chiamata da bot.py).

    Args:
        bot_token: Token del bot Telegram
        today: Data odierna (default date.today(), utile per i test)
    """
    start_time = time.time()
    reference_day = today or date.today()
    logger.info("📅 Inizio controllo scadenze offerte fisse...")

    users = load_users()
    if not users:
        logger.warning("⚠️  Nessun utente registrato per il controllo scadenze")
        return

    current_rates = get_current_rates() or {}
    reminded_map = get_scadenza_reminded_map()

    bot = Bot(token=bot_token)

    # ========== FASE 1: Prepara i reminder ==========
    reminders_to_send = []
    for user_id, user_rates in users.items():
        result = _prepare_reminder(user_id, user_rates, reminded_map, current_rates, reference_day)
        if result is not None:
            message, expiring = result
            reminders_to_send.append((user_id, message, expiring))

    # ========== FASE 2: Invia i reminder in parallelo ==========
    sent = await _send_reminders_parallel(bot, reminders_to_send)

    duration = time.time() - start_time
    logger.info(
        f"✅ Reminder scadenze completato in {duration:.2f}s - Inviati: {sent}/{len(users)}"
    )


async def main() -> None:
    """Main per esecuzione standalone"""
    token = os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")
    await check_and_send_reminders(token)


if __name__ == "__main__":
    LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, LOG_LEVEL, logging.INFO),
        format="%(asctime)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    asyncio.run(main())
