#!/usr/bin/env python3
"""
Utility per la gestione delle date delle offerte a prezzo fisso.

Responsabilità:
- Parsing della data di attivazione inserita dall'utente (vari formati italiani)
- Calcolo della data di scadenza (attivazione + 12 mesi)
- Formattazione date ISO ↔ display (DD/MM/YYYY)
- Calcolo dei giorni mancanti a una scadenza
"""

import logging
from datetime import date, datetime

# Setup logger
logger = logging.getLogger(__name__)

# Durata standard di un'offerta a prezzo fisso Octopus Energy (mesi)
FIXED_OFFER_MONTHS = 12

# Anticipo con cui inviare il reminder prima della scadenza (giorni)
REMINDER_DAYS_BEFORE = 30

# Formati di input accettati per la data di attivazione
_ACCEPTED_FORMATS = ("%d/%m/%Y", "%d-%m-%Y", "%d.%m.%Y")

# Range plausibile per la data di attivazione (protezione da typo evidenti)
_MIN_YEAR = 2015


def parse_activation_date(text: str) -> date | None:
    """
    Converte il testo inserito dall'utente in una data.

    Accetta i formati DD/MM/YYYY, DD-MM-YYYY, DD.MM.YYYY.

    Args:
        text: Testo inserito dall'utente

    Returns:
        Oggetto date se valido e plausibile, None altrimenti
    """
    if not text:
        return None

    cleaned = text.strip()

    for fmt in _ACCEPTED_FORMATS:
        try:
            parsed = datetime.strptime(cleaned, fmt).date()
        except ValueError:
            continue

        # Sanity check: anno plausibile e non troppo nel futuro
        if parsed.year < _MIN_YEAR:
            return None
        if parsed.year > date.today().year + 1:
            return None

        return parsed

    return None


def add_months(start: date, months: int) -> date:
    """
    Aggiunge un numero di mesi a una data, gestendo il caso 29 febbraio.

    Args:
        start: Data di partenza
        months: Numero di mesi da aggiungere

    Returns:
        Nuova data con i mesi aggiunti (il giorno viene troncato all'ultimo
        giorno valido del mese target, es. 29/02 → 28/02 in anni non bisestili)
    """
    month_index = start.month - 1 + months
    year = start.year + month_index // 12
    month = month_index % 12 + 1

    # Giorni nel mese target (gestione mesi corti e anni bisestili)
    if month == 12:
        days_in_month = 31
    else:
        days_in_month = (date(year, month + 1, 1) - date(year, month, 1)).days

    day = min(start.day, days_in_month)
    return date(year, month, day)


def compute_expiry_date(activation: date) -> date:
    """
    Calcola la data di scadenza di un'offerta fissa (attivazione + 12 mesi).

    Args:
        activation: Data di attivazione dell'offerta

    Returns:
        Data di scadenza
    """
    return add_months(activation, FIXED_OFFER_MONTHS)


def format_date_display(iso_date: str | date) -> str:
    """
    Formatta una data ISO (YYYY-MM-DD) o un oggetto date in DD/MM/YYYY.

    Args:
        iso_date: Data in formato ISO string oppure oggetto date

    Returns:
        Data formattata come DD/MM/YYYY, o stringa vuota se non valida
    """
    if isinstance(iso_date, date):
        return iso_date.strftime("%d/%m/%Y")

    try:
        parsed = date.fromisoformat(iso_date)
    except (ValueError, TypeError):
        return ""

    return parsed.strftime("%d/%m/%Y")


def days_until(iso_date: str, today: date | None = None) -> int | None:
    """
    Calcola i giorni mancanti alla data indicata.

    Args:
        iso_date: Data target in formato ISO string (YYYY-MM-DD)
        today: Data odierna (default: date.today(), utile per i test)

    Returns:
        Numero di giorni mancanti (negativo se già passata), None se non valida
    """
    try:
        target = date.fromisoformat(iso_date)
    except (ValueError, TypeError):
        return None

    reference = today or date.today()
    return (target - reference).days
