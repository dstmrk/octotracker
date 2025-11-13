#!/usr/bin/env python3
"""
Lettore dati ARERA Open Data per tariffe Octopus Energy

Legge i dati delle offerte elettriche dal portale Open Data ARERA
invece di fare scraping del sito Octopus Energy.

Struttura JSON salvata (compatibile con scraper.py):
{
  "luce": {
    "fissa": {
      "monoraria": {"energia": float, "commercializzazione": float}
    },
    "variabile": {
      "monoraria": {"energia": float, "commercializzazione": float},
      "trioraria": {"energia": float, "commercializzazione": float}
    }
  },
  "gas": {
    "fissa": {
      "monoraria": {"energia": float, "commercializzazione": float}
    },
    "variabile": {
      "monoraria": {"energia": float, "commercializzazione": float}
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
import xml.etree.ElementTree as ET
from datetime import datetime, timedelta
from pathlib import Path
from typing import Any

import httpx

# Setup logger
logger = logging.getLogger(__name__)

# Constants
OCTOPUS_PIVA = "01771990445"  # P.IVA Octopus Energy Italia
ARERA_BASE_URL = "https://www.ilportaleofferte.it/portaleOfferte/resources/opendata/csv/offerteML"
REQUEST_TIMEOUT = 30.0  # Timeout per richieste HTTP
USER_AGENT = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
ARERA_NAMESPACE = {"ns": "http://www.acquirenteunico.it/schemas/SII_AU/OffertaRetail/01"}

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"
LOCAL_XML_FILE = DATA_DIR / "arera_offerte_elettriche.xml"  # File XML locale come fallback
CACHE_METADATA_FILE = DATA_DIR / "arera_cache_metadata.json"  # Metadata del file in cache


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


async def _download_xml(url: str) -> str:
    """Scarica file XML da URL con gestione errori

    Args:
        url: URL del file XML da scaricare

    Returns:
        Contenuto XML come stringa

    Raises:
        httpx.HTTPError: Se download fallisce
    """
    headers = {
        "User-Agent": USER_AGENT,
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "it-IT,it;q=0.9,en-US;q=0.8,en;q=0.7",
        "Accept-Encoding": "gzip, deflate, br",
        "Referer": "https://www.ilportaleofferte.it/",
        "DNT": "1",
        "Connection": "keep-alive",
        "Upgrade-Insecure-Requests": "1",
    }

    async with httpx.AsyncClient(timeout=REQUEST_TIMEOUT) as client:
        logger.debug(f"Downloading {url}")
        response = await client.get(url, headers=headers, follow_redirects=True)
        response.raise_for_status()

        content = response.text
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
            result = {}

            # Nome e descrizione
            nome = comp.find("NOME")
            if nome is not None:
                result["nome"] = nome.text

            # Intervalli prezzi
            intervalli = []
            for intervallo in comp.findall(".//IntervalloPrezzi"):
                prezzo_elem = intervallo.find("PREZZO")
                fascia_elem = intervallo.find("FASCIA_COMPONENTE")
                unita_elem = intervallo.find("UNITA_MISURA")

                if prezzo_elem is not None:
                    intervalli.append({
                        "prezzo": float(prezzo_elem.text),
                        "fascia": fascia_elem.text if fascia_elem is not None else None,
                        "unita_misura": unita_elem.text if unita_elem is not None else None,
                    })

            if intervalli:
                result["intervalli"] = intervalli

            return result

    return None


def _parse_offerta_luce(offerta_elem: ET.Element) -> tuple[str, str, dict[str, float]] | None:
    """Parsea singola offerta luce da XML

    Args:
        offerta_elem: Elemento XML offerta

    Returns:
        Tupla (tipo_offerta, tipo_fascia, dati) dove:
        - tipo_offerta: "fissa" o "variabile"
        - tipo_fascia: "monoraria" o "trioraria"
        - dati: {"energia": float, "commercializzazione": float}

        None se offerta non è valida
    """
    # Verifica P.IVA Octopus
    piva_elem = offerta_elem.find(".//PIVA_UTENTE")
    if piva_elem is None or piva_elem.text != OCTOPUS_PIVA:
        return None

    # Determina tipo offerta (01=fissa, 02=variabile)
    tipo_offerta_elem = offerta_elem.find(".//TIPO_OFFERTA")
    if tipo_offerta_elem is None:
        return None

    tipo_offerta_code = tipo_offerta_elem.text
    tipo_offerta = "fissa" if tipo_offerta_code == "01" else "variabile"

    # Determina tipo fascia (01=monoraria, 03=trioraria)
    tipo_fascia_elem = offerta_elem.find(".//TIPOLOGIA_FASCE")
    if tipo_fascia_elem is None:
        return None

    tipo_fascia_code = tipo_fascia_elem.text
    tipo_fascia = "monoraria" if tipo_fascia_code == "01" else "trioraria"

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
        logger.warning(f"Energia non trovata per offerta {offerta_elem.find('.//NOME_OFFERTA').text}")
        return None

    # Log per debugging
    nome_offerta = offerta_elem.find(".//NOME_OFFERTA")
    nome = nome_offerta.text if nome_offerta is not None else "N/A"
    logger.info(f"✅ {nome} ({tipo_offerta} {tipo_fascia}): energia={energia} €/kWh, comm={commercializzazione} €/anno")

    return tipo_offerta, tipo_fascia, {
        "energia": energia,
        "commercializzazione": commercializzazione,
    }


def _parse_arera_xml(xml_content: str) -> dict[str, Any]:
    """Parsea XML ARERA ed estrae tariffe Octopus

    Args:
        xml_content: Contenuto XML come stringa

    Returns:
        Dict con struttura nested luce/gas → fissa/variabile → monoraria/trioraria
    """
    root = ET.fromstring(xml_content)

    # Rimuovi namespace per semplificare il parsing
    _remove_namespace(root)

    # Struttura dati per salvare tutte le tariffe
    tariffe_data = {
        "luce": {"fissa": {}, "variabile": {}},
        "gas": {"fissa": {}, "variabile": {}},
        "data_aggiornamento": datetime.now().strftime("%Y-%m-%d"),
    }

    # Trova tutte le offerte
    offerte = root.findall(".//offerta")

    logger.debug(f"Trovate {len(offerte)} offerte nel file XML")

    # Processa ogni offerta
    for offerta in offerte:
        parsed = _parse_offerta_luce(offerta)
        if parsed:
            tipo_offerta, tipo_fascia, dati = parsed
            tariffe_data["luce"][tipo_offerta][tipo_fascia] = dati

    return tariffe_data


def _save_cache(xml_content: str, source_date: datetime) -> None:
    """Salva il file XML in cache locale con metadata

    Args:
        xml_content: Contenuto XML da salvare
        source_date: Data del file XML (dalla URL ARERA)
    """
    # Salva XML
    LOCAL_XML_FILE.write_text(xml_content, encoding="utf-8")

    # Salva metadata
    metadata = {
        "source_date": source_date.strftime("%Y-%m-%d"),
        "download_date": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
    }
    CACHE_METADATA_FILE.write_text(json.dumps(metadata, indent=2), encoding="utf-8")

    logger.debug(f"Cache salvata: {LOCAL_XML_FILE}")


def _load_cache_metadata() -> dict[str, Any] | None:
    """Carica metadata del file in cache

    Returns:
        Dict con metadata o None se non esiste
    """
    if CACHE_METADATA_FILE.exists():
        return json.loads(CACHE_METADATA_FILE.read_text(encoding="utf-8"))
    return None


def _write_rates_file(file_path: Path, data: dict[str, Any]) -> None:
    """Helper sincrono per scrivere file JSON delle tariffe

    Args:
        file_path: Path del file da scrivere
        data: Dati da serializzare in JSON
    """
    with open(file_path, "w") as f:
        json.dump(data, f, indent=2)


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

    xml_content = None
    last_error = None
    source_date = None
    used_cache = False

    # Prova a scaricare il file, partendo da oggi e andando indietro fino a max_days_back
    for days_back in range(max_days_back + 1):
        try:
            target_date = datetime.now() - timedelta(days=days_back)
            url = _build_arera_url(target_date, "E")  # E = Elettrico

            date_str = target_date.strftime("%Y-%m-%d")
            if days_back == 0:
                logger.info(f"📄 Download dati da ARERA (data: {date_str})...")
            else:
                logger.info(f"📄 Tentativo con data precedente: {date_str}...")

            xml_content = await _download_xml(url)
            source_date = target_date
            logger.info(f"✅ File scaricato con successo (data: {date_str})")

            # Salva in cache
            await asyncio.to_thread(_save_cache, xml_content, source_date)
            break

        except httpx.HTTPStatusError as e:
            last_error = e
            if e.response.status_code == 403:
                logger.debug(f"⚠️  File non accessibile per data {target_date.strftime('%Y-%m-%d')} (403 Forbidden)")
            elif e.response.status_code == 404:
                logger.debug(f"⚠️  File non trovato per data {target_date.strftime('%Y-%m-%d')} (404 Not Found)")
            else:
                logger.debug(f"⚠️  Errore {e.response.status_code} per data {target_date.strftime('%Y-%m-%d')}")
            continue

    if xml_content is None:
        # Fallback: prova a leggere file locale
        if LOCAL_XML_FILE.exists():
            cache_metadata = _load_cache_metadata()
            cache_date_str = cache_metadata.get("source_date", "sconosciuta") if cache_metadata else "sconosciuta"

            logger.warning(
                f"⚠️  Download da ARERA fallito (provate {max_days_back + 1} date). "
                f"Uso file in cache (data: {cache_date_str})"
            )
            xml_content = LOCAL_XML_FILE.read_text(encoding="utf-8")
            used_cache = True

            # Usa la data dalla cache se disponibile
            if cache_metadata and cache_metadata.get("source_date"):
                source_date = datetime.strptime(cache_metadata["source_date"], "%Y-%m-%d")
        else:
            raise Exception(
                f"Impossibile scaricare file XML da ARERA e nessun file locale trovato. "
                f"Provato con le ultime {max_days_back + 1} date. "
                f"Ultimo errore: {last_error}. "
                f"\n\n"
                f"Il portale ARERA blocca gli accessi automatici. "
                f"Per usare questo lettore:\n"
                f"1. Scarica manualmente il file XML da: https://www.ilportaleofferte.it/portaleOfferte/\n"
                f"2. Salva il file in: {LOCAL_XML_FILE}\n"
                f"3. Riprova l'esecuzione"
            )

    try:

        # Parsea XML ed estrai tariffe Octopus
        logger.info("🔍 Parsing XML ed estrazione tariffe Octopus...")
        tariffe_data = _parse_arera_xml(xml_content)

        # Aggiungi metadata sul file sorgente
        if source_date:
            tariffe_data["data_fonte_xml"] = source_date.strftime("%Y-%m-%d")
        tariffe_data["used_cache"] = used_cache

        # Log warning se mancano tariffe
        warnings = []

        # Controlla se abbiamo almeno una tariffa luce
        has_luce = any([
            tariffe_data["luce"]["fissa"].get("monoraria"),
            tariffe_data["luce"]["variabile"].get("monoraria"),
            tariffe_data["luce"]["variabile"].get("trioraria"),
        ])
        if not has_luce:
            warnings.append("NESSUNA tariffa luce trovata")

        # Gas non ancora implementato
        if not tariffe_data["gas"]["fissa"] and not tariffe_data["gas"]["variabile"]:
            logger.debug("ℹ️  Tariffe gas non ancora implementate")

        if warnings:
            logger.warning(f"⚠️  {' | '.join(warnings)}")
            logger.warning("   Il checker non potrà confrontare queste categorie")

        # Salva risultati
        DATA_DIR.mkdir(exist_ok=True)
        await asyncio.to_thread(_write_rates_file, RATES_FILE, tariffe_data)

        # Calcola metriche
        duration = time.time() - start_time

        # Conta tariffe trovate
        rates_count = {
            "luce_fissa": bool(tariffe_data["luce"]["fissa"].get("monoraria")),
            "luce_var_mono": bool(tariffe_data["luce"]["variabile"].get("monoraria")),
            "luce_var_tri": bool(tariffe_data["luce"]["variabile"].get("trioraria")),
            "gas_fisso": bool(tariffe_data["gas"]["fissa"].get("monoraria")),
            "gas_var": bool(tariffe_data["gas"]["variabile"].get("monoraria")),
        }
        total_found = sum(rates_count.values())

        logger.info(f"✅ Lettura completata in {duration:.2f}s - Trovate {total_found}/5 tariffe")
        logger.info(f"💾 Tariffe salvate in {RATES_FILE}")

        return tariffe_data

    except httpx.HTTPError as e:
        duration = time.time() - start_time
        logger.error(f"🌐 Errore download dati ARERA dopo {duration:.2f}s: {e}")
        raise
    except ET.ParseError as e:
        duration = time.time() - start_time
        logger.error(f"❌ Errore parsing XML dopo {duration:.2f}s: {e}")
        raise
    except Exception as e:
        duration = time.time() - start_time
        logger.error(f"❌ Errore inatteso dopo {duration:.2f}s: {e}")
        raise


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
