#!/usr/bin/env python3
"""
Test per backfill_rate_history.py
"""

from datetime import datetime
from unittest.mock import patch

from backfill_rate_history import _download_and_parse_date, backfill


class TestDownloadAndParseDate:
    """Test per _download_and_parse_date()"""

    def test_download_and_parse_date_success(self):
        """Test download e parsing riuscito"""
        mock_xml_luce = "<root></root>"
        mock_xml_gas = "<root></root>"
        mock_parsed_luce = {
            "luce": {
                "fissa": {"monoraria": {"energia": 0.1078, "commercializzazione": 72.0}},
                "variabile": {},
            }
        }
        mock_parsed_gas = {
            "gas": {
                "fissa": {"monoraria": {"energia": 0.39, "commercializzazione": 84.0}},
                "variabile": {},
            }
        }

        with (
            patch("backfill_rate_history._download_xml") as mock_download,
            patch("backfill_rate_history._parse_arera_xml") as mock_parse,
        ):
            # Setup mocks
            mock_download.side_effect = [mock_xml_luce, mock_xml_gas]
            mock_parse.side_effect = [mock_parsed_luce, mock_parsed_gas]

            result = _download_and_parse_date(datetime(2025, 1, 15))

            assert len(result) == 2
            # Verifica struttura flat
            luce_rates = [r for r in result if r["servizio"] == "luce"]
            gas_rates = [r for r in result if r["servizio"] == "gas"]
            assert len(luce_rates) == 1
            assert len(gas_rates) == 1
            assert luce_rates[0]["energia"] == 0.1078
            assert gas_rates[0]["energia"] == 0.39

    def test_download_and_parse_date_download_fails(self):
        """Test con download fallito"""
        with patch("backfill_rate_history._download_xml") as mock_download:
            mock_download.side_effect = Exception("Network error")

            result = _download_and_parse_date(datetime(2025, 1, 15))

            # Deve ritornare lista vuota, non errore
            assert result == []

    def test_download_and_parse_date_partial_failure(self):
        """Test con download parziale (solo luce disponibile)"""
        mock_xml_luce = "<root></root>"
        mock_parsed_luce = {
            "luce": {
                "fissa": {"monoraria": {"energia": 0.1078}},
                "variabile": {},
            }
        }

        with (
            patch("backfill_rate_history._download_xml") as mock_download,
            patch("backfill_rate_history._parse_arera_xml") as mock_parse,
        ):
            # Luce OK, Gas fallisce
            mock_download.side_effect = [mock_xml_luce, Exception("Gas not available")]
            mock_parse.return_value = mock_parsed_luce

            result = _download_and_parse_date(datetime(2025, 1, 15))

            # Solo luce dovrebbe essere presente
            assert len(result) == 1
            assert result[0]["servizio"] == "luce"


class TestBackfill:
    """Test per backfill()"""

    def test_backfill_dry_run(self):
        """Test backfill in modalità dry-run"""
        mock_rates = [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}]

        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value=set()),
            patch("backfill_rate_history._download_and_parse_date", return_value=mock_rates),
            patch("backfill_rate_history.save_rates_batch") as mock_save,
            patch("backfill_rate_history.time.sleep"),
        ):
            backfill(days=2, dry_run=True, delay=0)

            # In dry-run, save_rates_batch non deve essere chiamato
            mock_save.assert_not_called()

    def test_backfill_skips_existing_dates(self):
        """Test che date già presenti vengono saltate"""
        today = datetime.now().strftime("%Y-%m-%d")

        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value={today}),
            patch("backfill_rate_history._download_and_parse_date") as mock_download,
            patch("backfill_rate_history.save_rates_batch"),
            patch("backfill_rate_history.time.sleep"),
        ):
            backfill(days=0, dry_run=False, delay=0)

            # Non deve scaricare per date già presenti
            mock_download.assert_not_called()

    def test_backfill_saves_new_rates(self):
        """Test che nuove tariffe vengono salvate"""
        mock_rates = [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}]

        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value=set()),
            patch("backfill_rate_history._download_and_parse_date", return_value=mock_rates),
            patch("backfill_rate_history.save_rates_batch", return_value=1) as mock_save,
            patch("backfill_rate_history.time.sleep"),
        ):
            backfill(days=0, dry_run=False, delay=0)

            # save_rates_batch deve essere chiamato
            mock_save.assert_called_once()

    def test_backfill_handles_empty_days(self):
        """Test gestione giorni senza dati"""
        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value=set()),
            patch("backfill_rate_history._download_and_parse_date", return_value=[]),
            patch("backfill_rate_history.save_rates_batch") as mock_save,
            patch("backfill_rate_history.time.sleep"),
        ):
            backfill(days=2, dry_run=False, delay=0)

            # Non deve salvare se non ci sono dati
            mock_save.assert_not_called()

    def test_backfill_handles_save_error(self):
        """Test gestione errore durante salvataggio"""
        mock_rates = [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}]

        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value=set()),
            patch("backfill_rate_history._download_and_parse_date", return_value=mock_rates),
            patch("backfill_rate_history.save_rates_batch", side_effect=Exception("DB error")),
            patch("backfill_rate_history.time.sleep"),
        ):
            # Non deve sollevare eccezione
            backfill(days=0, dry_run=False, delay=0)

    def test_backfill_respects_delay(self):
        """Test che il delay viene rispettato"""
        mock_rates = [{"servizio": "luce", "tipo": "fissa", "fascia": "monoraria", "energia": 0.10}]

        with (
            patch("backfill_rate_history.init_db"),
            patch("backfill_rate_history.get_rate_history_dates", return_value=set()),
            patch("backfill_rate_history._download_and_parse_date", return_value=mock_rates),
            patch("backfill_rate_history.save_rates_batch", return_value=1),
            patch("backfill_rate_history.time.sleep") as mock_sleep,
        ):
            backfill(days=1, dry_run=False, delay=0.5)

            # sleep deve essere chiamato con il delay specificato
            # (2 giorni: oggi e ieri)
            assert mock_sleep.call_count == 2
            mock_sleep.assert_called_with(0.5)
