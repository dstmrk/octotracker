#!/usr/bin/env python3
"""
Test per le funzioni database utilizzate dall'API Mini App

Testa get_rate_history() per il recupero storico tariffe filtrato.
"""

import sqlite3
import tempfile
from datetime import datetime, timedelta
from pathlib import Path
from unittest.mock import patch

import pytest

from database import (
    get_rate_history,
    init_db,
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


@pytest.fixture
def populated_db(temp_db):
    """Fixture con database popolato con dati di test"""
    with patch("database.DB_FILE", temp_db):
        # Genera dati per gli ultimi 30 giorni
        base_date = datetime.now()
        for i in range(30):
            date = (base_date - timedelta(days=i)).strftime("%Y-%m-%d")
            rates = [
                # Luce fissa monoraria
                {
                    "servizio": "luce",
                    "tipo": "fissa",
                    "fascia": "monoraria",
                    "energia": 0.115 + (i * 0.001),  # Variazione nel tempo
                    "commercializzazione": 72.0,
                },
                # Luce variabile monoraria
                {
                    "servizio": "luce",
                    "tipo": "variabile",
                    "fascia": "monoraria",
                    "energia": 0.008 + (i * 0.0005),
                    "commercializzazione": 72.0,
                },
                # Luce fissa bioraria
                {
                    "servizio": "luce",
                    "tipo": "fissa",
                    "fascia": "bioraria",
                    "energia": 0.110 + (i * 0.001),
                    "commercializzazione": 72.0,
                },
                # Gas fissa
                {
                    "servizio": "gas",
                    "tipo": "fissa",
                    "fascia": "monoraria",
                    "energia": 0.39 + (i * 0.005),
                    "commercializzazione": 84.0,
                },
                # Gas variabile
                {
                    "servizio": "gas",
                    "tipo": "variabile",
                    "fascia": "monoraria",
                    "energia": 0.05 + (i * 0.002),
                    "commercializzazione": 84.0,
                },
            ]
            save_rates_batch(date, rates)

        yield temp_db


class TestGetRateHistory:
    """Test per get_rate_history()"""

    def test_get_history_basic(self, populated_db):
        """Test recupero storico base"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            assert result is not None
            assert "labels" in result
            assert "data" in result
            assert len(result["labels"]) == 30
            assert len(result["data"]) == 30

    def test_get_history_returns_sorted_by_date(self, populated_db):
        """Test che i risultati sono ordinati per data crescente"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            # Le date devono essere in ordine crescente
            labels = result["labels"]
            for i in range(len(labels) - 1):
                assert labels[i] < labels[i + 1]

    def test_get_history_energia_values(self, populated_db):
        """Test che i valori energia sono corretti"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            # Tutti i valori energia devono essere positivi
            for value in result["data"]:
                assert value > 0
                assert isinstance(value, (int, float))

    def test_get_history_includes_commercializzazione(self, populated_db):
        """Test che include anche commercializzazione se richiesto"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(
                servizio="luce",
                tipo="fissa",
                fascia="monoraria",
                days=30,
                include_commercializzazione=True,
            )

            assert "commercializzazione" in result
            assert len(result["commercializzazione"]) == 30

    def test_get_history_filter_by_days_7(self, populated_db):
        """Test filtro ultimi 7 giorni"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=7)

            # SQLite date('now', '-7 days') include oggi, quindi possono essere 7 o 8 giorni
            assert 7 <= len(result["labels"]) <= 8
            assert len(result["labels"]) == len(result["data"])

    def test_get_history_filter_by_days_365(self, populated_db):
        """Test filtro ultimi 365 giorni (default UI)"""
        with patch("database.DB_FILE", populated_db):
            # Abbiamo solo 30 giorni di dati, ma chiediamo 365
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=365)

            # Dovrebbe ritornare tutti i dati disponibili (30)
            assert len(result["labels"]) == 30

    def test_get_history_filter_luce_variabile(self, populated_db):
        """Test filtro luce variabile"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(
                servizio="luce", tipo="variabile", fascia="monoraria", days=30
            )

            assert result is not None
            assert len(result["data"]) == 30
            # Valori variabile sono spread, quindi molto più bassi
            assert all(v < 0.05 for v in result["data"])

    def test_get_history_filter_gas(self, populated_db):
        """Test filtro gas"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="gas", tipo="fissa", fascia="monoraria", days=30)

            assert result is not None
            assert len(result["data"]) == 30
            # Gas ha prezzi diversi dalla luce
            assert all(v > 0.3 for v in result["data"])

    def test_get_history_filter_bioraria(self, populated_db):
        """Test filtro fascia bioraria"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="bioraria", days=30)

            assert result is not None
            assert len(result["data"]) == 30

    def test_get_history_empty_result(self, temp_db):
        """Test con database vuoto"""
        with patch("database.DB_FILE", temp_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            assert result is not None
            assert result["labels"] == []
            assert result["data"] == []

    def test_get_history_no_matching_data(self, populated_db):
        """Test senza dati corrispondenti ai filtri"""
        with patch("database.DB_FILE", populated_db):
            # Fascia trioraria non esiste nei dati di test
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="trioraria", days=30)

            assert result is not None
            assert result["labels"] == []
            assert result["data"] == []

    def test_get_history_invalid_servizio(self, populated_db):
        """Test con servizio non valido"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(
                servizio="acqua",  # Non esiste
                tipo="fissa",
                fascia="monoraria",
                days=30,
            )

            assert result["labels"] == []
            assert result["data"] == []

    def test_get_history_db_error(self):
        """Test gestione errore database"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            # Deve ritornare risultato vuoto, non crashare
            assert result is not None
            assert result["labels"] == []
            assert result["data"] == []

    def test_get_history_metadata(self, populated_db):
        """Test che include metadata del periodo"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=30)

            assert "period" in result
            assert "from" in result["period"]
            assert "to" in result["period"]

    def test_get_history_statistics(self, populated_db):
        """Test che calcola statistiche base"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(
                servizio="luce",
                tipo="fissa",
                fascia="monoraria",
                days=30,
                include_stats=True,
            )

            assert "stats" in result
            assert "min" in result["stats"]
            assert "max" in result["stats"]
            assert "avg" in result["stats"]
            assert result["stats"]["min"] <= result["stats"]["avg"] <= result["stats"]["max"]


class TestGetRateHistoryEdgeCases:
    """Test edge cases per get_rate_history()"""

    def test_days_zero(self, populated_db):
        """Test con days=0"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=0)

            # Dovrebbe ritornare vuoto o un singolo giorno
            assert result is not None

    def test_days_negative(self, populated_db):
        """Test con days negativo"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=-5)

            # Dovrebbe gestire gracefully (vuoto o default)
            assert result is not None

    def test_very_large_days(self, populated_db):
        """Test con days molto grande"""
        with patch("database.DB_FILE", populated_db):
            result = get_rate_history(servizio="luce", tipo="fissa", fascia="monoraria", days=10000)

            # Dovrebbe ritornare tutti i dati disponibili
            assert result is not None
            assert len(result["labels"]) <= 30  # Solo 30 giorni di dati
