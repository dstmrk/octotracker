#!/usr/bin/env python3
"""
Script standalone per popolare lo storico tariffe Octopus Energy

Scarica i dati ARERA degli ultimi 365 giorni e li salva nella tabella rate_history
del database SQLite. Può essere eseguito più volte: i giorni già presenti vengono saltati.

Uso:
    python backfill_rate_history.py              # Ultimi 365 giorni
    python backfill_rate_history.py --days 30    # Ultimi 30 giorni
    python backfill_rate_history.py --dry-run    # Simula senza scrivere
"""

import argparse
import logging
import time
from datetime import datetime, timedelta

from data_reader import (
    _build_arera_url,
    _download_xml,
    _flatten_rates,
    _parse_arera_xml,
)
from database import get_rate_history_dates, init_db, save_rates_batch

logger = logging.getLogger(__name__)


def _download_and_parse_date(target_date: datetime) -> list[dict]:
    """Scarica e parsea i dati ARERA per una data specifica

    Returns:
        Lista di rate dict per quella data (vuota se download fallisce)
    """
    combined = {"luce": {"fissa": {}, "variabile": {}}, "gas": {"fissa": {}, "variabile": {}}}

    for service_code, servizio in [("E", "luce"), ("G", "gas")]:
        try:
            url = _build_arera_url(target_date, service_code)
            xml_content = _download_xml(url)
            parsed = _parse_arera_xml(xml_content, service_code)
            # Merge parsed data into combined
            if servizio in parsed:
                combined[servizio] = parsed[servizio]
        except Exception:
            # File non disponibile per questa data (normale per weekend/festivi)
            pass

    return _flatten_rates(combined)


def backfill(days: int = 365, dry_run: bool = False, delay: float = 0.5) -> None:
    """Esegue il backfill dello storico tariffe

    Args:
        days: Numero di giorni indietro da scaricare
        dry_run: Se True, non scrive nel DB
        delay: Secondi di attesa tra una richiesta e l'altra (rispetto rate limit)
    """
    init_db()
    existing_dates = get_rate_history_dates()

    logger.info(f"📊 Date già presenti nello storico: {len(existing_dates)}")
    logger.info(f"📅 Periodo: ultimi {days} giorni")
    if dry_run:
        logger.info("🔍 Modalità dry-run: nessuna scrittura nel DB")

    total_inserted = 0
    total_skipped = 0
    total_empty = 0
    total_errors = 0

    for days_back in range(days, -1, -1):  # Dal più vecchio al più recente
        target_date = datetime.now() - timedelta(days=days_back)
        date_str = target_date.strftime("%Y-%m-%d")

        # Salta date già presenti
        if date_str in existing_dates:
            total_skipped += 1
            continue

        # Scarica e parsea
        rates = _download_and_parse_date(target_date)

        if not rates:
            total_empty += 1
            logger.debug(f"   {date_str}: nessun dato disponibile")
            continue

        if dry_run:
            logger.info(f"   {date_str}: {len(rates)} tariffe trovate (dry-run, non salvate)")
            total_inserted += len(rates)
            continue

        # Salva nel DB
        try:
            inserted = save_rates_batch(date_str, rates)
            total_inserted += inserted
            logger.info(f"   {date_str}: {inserted} tariffe salvate")
        except Exception as e:
            total_errors += 1
            logger.error(f"   {date_str}: errore salvataggio: {e}")

        # Rate limiting
        time.sleep(delay)

    # Riepilogo
    logger.info("")
    logger.info("=" * 50)
    logger.info("📊 Riepilogo backfill:")
    logger.info(f"   Tariffe inserite: {total_inserted}")
    logger.info(f"   Giorni saltati (già presenti): {total_skipped}")
    logger.info(f"   Giorni senza dati: {total_empty}")
    if total_errors:
        logger.info(f"   Errori: {total_errors}")
    logger.info("=" * 50)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Backfill storico tariffe Octopus Energy da ARERA")
    parser.add_argument(
        "--days",
        type=int,
        default=365,
        help="Numero di giorni indietro da scaricare (default: 365)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Simula senza scrivere nel database",
    )
    parser.add_argument(
        "--delay",
        type=float,
        default=0.5,
        help="Secondi di attesa tra richieste HTTP (default: 0.5)",
    )
    parser.add_argument(
        "--log-level",
        default="INFO",
        choices=["DEBUG", "INFO", "WARNING", "ERROR"],
        help="Livello di logging (default: INFO)",
    )
    args = parser.parse_args()

    logging.basicConfig(
        level=getattr(logging, args.log_level),
        format="%(asctime)s %(message)s",
        datefmt="%H:%M:%S",
    )

    backfill(days=args.days, dry_run=args.dry_run, delay=args.delay)
