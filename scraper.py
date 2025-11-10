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
import re
from pathlib import Path
from datetime import datetime
from typing import Optional, Dict, Any
from playwright.async_api import async_playwright

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"

def extract_price(text: str) -> Optional[float]:
    """Estrae prezzo da testo (es: '0.123 €/kWh' -> 0.123)"""
    match = re.search(r'(\d+[.,]\d+)', text.replace(',', '.'))
    return float(match.group(1)) if match else None

# ========== HELPER FUNCTIONS ==========

def _extract_luce_fissa(clean_text: str) -> Optional[Dict[str, float]]:
    """Estrae tariffa luce fissa dal testo"""
    luce_fissa_match = re.search(r'(?<!PUN\s)(?<!PUN Mono \+ )(\d+[.,]\d+)\s*€/kWh', clean_text)
    if luce_fissa_match:
        comm_match = re.search(r'(\d+)\s*€/anno', clean_text)
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_fissa_match.group(1).replace(',', '.')),
            "commercializzazione": comm
        }
        print(f"✅ Luce fissa monoraria: {result['energia']} €/kWh, comm: {comm} €/anno")
        return result
    return None

def _extract_luce_variabile_mono(clean_text: str) -> Optional[Dict[str, float]]:
    """Estrae tariffa luce variabile monoraria dal testo"""
    luce_var_mono_match = re.search(r'PUN Mono \+ (\d+[.,]\d+)\s*€/kWh', clean_text)
    if luce_var_mono_match:
        pun_mono_pos = clean_text.find('PUN Mono')
        comm_match = re.search(r'(\d+)\s*€/anno', clean_text[pun_mono_pos:pun_mono_pos+200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_var_mono_match.group(1).replace(',', '.')),
            "commercializzazione": comm
        }
        print(f"✅ Luce variabile monoraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno")
        return result
    return None

