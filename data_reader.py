#!/usr/bin/env python3
"""
Lettore dati ARERA Open Data per tariffe Octopus Energy

Legge i dati delle offerte elettriche dal portale Open Data ARERA
invece di fare scraping del sito Octopus Energy.

Struttura JSON salvata (compatibile con scraper.py):
{
  "luce": {
    "fissa": {
      "monoraria": {"energia": float, "commercializzazione": float, "cod_offerta": str | None}
    },
    "variabile": {
      "monoraria": {"energia": float, "commercializzazione": float, "cod_offerta": str | None},
      "trioraria": {"energia": float, "commercializzazione": float, "cod_offerta": str | None}
    }
  },
  "gas": {
    "fissa": {
      "monoraria": {"energia": float, "commercializzazione": float, "cod_offerta": str | None}
    },
    "variabile": {
      "monoraria": {"energia": float, "commercializzazione": float, "cod_offerta": str | None}
    }
  },
  "data_aggiornamento": "YYYY-MM-DD"
}

Note:
- Per tariffe fisse: "energia" è il prezzo fisso (€/kWh o €/Smc)
- Per tariffe variabili: "energia" è lo spread (da sommare a PUN/PSVDAm)
- Filtra offerte usando P.IVA Octopus Energy Italia: 01771990445
"""

import asyncio
import json
import logging
import time
import urllib.request
import xml.etree.ElementTree as ET
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

# Setup logger
logger = logging.getLogger(__name__)

# Constants
OCTOPUS_PIVA = "01771990445"  # P.IVA Octopus Energy Italia
ARERA_BASE_URL = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML"
SERVICE_NAME_ELECTRICITY = "elettricità"  # Nome servizio per elettricità
SERVICE_NAME_GAS = "gas"  # Nome servizio per gas
REQUEST_TIMEOUT = 30  # Timeout per richieste HTTP (secondi)

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"


def _build_arera_url(date: datetime, service: str = "E") -> str:
    """Costruisce URL file XML ARERA basato sulla data

    Args:
        date: Data per cui costruire l'URL
        service: Tipo servizio - "E" per elettrico, "G" per gas

    Returns:
        URL completo del file XML

    Example:
        Per data 2025-11-13 e servizio "E":
        https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML/2025_11/PO_Offerte_E_MLIBERO_20251113.xml
    """
    year_month = date.strftime("%Y_%m")
    date_str = date.strftime("%Y%m%d")
    filename = f"PO_Offerte_{service}_MLIBERO_{date_str}.xml"

    url = f"{ARERA_BASE_URL}/{year_month}/{filename}"
    logger.debug(f"URL costruito: {url}")
    return url


def _download_xml(url: str) -> str:
    """Scarica file XML da URL con gestione errori

    Args:
        url: URL del file XML da scaricare

    Returns:
        Contenuto XML come stringa

    Raises:
        urllib.error.HTTPError: Se download fallisce
    """
    logger.debug(f"Downloading {url}")
    with urllib.request.urlopen(url, timeout=REQUEST_TIMEOUT) as response:
        content = response.read().decode("utf-8")
        logger.debug(f"Downloaded {len(content)} bytes")
        return content


def _remove_namespace(tree: ET.Element) -> None:
    """Rimuove namespace da tutti gli elementi dell'albero XML

    Args:
        tree: Root element dell'albero XML da processare
    """
    for elem in tree.iter():
        # Rimuovi namespace dal tag
        if "}" in elem.tag:
            elem.tag = elem.tag.split("}", 1)[1]


def _parse_intervallo_prezzo(intervallo: ET.Element) -> dict[str, Any] | None:
    """Parsea singolo intervallo prezzi da XML

    Args:
        intervallo: Elemento XML IntervalloPrezzi

    Returns:
        Dict con prezzo, fascia, unita_misura o None se prezzo mancante
    """
    prezzo_elem = intervallo.find("PREZZO")
    if prezzo_elem is None:
        return None

    fascia_elem = intervallo.find("FASCIA_COMPONENTE")
    unita_elem = intervallo.find("UNITA_MISURA")

    return {
        "prezzo": float(prezzo_elem.text),
        "fascia": fascia_elem.text if fascia_elem is not None else None,
        "unita_misura": unita_elem.text if unita_elem is not None else None,
    }


