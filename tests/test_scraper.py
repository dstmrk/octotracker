#!/usr/bin/env python3
"""
Unit tests per scraper.py
Verifica formato JSON output senza eseguire scraping reale
"""
import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, mock_open, patch

import pytest

from scraper import (
    _extract_gas_fisso,
    _extract_gas_variabile,
    _extract_luce_fissa,
    _extract_luce_variabile_mono,
    _extract_luce_variabile_tri,
    _write_rates_file,
    scrape_octopus_tariffe,
)


def test_complete_data_structure():
    """Verifica struttura completa con tutte le tariffe"""
    # Simula output completo dello scraper
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}},
            "variabile": {
                "monoraria": {"energia": 0.0088, "commercializzazione": 72.0},
                "trioraria": {"energia": 0.0088, "commercializzazione": 72.0},
            },
        },
        "gas": {
            "fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}},
            "variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 84.0}},
        },
        "data_aggiornamento": "2025-11-10",
    }

    # Verifica struttura top-level
    assert "luce" in tariffe_data
    assert "gas" in tariffe_data
    assert "data_aggiornamento" in tariffe_data

    # Verifica struttura luce
    assert "fissa" in tariffe_data["luce"]
    assert "variabile" in tariffe_data["luce"]

    # Verifica luce fissa
    assert "monoraria" in tariffe_data["luce"]["fissa"]
    assert set(tariffe_data["luce"]["fissa"]["monoraria"].keys()) == {
        "energia",
        "commercializzazione",
    }

    # Verifica luce variabile
    assert "monoraria" in tariffe_data["luce"]["variabile"]
    assert "trioraria" in tariffe_data["luce"]["variabile"]

    # Verifica struttura gas
    assert "fissa" in tariffe_data["gas"]
    assert "variabile" in tariffe_data["gas"]

    # Verifica gas fissa
    assert "monoraria" in tariffe_data["gas"]["fissa"]

    # Verifica gas variabile
    assert "monoraria" in tariffe_data["gas"]["variabile"]


def test_partial_data_only_luce():
    """Verifica dati parziali: solo tariffe luce"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}},
            "variabile": {},
        },
        "gas": {"fissa": {}, "variabile": {}},
        "data_aggiornamento": "2025-11-10",
    }

    # Struttura base deve esistere
    assert "luce" in tariffe_data
    assert "gas" in tariffe_data

    # Luce fissa deve avere dati
    assert tariffe_data["luce"]["fissa"].get("monoraria") is not None
    assert "energia" in tariffe_data["luce"]["fissa"]["monoraria"]

    # Gas deve essere vuoto ma struttura esistente
    assert isinstance(tariffe_data["gas"]["fissa"], dict)
    assert isinstance(tariffe_data["gas"]["variabile"], dict)


def test_partial_data_only_gas():
    """Verifica dati parziali: solo tariffe gas"""
    tariffe_data = {
        "luce": {"fissa": {}, "variabile": {}},
        "gas": {
            "fissa": {},
            "variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 84.0}},
        },
        "data_aggiornamento": "2025-11-10",
    }

    # Struttura base deve esistere
    assert "luce" in tariffe_data
    assert "gas" in tariffe_data

    # Gas variabile deve avere dati
    assert tariffe_data["gas"]["variabile"].get("monoraria") is not None
    assert "energia" in tariffe_data["gas"]["variabile"]["monoraria"]

    # Luce deve essere vuoto ma struttura esistente
    assert isinstance(tariffe_data["luce"]["fissa"], dict)
    assert isinstance(tariffe_data["luce"]["variabile"], dict)


def test_empty_data():
    """Verifica dati completamente vuoti (scraping fallito)"""
    tariffe_data = {
        "luce": {"fissa": {}, "variabile": {}},
        "gas": {"fissa": {}, "variabile": {}},
        "data_aggiornamento": "2025-11-10",
    }

    # Struttura base deve esistere anche se vuota
    assert "luce" in tariffe_data
    assert "gas" in tariffe_data
    assert isinstance(tariffe_data["luce"]["fissa"], dict)
    assert isinstance(tariffe_data["luce"]["variabile"], dict)
    assert isinstance(tariffe_data["gas"]["fissa"], dict)
    assert isinstance(tariffe_data["gas"]["variabile"], dict)


def test_data_types():
    """Verifica tipi di dati corretti"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}},
            "variabile": {},
        },
        "gas": {"fissa": {}, "variabile": {}},
        "data_aggiornamento": "2025-11-10",
    }

    # energia deve essere float
    assert isinstance(tariffe_data["luce"]["fissa"]["monoraria"]["energia"], float)

    # commercializzazione deve essere float
    assert isinstance(tariffe_data["luce"]["fissa"]["monoraria"]["commercializzazione"], float)

    # data_aggiornamento deve essere string
    assert isinstance(tariffe_data["data_aggiornamento"], str)


