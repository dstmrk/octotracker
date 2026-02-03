#!/usr/bin/env python3
"""
Test per le funzioni storico tariffe in database.py
"""

import sqlite3
import tempfile
from pathlib import Path
from unittest.mock import patch

import pytest

from database import (
    get_current_rates,
    get_latest_rate_date,
    get_rate_history_dates,
    init_db,
    save_rate,
    save_rates_batch,
)


@pytest.fixture
def temp_db():
    """Fixture che crea un database temporaneo per i test"""
    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = Path(tmpdir) / "test.db"
        with patch("database.DB_FILE", db_path):
            with patch("database.DATA_DIR", Path(tmpdir)):
                init_db()
                yield db_path


class TestSaveRate:
    """Test per save_rate()"""

    def test_save_rate_success(self, temp_db):
        """Test inserimento tariffa singola"""
        with patch("database.DB_FILE", temp_db):
            result = save_rate(
                data_fonte="2025-01-15",
                servizio="luce",
                tipo="fissa",
                fascia="monoraria",
                energia=0.1078,
                commercializzazione=72.0,
                cod_offerta="COD123",
            )
            assert result is True

    def test_save_rate_minimal(self, temp_db):
        """Test inserimento tariffa con solo campi obbligatori"""
        with patch("database.DB_FILE", temp_db):
            result = save_rate(
                data_fonte="2025-01-15",
                servizio="gas",
                tipo="variabile",
                fascia="monoraria",
                energia=0.08,
            )
            assert result is True

    def test_save_rate_duplicate_ignored(self, temp_db):
        """Test che duplicati vengono ignorati senza errore"""
        with patch("database.DB_FILE", temp_db):
            # Prima inserzione
            save_rate("2025-01-15", "luce", "fissa", "monoraria", 0.1078)
            # Duplicato - stesso key, deve essere ignorato
            result = save_rate("2025-01-15", "luce", "fissa", "monoraria", 0.1078)
            assert result is True  # Non genera errore

    def test_save_rate_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")
            result = save_rate("2025-01-15", "luce", "fissa", "monoraria", 0.1078)
            assert result is False


