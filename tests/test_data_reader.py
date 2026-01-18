#!/usr/bin/env python3
"""
Unit tests for data_reader.py
Tests XML parsing and data extraction from ARERA Open Data
"""

import json
import xml.etree.ElementTree as ET
from datetime import datetime
from pathlib import Path
from unittest.mock import mock_open, patch

import pytest

from data_reader import (
    _build_arera_url,
    _empty_structure,
    _extract_componente_impresa,
    _fetch_service_data,
    _parse_arera_xml,
    _parse_offerta_gas,
    _parse_offerta_luce,
    _remove_namespace,
    _write_rates_file,
    fetch_octopus_tariffe,
)

# ========== URL BUILDING TESTS ==========


def test_build_arera_url_electricity():
    """Test costruzione URL per elettricità"""
    date = datetime(2025, 11, 13)
    url = _build_arera_url(date, "E")
    expected = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_11/PO_Offerte_E_MLIBERO_20251113.xml"
    assert url == expected


def test_build_arera_url_gas():
    """Test costruzione URL per gas"""
    date = datetime(2025, 11, 13)
    url = _build_arera_url(date, "G")
    expected = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_11/PO_Offerte_G_MLIBERO_20251113.xml"
    assert url == expected


def test_build_arera_url_different_month():
    """Test costruzione URL per un mese diverso"""
    date = datetime(2025, 1, 5)
    url = _build_arera_url(date, "E")
    expected = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_01/PO_Offerte_E_MLIBERO_20250105.xml"
    assert url == expected


# ========== XML NAMESPACE TESTS ==========


def test_remove_namespace():
    """Test rimozione namespace da XML"""
    xml_str = """<?xml version="1.0"?>
    <root xmlns="http://example.com">
        <child>test</child>
    </root>"""
    tree = ET.fromstring(xml_str)
    _remove_namespace(tree)

    # Tag non dovrebbe contenere namespace
    assert "}" not in tree.tag
    assert tree.tag == "root"
    assert tree.find("child") is not None


# ========== COMPONENT EXTRACTION TESTS ==========


def test_extract_componente_impresa_found():
    """Test estrazione componente impresa quando trovata"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Test</NOME>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
                <UNITA_MISURA>01</UNITA_MISURA>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _extract_componente_impresa(offerta, "01")

    assert result is not None
    assert result["nome"] == "Test"
    assert len(result["intervalli"]) == 1
    assert result["intervalli"][0]["prezzo"] == 72.0


def test_extract_componente_impresa_not_found():
    """Test estrazione componente impresa quando non trovata"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Test</NOME>
            <MACROAREA>01</MACROAREA>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _extract_componente_impresa(offerta, "04")

    assert result is None


def test_extract_componente_impresa_multiple_fasce():
    """Test estrazione componente impresa con più fasce"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <ComponenteImpresa>
            <NOME>Spread</NOME>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <FASCIA_COMPONENTE>01</FASCIA_COMPONENTE>
                <PREZZO>0.0088</PREZZO>
                <UNITA_MISURA>03</UNITA_MISURA>
            </IntervalloPrezzi>
            <IntervalloPrezzi>
                <FASCIA_COMPONENTE>02</FASCIA_COMPONENTE>
                <PREZZO>0.0088</PREZZO>
                <UNITA_MISURA>03</UNITA_MISURA>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _extract_componente_impresa(offerta, "04")

    assert result is not None
    assert len(result["intervalli"]) == 2
    assert result["intervalli"][0]["fascia"] == "01"
    assert result["intervalli"][1]["fascia"] == "02"


# ========== LUCE PARSING TESTS ==========


def test_parse_offerta_luce_fissa_monoraria():
    """Test parsing offerta luce fissa monoraria"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
            <NOME_OFFERTA>Test Fissa</NOME_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.1078</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is not None
    tipo_offerta, tipo_fascia, dati = result
    assert tipo_offerta == "fissa"
    assert tipo_fascia == "monoraria"
    assert dati["energia"] == 0.1078
    assert dati["commercializzazione"] == 72.0