async def _extract_luce_variabile_tri(page, clean_text: str) -> Optional[Dict[str, float]]:
    """Estrae tariffa luce variabile trioraria (con gestione toggle)"""
    luce_var_multi_match = re.search(r'PUN \+ (\d+[.,]\d+)\s*€/kWh', clean_text)

    # Se non trovo "PUN +" ma trovo il toggle, faccio click
    if not luce_var_multi_match:
        toggle = await page.query_selector('input[type="checkbox"][role="switch"]')
        if toggle:
            print("🔄 Clic sul toggle per vedere tariffa multioraria...")
            await toggle.click()
            await page.wait_for_timeout(1000)

            # Rileggi il testo
            text_after_toggle = await page.inner_text('body')
            clean_text_after = text_after_toggle.replace('\n', ' ').replace('\r', ' ')
            luce_var_multi_match = re.search(r'PUN \+ (\d+[.,]\d+)\s*€/kWh', clean_text_after)

            if luce_var_multi_match:
                pun_pos = clean_text_after.find('PUN +')
                comm_match = re.search(r'(\d+)\s*€/anno', clean_text_after[pun_pos:pun_pos+200])
                comm = float(comm_match.group(1)) if comm_match else None

                result = {
                    "energia": float(luce_var_multi_match.group(1).replace(',', '.')),
                    "commercializzazione": comm
                }
                print(f"✅ Luce variabile trioraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno")

                # Riclicco per tornare allo stato iniziale
                await toggle.click()
                await page.wait_for_timeout(500)
                return result
    else:
        # Trovata direttamente
        pun_pos = clean_text.find('PUN +')
        comm_match = re.search(r'(\d+)\s*€/anno', clean_text[pun_pos:pun_pos+200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(luce_var_multi_match.group(1).replace(',', '.')),
            "commercializzazione": comm
        }
        print(f"✅ Luce variabile trioraria: PUN + {result['energia']} €/kWh, comm: {comm} €/anno")
        return result

    return None

def _extract_gas_fisso(clean_text: str) -> Optional[Dict[str, float]]:
    """Estrae tariffa gas fisso dal testo"""
    gas_fisso_match = re.search(r'(?<!PSVDAm \+ )(\d+[.,]\d+)\s*€/Smc', clean_text)
    if gas_fisso_match:
        gas_pos = clean_text.lower().find('gas')
        comm_matches = re.findall(r'(\d+)\s*€/anno', clean_text[gas_pos:])
        comm = float(comm_matches[-1]) if comm_matches else None

        result = {
            "energia": float(gas_fisso_match.group(1).replace(',', '.')),
            "commercializzazione": comm
        }
        print(f"✅ Gas fisso monorario: {result['energia']} €/Smc, comm: {comm} €/anno")
        return result
    return None

def _extract_gas_variabile(clean_text: str) -> Optional[Dict[str, float]]:
    """Estrae tariffa gas variabile dal testo"""
    gas_var_match = re.search(r'PSVDAm \+ (\d+[.,]\d+)\s*€/Smc', clean_text)
    if gas_var_match:
        psv_pos = clean_text.find('PSVDAm')
        comm_match = re.search(r'(\d+)\s*€/anno', clean_text[psv_pos:psv_pos+200])
        comm = float(comm_match.group(1)) if comm_match else None

        result = {
            "energia": float(gas_var_match.group(1).replace(',', '.')),
            "commercializzazione": comm
        }
        print(f"✅ Gas variabile monorario: PSV + {result['energia']} €/Smc, comm: {comm} €/anno")
        return result
    return None

async def scrape_octopus_tariffe() -> Dict[str, Any]:
    """Scrape tariffe fisse e variabili da Octopus Energy

    Returns:
        Dict con struttura nested luce/gas → fissa/variabile → monoraria/trioraria
    """
    print("🔍 Avvio scraping tariffe Octopus Energy...")

    async with async_playwright() as p:
        # Avvia browser
        browser = await p.chromium.launch(headless=True)
        page = await browser.new_page()

        try:
            # Vai alla pagina tariffe
            print("📄 Caricamento pagina...")
            await page.goto('https://octopusenergy.it/le-nostre-tariffe', wait_until='load', timeout=60000)

            # Attendi caricamento contenuto aggiuntivo (per JS dinamico)
            await page.wait_for_timeout(5000)

            # Estrai tutto il testo della pagina per analisi
            text = await page.inner_text('body')

            # Struttura dati per salvare tutte le tariffe
            tariffe_data = {
                "luce": {
                    "fissa": {},
                    "variabile": {}
                },
                "gas": {
                    "fissa": {},
                    "variabile": {}
                },
                "data_aggiornamento": datetime.now().strftime("%Y-%m-%d")
            }

            # Normalizza testo per parsing
            clean_text = text.replace('\n', ' ').replace('\r', ' ')

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

        except Exception as e:
            print(f"❌ Errore durante scraping: {e}")
            raise

        finally:
            await browser.close()

    # Log warning solo se manca un'intera categoria (luce o gas)
    warnings = []

    # Controlla se abbiamo almeno una tariffa luce
    has_luce = any([
        tariffe_data["luce"]["fissa"].get("monoraria"),
        tariffe_data["luce"]["variabile"].get("monoraria"),
        tariffe_data["luce"]["variabile"].get("trioraria")
    ])
    if not has_luce:
        warnings.append("NESSUNA tariffa luce trovata")

    # Controlla se abbiamo almeno una tariffa gas
    has_gas = any([
        tariffe_data["gas"]["fissa"].get("monoraria"),
        tariffe_data["gas"]["variabile"].get("monoraria")
    ])
    if not has_gas:
        warnings.append("NESSUNA tariffa gas trovata")

    if warnings:
        print(f"⚠️  {' | '.join(warnings)}")
        print(f"   Il checker non potrà confrontare queste categorie")

    # Salva risultati (anche se parziali)
    DATA_DIR.mkdir(exist_ok=True)
    with open(RATES_FILE, 'w') as f:
        json.dump(tariffe_data, f, indent=2)

    print(f"💾 Tariffe salvate in {RATES_FILE}")

    return tariffe_data

if __name__ == '__main__':
    import asyncio
    result = asyncio.run(scrape_octopus_tariffe())
    print("\n📊 Tariffe estratte:")
    print(json.dumps(result, indent=2))
