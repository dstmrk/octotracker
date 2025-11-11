#!/usr/bin/env python3
"""
Scraper per estrarre tariffe Octopus Energy con Playwright

Estrae tariffe fisse e variabili per luce e gas:
- Luce fissa: prezzo fisso €/kWh
- Luce variabile mono: PUN Mono + spread
- Luce variabile multi: PUN + spread (F1, F2, F3)
- Gas fisso: prezzo fisso €/Smc
- Gas variabile: PSVDAm + spread

Struttura JSON salvata:
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
- La struttura a 3 livelli permette accesso diretto: luce/gas → fissa/variabile → monoraria/bioraria/trioraria
"""
import json
import logging
import os
import re
from datetime import datetime
from pathlib import Path
from typing import Any

from playwright.async_api import Error as PlaywrightError
from playwright.async_api import TimeoutError as PlaywrightTimeout
from playwright.async_api import async_playwright

# Setup logger
logger = logging.getLogger(__name__)

# Constants
OCTOPUS_TARIFFE_URL = "https://octopusenergy.it/le-nostre-tariffe"
PAGE_LOAD_TIMEOUT_MS = 60000  # Timeout caricamento pagina principale
JS_DYNAMIC_WAIT_MS = 5000  # Attesa caricamento contenuto JS dinamico
TOGGLE_CLICK_WAIT_MS = 1000  # Attesa dopo click toggle multioraria
TOGGLE_RESET_WAIT_MS = 500  # Attesa dopo reset toggle

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"

# Regex patterns pre-compilati per performance
LUCE_FISSA_PATTERN = re.compile(r"(?<!PUN\s)(?<!PUN Mono \+ )(\d+[.,]\d+)\s*€/kWh")
LUCE_VAR_MONO_PATTERN = re.compile(r"PUN Mono \+ (\d+[.,]\d+)\s*€/kWh")
LUCE_VAR_MULTI_PATTERN = re.compile(r"PUN \+ (\d+[.,]\d+)\s*€/kWh")
GAS_FISSO_PATTERN = re.compile(r"(?<!PSVDAm \+ )(\d+[.,]\d+)\s*€/Smc")
GAS_VAR_PATTERN = re.compile(r"PSVDAm \+ (\d+[.,]\d+)\s*€/Smc")
COMM_PATTERN = re.compile(r"(\d+)\s*€/anno")

# ========== HELPER FUNCTIONS ==========


def _extract_luce_fissa(clean_text: str) -> dict[str, float] | None:
    """Estrae tariffa luce fissa dal testo"""
    luce_fissa_match = LUCE_FISSA_PATTERN.search(clean_text)
    if luce_fissa_match:
        comm_match = COMM_PATTERN.search(clean_text)
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_fissa_match.group(1).replace(",", ".")),
            "commercializzazione": comm,
        }
        logger.info(f"✅ Luce fissa monoraria: {result['energia']} €/kWh, comm: {comm} €/anno")
        return result
    return None


def _extract_luce_variabile_mono(clean_text: str) -> dict[str, float] | None:
    """Estrae tariffa luce variabile monoraria dal testo"""
    luce_var_mono_match = LUCE_VAR_MONO_PATTERN.search(clean_text)
    if luce_var_mono_match:
        pun_mono_pos = clean_text.find("PUN Mono")
        comm_match = COMM_PATTERN.search(clean_text[pun_mono_pos : pun_mono_pos + 200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_var_mono_match.group(1).replace(",", ".")),
            "commercializzazione": comm,
        }
        logger.info(
            f"✅ Luce variabile monoraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno"
        )
        return result
    return None