def test_parse_offerta_luce_variabile_trioraria():
    """Test parsing offerta luce variabile trioraria"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>03</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.0088</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is not None
    tipo_offerta, tipo_fascia, dati = result
    assert tipo_offerta == "variabile"
    assert tipo_fascia == "trioraria"
    assert dati["energia"] == 0.0088


def test_parse_offerta_luce_wrong_piva():
    """Test parsing offerta luce con P.IVA sbagliata"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>12345678901</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is None


def test_parse_offerta_luce_gas_market():
    """Test parsing offerta gas (dovrebbe restituire None)"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is None


def test_parse_offerta_luce_with_cod_offerta():
    """Test estrazione codice offerta da XML luce"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
            <COD_OFFERTA>000129ESVML77XXXXXOCTOFLEXMONv77</COD_OFFERTA>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.0088</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is not None
    tipo_offerta, tipo_fascia, dati = result
    assert dati["cod_offerta"] == "000129ESVML77XXXXXOCTOFLEXMONv77"


def test_parse_offerta_luce_without_cod_offerta():
    """Test parsing luce senza codice offerta (backward compatibility)"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>01</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <TipoPrezzo>
            <TIPOLOGIA_FASCE>01</TIPOLOGIA_FASCE>
        </TipoPrezzo>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>72.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.1045</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_luce(offerta)

    assert result is not None
    tipo_offerta, tipo_fascia, dati = result
    assert dati["cod_offerta"] is None


# ========== GAS PARSING TESTS ==========


def test_parse_offerta_gas_fissa():
    """Test parsing offerta gas fissa"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.39</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_gas(offerta)

    assert result is not None
    tipo_offerta, dati = result
    assert tipo_offerta == "fissa"
    assert dati["energia"] == 0.39
    assert dati["commercializzazione"] == 84.0


def test_parse_offerta_gas_variabile():
    """Test parsing offerta gas variabile"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.08</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_gas(offerta)

    assert result is not None
    tipo_offerta, dati = result
    assert tipo_offerta == "variabile"
    assert dati["energia"] == 0.08


def test_parse_offerta_gas_wrong_piva():
    """Test parsing offerta gas con P.IVA sbagliata"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>12345678901</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
        </DettaglioOfferta>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_gas(offerta)

    assert result is None