def _extract_intervalli_prezzi(comp: ET.Element) -> list[dict[str, Any]]:
    """Estrae lista intervalli prezzi da componente

    Args:
        comp: Elemento XML ComponenteImpresa

    Returns:
        Lista di dict con dati intervalli (vuota se nessun intervallo valido)
    """
    intervalli = []
    for intervallo in comp.findall(".//IntervalloPrezzi"):
        parsed = _parse_intervallo_prezzo(intervallo)
        if parsed is not None:
            intervalli.append(parsed)
    return intervalli


def _build_componente_result(comp: ET.Element, intervalli: list[dict[str, Any]]) -> dict[str, Any]:
    """Costruisce dict risultato per componente impresa

    Args:
        comp: Elemento XML ComponenteImpresa
        intervalli: Lista intervalli prezzi parsati

    Returns:
        Dict con nome e intervalli (se presenti)
    """
    result = {}

    nome = comp.find("NOME")
    if nome is not None:
        result["nome"] = nome.text

    if intervalli:
        result["intervalli"] = intervalli

    return result


def _extract_componente_impresa(offerta_elem: ET.Element, macroarea: str) -> dict[str, Any] | None:
    """Estrae componente impresa da offerta XML

    Args:
        offerta_elem: Elemento XML offerta
        macroarea: Codice macroarea da cercare (es: "01" per commercializzazione, "04" per energia)

    Returns:
        Dict con dati componente o None se non trovato
    """
    for comp in offerta_elem.findall(".//ComponenteImpresa"):
        macro = comp.find("MACROAREA")
        if macro is not None and macro.text == macroarea:
            intervalli = _extract_intervalli_prezzi(comp)
            return _build_componente_result(comp, intervalli)

    return None


def _validate_and_extract_luce_metadata(
    offerta_elem: ET.Element,
) -> tuple[str, str, str | None] | None:
    """Valida ed estrae metadati base di un'offerta luce

    Args:
        offerta_elem: Elemento XML offerta

    Returns:
        Tupla (tipo_offerta, tipo_fascia, cod_offerta) se valida, None altrimenti
    """
    # Verifica P.IVA Octopus
    piva_elem = offerta_elem.find(".//PIVA_UTENTE")
    if piva_elem is None or piva_elem.text != OCTOPUS_PIVA:
        return None

    # Verifica che sia offerta luce (TIPO_MERCATO=01)
    tipo_mercato_elem = offerta_elem.find(".//TIPO_MERCATO")
    if tipo_mercato_elem is None or tipo_mercato_elem.text != "01":
        return None

    # Determina tipo offerta (01=fissa, 02=variabile)
    tipo_offerta_elem = offerta_elem.find(".//TIPO_OFFERTA")
    if tipo_offerta_elem is None:
        return None
    tipo_offerta = "fissa" if tipo_offerta_elem.text == "01" else "variabile"

    # Determina tipo fascia (01=monoraria, 03=trioraria)
    tipo_fascia_elem = offerta_elem.find(".//TIPOLOGIA_FASCE")
    if tipo_fascia_elem is None:
        return None
    tipo_fascia = "monoraria" if tipo_fascia_elem.text == "01" else "trioraria"

    # Estrai codice offerta (opzionale)
    cod_offerta_elem = offerta_elem.find(".//COD_OFFERTA")
    cod_offerta = cod_offerta_elem.text if cod_offerta_elem is not None else None

    return (tipo_offerta, tipo_fascia, cod_offerta)