class TestSaveRatesBatch:
    """Test per save_rates_batch()"""

    def test_save_rates_batch_success(self, temp_db):
        """Test inserimento batch di tariffe"""
        rates = [
            {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.1078,
                "commercializzazione": 72.0,
            },
            {
                "servizio": "luce",
                "tipo": "variabile",
                "fascia": "monoraria",
                "energia": 0.0088,
                "commercializzazione": 72.0,
            },
            {
                "servizio": "gas",
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.39,
                "commercializzazione": 84.0,
            },
        ]

        with patch("database.DB_FILE", temp_db):
            inserted = save_rates_batch("2025-01-15", rates)
            assert inserted == 3

    def test_save_rates_batch_empty(self, temp_db):
        """Test con lista vuota"""
        with patch("database.DB_FILE", temp_db):
            inserted = save_rates_batch("2025-01-15", [])
            assert inserted == 0

    def test_save_rates_batch_duplicates_skipped(self, temp_db):
        """Test che duplicati vengono saltati"""
        rates = [
            {"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.1078},
        ]

        with patch("database.DB_FILE", temp_db):
            # Prima inserzione
            inserted1 = save_rates_batch("2025-01-15", rates)
            assert inserted1 == 1

            # Secondo tentativo - duplicato
            inserted2 = save_rates_batch("2025-01-15", rates)
            assert inserted2 == 0  # Nessuna nuova inserzione

    def test_save_rates_batch_partial_duplicates(self, temp_db):
        """Test con mix di nuove tariffe e duplicati"""
        with patch("database.DB_FILE", temp_db):
            # Prima inserzione
            rates1 = [
                {"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.1078},
            ]
            save_rates_batch("2025-01-15", rates1)

            # Mix di duplicato e nuovo
            rates2 = [
                {"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.1078},
                {"servizio": "gas", "tipo": "fissa", "fascia": "monoraria", "energia": 0.39},
            ]
            inserted = save_rates_batch("2025-01-15", rates2)
            assert inserted == 1  # Solo gas inserito

    def test_save_rates_batch_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")
            inserted = save_rates_batch("2025-01-15", [{"servizio": "luce"}])
            assert inserted == 0


class TestGetCurrentRates:
    """Test per get_current_rates()"""

    def test_get_current_rates_success(self, temp_db):
        """Test lettura tariffe correnti"""
        rates = [
            {
                "servizio": "luce",
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.1078,
                "commercializzazione": 72.0,
                "cod_offerta": "COD123",
            },
            {
                "servizio": "luce",
                "tipo": "variabile",
                "fascia": "monoraria",
                "energia": 0.0088,
                "commercializzazione": 72.0,
            },
            {
                "servizio": "gas",
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.39,
                "commercializzazione": 84.0,
            },
        ]

        with patch("database.DB_FILE", temp_db):
            save_rates_batch("2025-01-15", rates)
            result = get_current_rates()

            assert result is not None
            assert "luce" in result
            assert "gas" in result
            assert result["luce"]["fissa"]["monoraria"]["energia"] == 0.1078
            assert result["luce"]["fissa"]["monoraria"]["commercializzazione"] == 72.0
            assert result["luce"]["fissa"]["monoraria"]["cod_offerta"] == "COD123"
            assert result["luce"]["variabile"]["monoraria"]["energia"] == 0.0088
            assert result["gas"]["fissa"]["monoraria"]["energia"] == 0.39

    def test_get_current_rates_empty_db(self, temp_db):
        """Test con database senza tariffe"""
        with patch("database.DB_FILE", temp_db):
            result = get_current_rates()
            assert result is None

    def test_get_current_rates_returns_latest(self, temp_db):
        """Test che ritorna solo le tariffe più recenti"""
        with patch("database.DB_FILE", temp_db):
            # Tariffe vecchie
            save_rates_batch(
                "2025-01-10",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}],
            )
            # Tariffe recenti
            save_rates_batch(
                "2025-01-15",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.12}],
            )

            result = get_current_rates()
            # Deve ritornare 0.12 (più recente), non 0.10
            assert result["luce"]["fissa"]["monoraria"]["energia"] == 0.12

    def test_get_current_rates_without_optional_fields(self, temp_db):
        """Test tariffe senza campi opzionali"""
        with patch("database.DB_FILE", temp_db):
            save_rates_batch(
                "2025-01-15",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.1078}],
            )

            result = get_current_rates()
            assert "energia" in result["luce"]["fissa"]["monoraria"]
            assert "commercializzazione" not in result["luce"]["fissa"]["monoraria"]
            assert "cod_offerta" not in result["luce"]["fissa"]["monoraria"]

    def test_get_current_rates_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")
            result = get_current_rates()
            assert result is None


class TestGetLatestRateDate:
    """Test per get_latest_rate_date()"""

    def test_get_latest_rate_date_success(self, temp_db):
        """Test lettura data più recente"""
        with patch("database.DB_FILE", temp_db):
            save_rates_batch(
                "2025-01-10",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}],
            )
            save_rates_batch(
                "2025-01-15",
                [{"servizio": "gas", "tipo": "fissa", "fascia": "monoraria", "energia": 0.39}],
            )

            result = get_latest_rate_date()
            assert result == "2025-01-15"

    def test_get_latest_rate_date_empty_db(self, temp_db):
        """Test con database senza tariffe"""
        with patch("database.DB_FILE", temp_db):
            result = get_latest_rate_date()
            assert result is None

    def test_get_latest_rate_date_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")
            result = get_latest_rate_date()
            assert result is None


class TestGetRateHistoryDates:
    """Test per get_rate_history_dates()"""

    def test_get_rate_history_dates_success(self, temp_db):
        """Test lettura date presenti nello storico"""
        with patch("database.DB_FILE", temp_db):
            save_rates_batch(
                "2025-01-10",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}],
            )
            save_rates_batch(
                "2025-01-15",
                [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.12}],
            )
            save_rates_batch(
                "2025-01-20",
                [{"servizio": "gas", "tipo": "fissa", "fascia": "monoraria", "energia": 0.39}],
            )

            result = get_rate_history_dates()
            assert result == {"2025-01-10", "2025-01-15", "2025-01-20"}

    def test_get_rate_history_dates_empty_db(self, temp_db):
        """Test con database senza tariffe"""
        with patch("database.DB_FILE", temp_db):
            result = get_rate_history_dates()
            assert result == set()

    def test_get_rate_history_dates_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")
            result = get_rate_history_dates()
            assert result == set()