def test_parse_offerta_gas_with_cod_offerta():
    """Test estrazione codice offerta da XML gas"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
            <COD_OFFERTA>000129GSFML37XXXXXXXXOCTOFIXGv37</COD_OFFERTA>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>01</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.36</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_gas(offerta)

    assert result is not None
    tipo_offerta, dati = result
    assert dati["cod_offerta"] == "000129GSFML37XXXXXXXXOCTOFIXGv37"


def test_parse_offerta_gas_without_cod_offerta():
    """Test parsing gas senza codice offerta (backward compatibility)"""
    xml_str = """<?xml version="1.0"?>
    <offerta>
        <IdentificativiOfferta>
            <PIVA_UTENTE>01771990445</PIVA_UTENTE>
        </IdentificativiOfferta>
        <DettaglioOfferta>
            <TIPO_MERCATO>02</TIPO_MERCATO>
            <TIPO_OFFERTA>02</TIPO_OFFERTA>
        </DettaglioOfferta>
        <ComponenteImpresa>
            <MACROAREA>01</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>84.0</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
        <ComponenteImpresa>
            <MACROAREA>04</MACROAREA>
            <IntervalloPrezzi>
                <PREZZO>0.08</PREZZO>
            </IntervalloPrezzi>
        </ComponenteImpresa>
    </offerta>"""
    offerta = ET.fromstring(xml_str)
    result = _parse_offerta_gas(offerta)

    assert result is not None
    tipo_offerta, dati = result
    assert dati["cod_offerta"] is None


# ========== XML PARSING TESTS ==========


def test_parse_arera_xml_electricity():
    """Test parsing completo XML elettricità"""
    xml_path = Path(__file__).parent.parent / "test_data" / "sample_arera.xml"
    xml_content = xml_path.read_text()

    result = _parse_arera_xml(xml_content, "E")

    assert "luce" in result
    assert "fissa" in result["luce"]
    assert "variabile" in result["luce"]


def test_parse_arera_xml_gas():
    """Test parsing completo XML gas"""
    # Use the gas offers from sample_arera.xml
    xml_path = Path(__file__).parent.parent / "test_data" / "sample_arera.xml"
    xml_content = xml_path.read_text()

    result = _parse_arera_xml(xml_content, "G")

    assert "gas" in result
    assert "fissa" in result["gas"]
    assert "variabile" in result["gas"]


# ========== FILE WRITING TESTS ==========


def test_write_rates_file():
    """Test scrittura file tariffe"""
    test_path = Path("/tmp/test_rates.json")
    test_data = {"luce": {"fissa": {"monoraria": {"energia": 0.1078}}}}

    with patch("builtins.open", mock_open()) as mock_file:
        _write_rates_file(test_path, test_data)

        # Verifica che il file sia stato aperto in scrittura
        mock_file.assert_called_once_with(test_path, "w")

        # Verifica che json.dump sia stato chiamato
        handle = mock_file()
        handle.write.assert_called()


# ========== INTEGRATION TESTS ==========


@pytest.mark.asyncio
async def test_fetch_octopus_tariffe_success():
    """Test fetch completo con successo"""
    # Mock _fetch_service_data to return test data
    mock_luce_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.1078, "commercializzazione": 72.0}},
            "variabile": {
                "monoraria": {"energia": 0.0088, "commercializzazione": 72.0},
                "trioraria": {"energia": 0.0088, "commercializzazione": 72.0},
            },
        }
    }
    mock_gas_data = {
        "gas": {
            "fissa": {"monoraria": {"energia": 0.39, "commercializzazione": 84.0}},
            "variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 84.0}},
        }
    }

    with patch("data_reader._fetch_service_data"):
        with patch("data_reader.asyncio.to_thread") as mock_to_thread:
            # Mock parallel fetches
            async def side_effect(func, *args):
                if args[0] == "E":
                    return mock_luce_data, datetime.now()
                else:
                    return mock_gas_data, datetime.now()

            mock_to_thread.side_effect = side_effect

            with patch("data_reader._write_rates_file"):
                result = await fetch_octopus_tariffe()

                # Verify structure
                assert "luce" in result
                assert "gas" in result
                assert "data_aggiornamento" in result

                # Verify data
                assert result["luce"]["fissa"]["monoraria"]["energia"] == 0.1078
                assert result["gas"]["fissa"]["monoraria"]["energia"] == 0.39


@pytest.mark.asyncio
async def test_fetch_octopus_tariffe_partial_failure():
    """Test fetch con fallimento parziale (solo gas disponibile)"""
    mock_gas_data = {
        "gas": {
            "fissa": {"monoraria": {"energia": 0.39, "commercializzazione": 84.0}},
            "variabile": {},
        }
    }

    with patch("data_reader.asyncio.to_thread") as mock_to_thread:

        async def side_effect(func, *args):
            if args[0] == "E":
                return {}, None  # Luce fallita
            else:
                return mock_gas_data, datetime.now()  # Gas OK

        mock_to_thread.side_effect = side_effect

        with patch("data_reader._write_rates_file"):
            result = await fetch_octopus_tariffe()

            # Structure should exist even if empty
            assert "luce" in result
            assert "gas" in result

            # Luce should be empty
            assert result["luce"]["fissa"] == {}
            assert result["luce"]["variabile"] == {}

            # Gas should have data
            assert result["gas"]["fissa"]["monoraria"]["energia"] == 0.39


def test_fetch_service_data_download_failure():
    """Test _fetch_service_data quando download fallisce"""
    with patch("data_reader._download_xml", side_effect=Exception("Network error")):
        result, date = _fetch_service_data("E", max_days_back=2)

        # Should return empty dict and None when all attempts fail
        assert result == {}
        assert date is None


# ========== DATA STRUCTURE TESTS ==========


def test_complete_data_structure():
    """Verifica struttura completa con tutte le tariffe"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.1078, "commercializzazione": 72.0}},
            "variabile": {
                "monoraria": {"energia": 0.0088, "commercializzazione": 72.0},
                "trioraria": {"energia": 0.0088, "commercializzazione": 72.0},
            },
        },
        "gas": {
            "fissa": {"monoraria": {"energia": 0.39, "commercializzazione": 84.0}},
            "variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 84.0}},
        },
        "data_aggiornamento": "2025-11-13",
    }

    # Verify top-level structure
    assert "luce" in tariffe_data
    assert "gas" in tariffe_data
    assert "data_aggiornamento" in tariffe_data

    # Verify luce structure
    assert "fissa" in tariffe_data["luce"]
    assert "variabile" in tariffe_data["luce"]

    # Verify gas structure
    assert "fissa" in tariffe_data["gas"]
    assert "variabile" in tariffe_data["gas"]