def _parse_offerta_luce(offerta_elem: ET.Element) -> tuple[str, str, dict[str, float]] | None:
    """Parsea singola offerta luce da XML

    Args:
        offerta_elem: Elemento XML offerta

    Returns:
        Tupla (tipo_offerta, tipo_fascia, dati) dove:
        - tipo_offerta: "fissa" o "variabile"
        - tipo_fascia: "monoraria" o "trioraria"
        - dati: {"energia": float, "commercializzazione": float, "cod_offerta": str | None}

        None se offerta non è valida o non è luce
    """
    # Valida ed estrai metadati base
    metadata = _validate_and_extract_luce_metadata(offerta_elem)
    if metadata is None:
        return None

    tipo_offerta, tipo_fascia, cod_offerta = metadata

    # Estrai costo commercializzazione (MACROAREA=01)
    comp_comm = _extract_componente_impresa(offerta_elem, "01")
    commercializzazione = None
    if comp_comm and "intervalli" in comp_comm and len(comp_comm["intervalli"]) > 0:
        # Il costo è in €/anno (UNITA_MISURA=01)
        commercializzazione = comp_comm["intervalli"][0]["prezzo"]

    # Estrai prezzo energia (MACROAREA=04)
    comp_energia = _extract_componente_impresa(offerta_elem, "04")
    energia = None
    if comp_energia and "intervalli" in comp_energia and len(comp_energia["intervalli"]) > 0:
        # Per tariffe monorarie, prendi il primo prezzo
        # Per tariffe triorarie, i prezzi sono uguali per tutte le fasce (spread fisso)
        energia = comp_energia["intervalli"][0]["prezzo"]

    if energia is None:
        logger.warning(
            f"Energia non trovata per offerta {offerta_elem.find('.//NOME_OFFERTA').text}"
        )
        return None

    # Log per debugging
    if tipo_offerta == "fissa":
        logger.info(
            f"✅ Luce fissa {tipo_fascia}: {energia} €/kWh, comm: {commercializzazione} €/anno"
        )
    else:
        logger.info(
            f"✅ Luce variabile {tipo_fascia}: PUN + {energia} €/kWh, comm: {commercializzazione} €/anno"
        )

    return (
        tipo_offerta,
        tipo_fascia,
        {
            "energia": energia,
            "commercializzazione": commercializzazione,
            "cod_offerta": cod_offerta,
        },
    )


def _parse_offerta_gas(offerta_elem: ET.Element) -> tuple[str, dict[str, float]] | None:
    """Parsea singola offerta gas da XML

    Args:
        offerta_elem: Elemento XML offerta

    Returns:
        Tupla (tipo_offerta, dati) dove:
        - tipo_offerta: "fissa" o "variabile"
        - dati: {"energia": float, "commercializzazione": float, "cod_offerta": str | None}

        None se offerta non è valida o non è gas
    """
    # Verifica P.IVA Octopus
    piva_elem = offerta_elem.find(".//PIVA_UTENTE")
    if piva_elem is None or piva_elem.text != OCTOPUS_PIVA:
        return None

    # Estrai codice offerta (opzionale)
    cod_offerta_elem = offerta_elem.find(".//COD_OFFERTA")
    cod_offerta = cod_offerta_elem.text if cod_offerta_elem is not None else None

    # Verifica che sia offerta gas (TIPO_MERCATO=02)
    tipo_mercato_elem = offerta_elem.find(".//TIPO_MERCATO")
    if tipo_mercato_elem is None or tipo_mercato_elem.text != "02":
        return None

    # Determina tipo offerta (01=fissa, 02=variabile)
    tipo_offerta_elem = offerta_elem.find(".//TIPO_OFFERTA")
    if tipo_offerta_elem is None:
        return None

    tipo_offerta_code = tipo_offerta_elem.text
    tipo_offerta = "fissa" if tipo_offerta_code == "01" else "variabile"

    # Estrai costo commercializzazione (MACROAREA=01)
    comp_comm = _extract_componente_impresa(offerta_elem, "01")
    commercializzazione = None
    if comp_comm and "intervalli" in comp_comm and len(comp_comm["intervalli"]) > 0:
        # Il costo è in €/anno (UNITA_MISURA=01)
        commercializzazione = comp_comm["intervalli"][0]["prezzo"]

    # Estrai prezzo energia (MACROAREA=04)
    comp_energia = _extract_componente_impresa(offerta_elem, "04")
    energia = None
    if comp_energia and "intervalli" in comp_energia and len(comp_energia["intervalli"]) > 0:
        # Prezzo in €/Smc (UNITA_MISURA=04)
        energia = comp_energia["intervalli"][0]["prezzo"]

    if energia is None:
        logger.warning(
            f"Energia non trovata per offerta gas {offerta_elem.find('.//NOME_OFFERTA').text}"
        )
        return None

    # Log per debugging
    if tipo_offerta == "fissa":
        logger.info(f"✅ Gas fisso monorario: {energia} €/Smc, comm: {commercializzazione} €/anno")
    else:
        logger.info(
            f"✅ Gas variabile monorario: PSV + {energia} €/Smc, comm: {commercializzazione} €/anno"
        )

    return tipo_offerta, {
        "energia": energia,
        "commercializzazione": commercializzazione,
        "cod_offerta": cod_offerta,
    }