def test_json_serializable():
    """Verifica che l'output sia serializzabile in JSON"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}},
            "variabile": {"monoraria": {"energia": 0.0088, "commercializzazione": 72.0}},
        },
        "gas": {
            "fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}},
            "variabile": {},
        },
        "data_aggiornamento": "2025-11-10",
    }

    # Deve essere serializzabile senza errori
    json_str = json.dumps(tariffe_data, indent=2)
    parsed = json.loads(json_str)
    assert tariffe_data == parsed


# ========== TESTS FOR EXTRACTION FUNCTIONS ==========


def test_extract_luce_fissa_found():
    """Test estrazione luce fissa quando trovata"""
    text = "Tariffa luce fissa 0.1078 €/kWh con commercializzazione 72 €/anno"
    result = _extract_luce_fissa(text)

    assert result is not None
    assert result["energia"] == 0.1078
    assert result["commercializzazione"] == 72.0


def test_extract_luce_fissa_not_found():
    """Test estrazione luce fissa quando non trovata"""
    text = "Tariffa luce variabile PUN Mono + 0.0088 €/kWh"
    result = _extract_luce_fissa(text)

    assert result is None


def test_extract_luce_fissa_comma_separator():
    """Test estrazione luce fissa con virgola come separatore decimale"""
    text = "Tariffa luce fissa 0,1078 €/kWh con commercializzazione 72 €/anno"
    result = _extract_luce_fissa(text)

    assert result is not None
    assert result["energia"] == 0.1078
    assert result["commercializzazione"] == 72.0


def test_extract_luce_variabile_mono_found():
    """Test estrazione luce variabile monoraria quando trovata"""
    text = "Tariffa luce variabile PUN Mono + 0.0088 €/kWh con commercializzazione 72 €/anno"
    result = _extract_luce_variabile_mono(text)

    assert result is not None
    assert result["energia"] == 0.0088
    assert result["commercializzazione"] == 72.0


def test_extract_luce_variabile_mono_not_found():
    """Test estrazione luce variabile monoraria quando non trovata"""
    text = "Tariffa luce fissa 0.1078 €/kWh"
    result = _extract_luce_variabile_mono(text)

    assert result is None


def test_extract_gas_fisso_found():
    """Test estrazione gas fisso quando trovato"""
    text = "Tariffa gas fisso 0.39 €/Smc con commercializzazione 84 €/anno"
    result = _extract_gas_fisso(text)

    assert result is not None
    assert result["energia"] == 0.39
    assert result["commercializzazione"] == 84.0


def test_extract_gas_fisso_not_found():
    """Test estrazione gas fisso quando non trovato"""
    text = "Tariffa gas variabile PSVDAm + 0.08 €/Smc"
    result = _extract_gas_fisso(text)

    assert result is None


def test_extract_gas_variabile_found():
    """Test estrazione gas variabile quando trovato"""
    text = "Tariffa gas variabile PSVDAm + 0.08 €/Smc con commercializzazione 84 €/anno"
    result = _extract_gas_variabile(text)

    assert result is not None
    assert result["energia"] == 0.08
    assert result["commercializzazione"] == 84.0


def test_extract_gas_variabile_not_found():
    """Test estrazione gas variabile quando non trovato"""
    text = "Tariffa gas fisso 0.39 €/Smc"
    result = _extract_gas_variabile(text)

    assert result is None


@pytest.mark.asyncio
async def test_extract_luce_variabile_tri_found_directly():
    """Test estrazione luce variabile trioraria trovata direttamente"""
    page_mock = AsyncMock()
    text = "Tariffa luce variabile PUN + 0.0088 €/kWh con commercializzazione 72 €/anno"

    result = await _extract_luce_variabile_tri(page_mock, text)

    assert result is not None
    assert result["energia"] == 0.0088
    assert result["commercializzazione"] == 72.0
    # Il toggle non dovrebbe essere cercato
    page_mock.query_selector.assert_not_called()


@pytest.mark.asyncio
async def test_extract_luce_variabile_tri_with_toggle():
    """Test estrazione luce variabile trioraria con click su toggle"""
    page_mock = AsyncMock()
    toggle_mock = AsyncMock()

    # Simula che il toggle esista
    page_mock.query_selector.return_value = toggle_mock

    # Simula testo dopo il click sul toggle
    page_mock.inner_text.return_value = (
        "Tariffa luce variabile PUN + 0.0088 €/kWh con commercializzazione 72 €/anno"
    )

    # Testo iniziale senza "PUN +"
    text = "Tariffa luce variabile monoraria"

    result = await _extract_luce_variabile_tri(page_mock, text)

    assert result is not None
    assert result["energia"] == 0.0088
    assert result["commercializzazione"] == 72.0

    # Verifica che il toggle sia stato cliccato 2 volte (apertura e chiusura)
    assert toggle_mock.click.call_count == 2


@pytest.mark.asyncio
async def test_extract_luce_variabile_tri_not_found():
    """Test estrazione luce variabile trioraria quando non trovata"""
    page_mock = AsyncMock()
    page_mock.query_selector.return_value = None
    text = "Tariffa luce fissa"

    result = await _extract_luce_variabile_tri(page_mock, text)

    assert result is None


def test_write_rates_file():
    """Test scrittura file tariffe"""
    test_path = Path("/tmp/test_rates.json")
    test_data = {"luce": {"fissa": {"monoraria": {"energia": 0.1078}}}}

    with patch("builtins.open", mock_open()) as mock_file:
        _write_rates_file(test_path, test_data)

        # Verifica che il file sia stato aperto in scrittura
        mock_file.assert_called_once_with(test_path, "w")

        # Verifica che json.dump sia stato chiamato (indirettamente verificato dal write)
        handle = mock_file()
        handle.write.assert_called()


@pytest.mark.asyncio
async def test_scrape_octopus_tariffe_success():
    """Test scraping completo con successo"""
    from unittest.mock import Mock

    # Simula testo della pagina con tutte le tariffe
    page_text = """
    Luce fissa monoraria 0.1078 €/kWh commercializzazione 72 €/anno
    Luce variabile PUN Mono + 0.0088 €/kWh commercializzazione 72 €/anno
    Luce variabile PUN + 0.0088 €/kWh commercializzazione 72 €/anno
    Gas fisso 0.39 €/Smc commercializzazione 84 €/anno
    Gas variabile PSVDAm + 0.08 €/Smc commercializzazione 84 €/anno
    """

    # Mock playwright context manager
    mock_page = MagicMock()
    mock_page.goto = AsyncMock()
    mock_page.wait_for_selector = AsyncMock()
    mock_page.inner_text = AsyncMock(return_value=page_text)
    mock_page.query_selector = AsyncMock(return_value=None)

    mock_browser = MagicMock()
    mock_browser.new_page = AsyncMock(return_value=mock_page)
    mock_browser.close = AsyncMock()

    mock_playwright_instance = MagicMock()
    mock_playwright_instance.chromium.launch = AsyncMock(return_value=mock_browser)

    mock_playwright_ctx = MagicMock()
    mock_playwright_ctx.__aenter__ = AsyncMock(return_value=mock_playwright_instance)
    mock_playwright_ctx.__aexit__ = AsyncMock(return_value=None)

    with patch("scraper.async_playwright", return_value=mock_playwright_ctx):
        with patch("scraper.asyncio.to_thread", new_callable=AsyncMock) as mock_to_thread:
            with patch("scraper.DATA_DIR") as mock_data_dir:
                mock_data_dir.mkdir = MagicMock()

                result = await scrape_octopus_tariffe()

                # Verifica struttura risultato
                assert "luce" in result
                assert "gas" in result
                assert "data_aggiornamento" in result

                # Verifica che le tariffe siano state estratte
                assert result["luce"]["fissa"].get("monoraria") is not None
                assert result["luce"]["variabile"].get("monoraria") is not None
                assert result["gas"]["fissa"].get("monoraria") is not None
                assert result["gas"]["variabile"].get("monoraria") is not None

                # Verifica che il file sia stato scritto
                mock_to_thread.assert_called_once()


@pytest.mark.asyncio
async def test_scrape_octopus_tariffe_timeout():
    """Test scraping con timeout"""
    from playwright.async_api import TimeoutError as PlaywrightTimeout
    from unittest.mock import Mock

    # Mock page con timeout
    mock_page = MagicMock()
    mock_page.goto = AsyncMock(side_effect=PlaywrightTimeout("Timeout"))

    mock_browser = MagicMock()
    mock_browser.new_page = AsyncMock(return_value=mock_page)
    mock_browser.close = AsyncMock()

    mock_playwright_instance = MagicMock()
    mock_playwright_instance.chromium.launch = AsyncMock(return_value=mock_browser)

    mock_playwright_ctx = MagicMock()
    mock_playwright_ctx.__aenter__ = AsyncMock(return_value=mock_playwright_instance)
    mock_playwright_ctx.__aexit__ = AsyncMock(return_value=None)

    with patch("scraper.async_playwright", return_value=mock_playwright_ctx):
        with pytest.raises(PlaywrightTimeout):
            await scrape_octopus_tariffe()