def test_data_types():
    """Verifica tipi di dati corretti"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.1078, "commercializzazione": 72.0}},
            "variabile": {},
        },
        "gas": {"fissa": {}, "variabile": {}},
        "data_aggiornamento": "2025-11-13",
    }

    # energia must be float
    assert isinstance(tariffe_data["luce"]["fissa"]["monoraria"]["energia"], float)

    # commercializzazione must be float
    assert isinstance(tariffe_data["luce"]["fissa"]["monoraria"]["commercializzazione"], float)

    # data_aggiornamento must be string
    assert isinstance(tariffe_data["data_aggiornamento"], str)


def test_json_serializable():
    """Verifica che l'output sia serializzabile in JSON"""
    tariffe_data = {
        "luce": {
            "fissa": {"monoraria": {"energia": 0.1078, "commercializzazione": 72.0}},
            "variabile": {"monoraria": {"energia": 0.0088, "commercializzazione": 72.0}},
        },
        "gas": {
            "fissa": {"monoraria": {"energia": 0.39, "commercializzazione": 84.0}},
            "variabile": {},
        },
        "data_aggiornamento": "2025-11-13",
    }

    # Must be serializable without errors
    json_str = json.dumps(tariffe_data, indent=2)
    parsed = json.loads(json_str)
    assert tariffe_data == parsed


# ===== Test Error Handling =====


def test_parse_arera_xml_malformed():
    """Test gestione XML malformato"""
    malformed_xml = """<?xml version="1.0"?>
    <OfferList>
        <Offer>
            <SupplierName>Octopus Energy Italia</SupplierName>
            <OfferName>TARIFF<missing_close_tag>
        </Offer>
    """  # Missing closing tags

    result = _parse_arera_xml(malformed_xml, "E")

    # Should return empty structure on parse error
    assert result == {"luce": {"fissa": {}, "variabile": {}}}


def test_parse_arera_xml_non_xml_content():
    """Test gestione contenuto non-XML (es. pagina di errore HTML)"""
    html_error = """<!DOCTYPE html>
    <html>
    <head><title>500 Internal Server Error</title></head>
    <body><h1>Server Error</h1></body>
    </html>"""

    result = _parse_arera_xml(html_error, "G")

    # Should return empty structure on parse error
    assert result == {"gas": {"fissa": {}, "variabile": {}}}


def test_parse_arera_xml_empty_string():
    """Test gestione stringa vuota"""
    result = _parse_arera_xml("", "E")

    # Should return empty structure on parse error
    assert result == {"luce": {"fissa": {}, "variabile": {}}}


def test_empty_structure_electricity():
    """Test struttura vuota per elettricità"""
    result = _empty_structure("E")
    assert result == {"luce": {"fissa": {}, "variabile": {}}}


def test_empty_structure_gas():
    """Test struttura vuota per gas"""
    result = _empty_structure("G")
    assert result == {"gas": {"fissa": {}, "variabile": {}}}


def test_empty_structure_invalid_service():
    """Test struttura vuota per servizio non valido"""
    result = _empty_structure("INVALID")
    assert result == {}