def _empty_structure(service: str) -> dict[str, Any]:
    """Ritorna struttura vuota per un servizio

    Args:
        service: Tipo servizio - "E" per elettrico, "G" per gas

    Returns:
        Dict con struttura vuota (luce o gas)
    """
    if service == "E":
        return {"luce": {"fissa": {}, "variabile": {}}}
    elif service == "G":
        return {"gas": {"fissa": {}, "variabile": {}}}
    return {}


def _process_electricity_offers(offerte: list) -> dict[str, Any]:
    """Processa offerte elettricità e ritorna struttura dati

    Args:
        offerte: Lista elementi XML offerta

    Returns:
        Dict con struttura luce (fissa/variabile con fasce)
    """
    tariffe_luce = {"fissa": {}, "variabile": {}}
    for offerta in offerte:
        parsed = _parse_offerta_luce(offerta)
        if parsed:
            tipo_offerta, tipo_fascia, dati = parsed
            tariffe_luce[tipo_offerta][tipo_fascia] = dati
    return {"luce": tariffe_luce}


def _process_gas_offers(offerte: list) -> dict[str, Any]:
    """Processa offerte gas e ritorna struttura dati

    Args:
        offerte: Lista elementi XML offerta

    Returns:
        Dict con struttura gas (fissa/variabile monoraria)
    """
    tariffe_gas = {"fissa": {}, "variabile": {}}
    for offerta in offerte:
        parsed = _parse_offerta_gas(offerta)
        if parsed:
            tipo_offerta, dati = parsed
            tariffe_gas[tipo_offerta]["monoraria"] = dati
    return {"gas": tariffe_gas}


def _parse_arera_xml(xml_content: str, service: str) -> dict[str, Any]:
    """Parsea XML ARERA ed estrae tariffe Octopus

    Args:
        xml_content: Contenuto XML come stringa
        service: Tipo servizio - "E" per elettrico, "G" per gas

    Returns:
        Dict con struttura parziale (solo luce o solo gas)
    """
    # Parse XML con gestione errori robusta
    try:
        root = ET.fromstring(xml_content)
    except ET.ParseError as e:
        service_name = SERVICE_NAME_ELECTRICITY if service == "E" else SERVICE_NAME_GAS
        logger.error(f"❌ XML malformato per {service_name}: {e}")
        logger.debug(f"   Primi 500 caratteri: {xml_content[:500]}")
        return _empty_structure(service)
    except Exception as e:
        service_name = SERVICE_NAME_ELECTRICITY if service == "E" else SERVICE_NAME_GAS
        logger.error(f"❌ Errore inaspettato parsing XML {service_name}: {type(e).__name__}: {e}")
        return _empty_structure(service)

    # Rimuovi namespace per semplificare il parsing
    _remove_namespace(root)

    # Trova tutte le offerte
    offerte = root.findall(".//offerta")
    logger.debug(f"Trovate {len(offerte)} offerte nel file XML")

    # Processa offerte in base al servizio
    if service == "E":
        return _process_electricity_offers(offerte)
    elif service == "G":
        return _process_gas_offers(offerte)

    return {}


def _write_rates_file(file_path: Path, data: dict[str, Any]) -> None:
    """Helper sincrono per scrivere file JSON delle tariffe

    Args:
        file_path: Path del file da scrivere
        data: Dati da serializzare in JSON
    """
    with open(file_path, "w") as f:
        json.dump(data, f, indent=2)