async def _extract_luce_variabile_tri(page, clean_text: str) -> dict[str, float] | None:
    """Estrae tariffa luce variabile trioraria (con gestione toggle)"""
    luce_var_multi_match = LUCE_VAR_MULTI_PATTERN.search(clean_text)

    # Se non trovo "PUN +" ma trovo il toggle, faccio click
    if not luce_var_multi_match:
        toggle = await page.query_selector('input[type="checkbox"][role="switch"]')
        if toggle:
            logger.debug("🔄 Clic sul toggle per vedere tariffa multioraria...")
            await toggle.click()
            await page.wait_for_timeout(TOGGLE_CLICK_WAIT_MS)

            # Rileggi il testo
            text_after_toggle = await page.inner_text("body")
            clean_text_after = text_after_toggle.replace("\n", " ").replace("\r", " ")
            luce_var_multi_match = LUCE_VAR_MULTI_PATTERN.search(clean_text_after)

            if luce_var_multi_match:
                pun_pos = clean_text_after.find("PUN +")
                comm_match = COMM_PATTERN.search(clean_text_after[pun_pos : pun_pos + 200])
                comm = float(comm_match.group(1)) if comm_match else None

                result = {
                    "energia": float(luce_var_multi_match.group(1).replace(",", ".")),
                    "commercializzazione": comm,
                }
                logger.info(
                    f"✅ Luce variabile trioraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno"
                )

                # Riclicco per tornare allo stato iniziale
                await toggle.click()
                await page.wait_for_timeout(TOGGLE_RESET_WAIT_MS)
                return result
    else:
        # Trovata direttamente
        pun_pos = clean_text.find("PUN +")
        comm_match = COMM_PATTERN.search(clean_text[pun_pos : pun_pos + 200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_var_multi_match.group(1).replace(",", ".")),
            "commercializzazione": comm,
        }
        logger.info(
            f"✅ Luce variabile trioraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno"
        )
        return result

    return None


def _extract_gas_fisso(clean_text: str) -> dict[str, float] | None:
    """Estrae tariffa gas fisso dal testo"""
    gas_fisso_match = GAS_FISSO_PATTERN.search(clean_text)
    if gas_fisso_match:
        gas_pos = clean_text.lower().find("gas")
        comm_matches = COMM_PATTERN.findall(clean_text[gas_pos:])
        comm = float(comm_matches[-1]) if comm_matches else None

        result = {
            "energia": float(gas_fisso_match.group(1).replace(",", ".")),
            "commercializzazione": comm,
        }
        logger.info(f"✅ Gas fisso monorario: {result['energia']} €/Smc, comm: {comm} €/anno")
        return result
    return None


def _extract_gas_variabile(clean_text: str) -> dict[str, float] | None:
    """Estrae tariffa gas variabile dal testo"""
    gas_var_match = GAS_VAR_PATTERN.search(clean_text)
    if gas_var_match:
        psv_pos = clean_text.find("PSVDAm")
        comm_match = COMM_PATTERN.search(clean_text[psv_pos : psv_pos + 200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(gas_var_match.group(1).replace(",", ".")),
            "commercializzazione": comm,
        }
        logger.info(
            f"✅ Gas variabile monorario: PSV + {result['energia']} €/Smc, comm: {comm} €/anno"
        )
        return result
    return None


async def scrape_octopus_tariffe() -> dict[str, Any]:
    """Scrape tariffe fisse e variabili da Octopus Energy

    Returns:
        Dict con struttura nested luce/gas → fissa/variabile → monoraria/trioraria
    """
    logger.info("🔍 Avvio scraping tariffe Octopus Energy...")

    async with async_playwright() as p:
        # Avvia browser
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()

        try:
            # Vai alla pagina tariffe (domcontentloaded è più veloce di load)
            logger.info("📄 Caricamento pagina...")
            await page.goto(
                OCTOPUS_TARIFFE_URL, wait_until="domcontentloaded", timeout=PAGE_LOAD_TIMEOUT_MS
            )

            # Attendi dinamicamente che appaiano elementi chiave delle tariffe
            # Cerca almeno uno dei prezzi caratteristici (€/kWh, €/Smc, €/anno)
            try:
                await page.wait_for_selector(
                    "text=€/kWh, text=€/Smc, text=€/anno",  # Uno qualsiasi di questi
                    timeout=5000,
                    state="visible",
                )
                logger.debug("✅ Contenuto tariffe caricato dinamicamente")
            except PlaywrightTimeout:
                # Fallback: se i selettori non vengono trovati, usa wait ridotto
                logger.warning("⚠️  Selettori tariffe non trovati, uso wait ridotto da 5s a 2s")
                await page.wait_for_timeout(2000)

            # Estrai tutto il testo della pagina per analisi
            text = await page.inner_text("body")

            # Struttura dati per salvare tutte le tariffe
            tariffe_data = {
                "luce": {"fissa": {}, "variabile": {}},
                "gas": {"fissa": {}, "variabile": {}},
                "data_aggiornamento": datetime.now().strftime("%Y-%m-%d"),
            }

            # Normalizza testo per parsing
            clean_text = text.replace("\n", " ").replace("\r", " ")

            # ========== ESTRAZIONE TARIFFE ==========

            # Luce fissa
            luce_fissa = _extract_luce_fissa(clean_text)
            if luce_fissa:
                tariffe_data["luce"]["fissa"]["monoraria"] = luce_fissa

            # Luce variabile monoraria
            luce_var_mono = _extract_luce_variabile_mono(clean_text)
            if luce_var_mono:
                tariffe_data["luce"]["variabile"]["monoraria"] = luce_var_mono

            # Luce variabile trioraria (con gestione toggle)
            luce_var_tri = await _extract_luce_variabile_tri(page, clean_text)
            if luce_var_tri:
                tariffe_data["luce"]["variabile"]["trioraria"] = luce_var_tri

            # Gas fisso
            gas_fisso = _extract_gas_fisso(clean_text)
            if gas_fisso:
                tariffe_data["gas"]["fissa"]["monoraria"] = gas_fisso

            # Gas variabile
            gas_variabile = _extract_gas_variabile(clean_text)
            if gas_variabile:
                tariffe_data["gas"]["variabile"]["monoraria"] = gas_variabile

        except PlaywrightTimeout:
            logger.error("⏱️  Timeout durante scraping: la pagina non ha risposto in tempo")
            raise
        except PlaywrightError as e:
            logger.error(f"❌ Errore Playwright durante scraping: {e}")
            raise
        except ConnectionError as e:
            logger.error(f"🌐 Errore di connessione durante scraping: {e}")
            raise
        except Exception as e:
            logger.error(f"❌ Errore inatteso durante scraping: {e}")
            raise

        finally:
            await browser.close()

    # Log warning solo se manca un'intera categoria (luce o gas)
    warnings = []

    # Controlla se abbiamo almeno una tariffa luce
    has_luce = any(
        [
            tariffe_data["luce"]["fissa"].get("monoraria"),
            tariffe_data["luce"]["variabile"].get("monoraria"),
            tariffe_data["luce"]["variabile"].get("trioraria"),
        ]
    )
    if not has_luce:
        warnings.append("NESSUNA tariffa luce trovata")

    # Controlla se abbiamo almeno una tariffa gas
    has_gas = any(
        [
            tariffe_data["gas"]["fissa"].get("monoraria"),
            tariffe_data["gas"]["variabile"].get("monoraria"),
        ]
    )
    if not has_gas:
        warnings.append("NESSUNA tariffa gas trovata")

    if warnings:
        logger.warning(f"⚠️  {' | '.join(warnings)}")
        logger.warning("   Il checker non potrà confrontare queste categorie")

    # Salva risultati (anche se parziali)
    DATA_DIR.mkdir(exist_ok=True)
    with open(RATES_FILE, "w") as f:
        json.dump(tariffe_data, f, indent=2)

    logger.info(f"💾 Tariffe salvate in {RATES_FILE}")

    return tariffe_data


if __name__ == "__main__":
    import asyncio

    # Configura logging per esecuzione standalone (usa env var LOG_LEVEL)
    LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, LOG_LEVEL, logging.INFO),
        format="%(message)s",  # Formato semplice per output CLI
    )

    result = asyncio.run(scrape_octopus_tariffe())
    logger.info("📊 Tariffe estratte:")
    logger.info(json.dumps(result, indent=2))