def _fetch_service_data(
    service: str, max_days_back: int = 7
) -> tuple[dict[str, Any], datetime | None]:
    """Scarica e parsea dati per un singolo servizio (luce o gas)

    Args:
        service: "E" per elettrico, "G" per gas
        max_days_back: Numero massimo di giorni indietro da provare

    Returns:
        Tupla (dati_parsati, source_date)
    """
    service_name = SERVICE_NAME_ELECTRICITY if service == "E" else SERVICE_NAME_GAS
    xml_content = None
    source_date = None

    # Prova a scaricare il file, partendo da oggi e andando indietro
    for days_back in range(max_days_back + 1):
        try:
            target_date = datetime.now() - timedelta(days=days_back)
            url = _build_arera_url(target_date, service)

            date_str = target_date.strftime("%Y-%m-%d")
            if days_back == 0:
                logger.info(f"📄 Download dati {service_name} (data: {date_str})...")
            else:
                logger.debug(f"   Tentativo {service_name} con data: {date_str}...")

            xml_content = _download_xml(url)
            source_date = target_date
            logger.info(f"✅ File {service_name} scaricato (data: {date_str})")
            break

        except Exception as e:
            # Log solo errori non HTTP
            if not hasattr(e, "code"):
                logger.debug(
                    f"⚠️  Errore download {service_name} per {target_date.strftime('%Y-%m-%d')}: {e}"
                )
            continue

    if xml_content is None:
        logger.warning(f"⚠️  Nessun dato {service_name} disponibile (download fallito)")
        return {}, None

    # Parsea XML
    parsed_data = _parse_arera_xml(xml_content, service)
    return parsed_data, source_date


async def fetch_octopus_tariffe(max_days_back: int = 7) -> dict[str, Any]:
    """Legge tariffe Octopus da Open Data ARERA

    Args:
        max_days_back: Numero massimo di giorni indietro da provare se il file di oggi non è disponibile

    Returns:
        Dict con struttura nested luce/gas → fissa/variabile → monoraria/trioraria
        (compatibile con scraper.py)
    """
    start_time = time.time()
    logger.info("🔍 Avvio lettura tariffe Octopus da ARERA Open Data...")

    # Scarica e parsea dati luce e gas in parallelo
    data_luce, date_luce = await asyncio.to_thread(_fetch_service_data, "E", max_days_back)
    data_gas, date_gas = await asyncio.to_thread(_fetch_service_data, "G", max_days_back)

    # Combina i risultati
    tariffe_data = {
        "luce": data_luce.get("luce", {"fissa": {}, "variabile": {}}),
        "gas": data_gas.get("gas", {"fissa": {}, "variabile": {}}),
        "data_aggiornamento": datetime.now().strftime("%Y-%m-%d"),
    }

    # Aggiungi metadata
    if date_luce or date_gas:
        # Usa la data più recente
        if date_luce and date_gas:
            fonte_date = max(date_luce, date_gas)
        else:
            fonte_date = date_luce or date_gas
        tariffe_data["data_fonte_xml"] = fonte_date.strftime("%Y-%m-%d")

    # Conta tariffe trovate
    rates_count = {
        "luce_fissa": bool(tariffe_data["luce"]["fissa"].get("monoraria")),
        "luce_var_mono": bool(tariffe_data["luce"]["variabile"].get("monoraria")),
        "luce_var_tri": bool(tariffe_data["luce"]["variabile"].get("trioraria")),
        "gas_fisso": bool(tariffe_data["gas"]["fissa"].get("monoraria")),
        "gas_var": bool(tariffe_data["gas"]["variabile"].get("monoraria")),
    }
    total_found = sum(rates_count.values())

    # Salva risultati
    DATA_DIR.mkdir(exist_ok=True)
    await asyncio.to_thread(_write_rates_file, RATES_FILE, tariffe_data)

    duration = time.time() - start_time
    logger.info(f"✅ Lettura ARERA completata in {duration:.2f}s - Trovate {total_found}/5 tariffe")
    logger.info(f"💾 Tariffe salvate in {RATES_FILE}")

    return tariffe_data


if __name__ == "__main__":
    import os

    # Configura logging per esecuzione standalone
    LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, LOG_LEVEL, logging.INFO),
        format="%(message)s",
    )

    result = asyncio.run(fetch_octopus_tariffe())
    logger.info("📊 Tariffe estratte:")
    logger.info(json.dumps(result, indent=2))
