#!/usr/bin/env python3
"""
Scraper per estrarre tariffe Octopus Energy con Playwright
"""
import json
import re
from pathlib import Path
from datetime import datetime
from playwright.sync_api import sync_playwright

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"

def extract_price(text):
    """Estrae prezzo da testo (es: '0.123 €/kWh' -> 0.123)"""
    match = re.search(r'(\d+[.,]\d+)', text.replace(',', '.'))
    return float(match.group(1)) if match else None

def scrape_octopus_tariffe():
    """Scrape tariffe mono-orarie fisse da Octopus Energy"""
    print("🔍 Avvio scraping tariffe Octopus Energy...")

    with sync_playwright() as p:
        # Avvia browser
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()

        try:
            # Vai alla pagina tariffe
            print("📄 Caricamento pagina...")
            page.goto('https://octopusenergy.it/le-nostre-tariffe', wait_until='networkidle', timeout=30000)

            # Attendi caricamento contenuto
            page.wait_for_timeout(3000)

            # Estrai tutto il testo della pagina per analisi
            content = page.content()
            text = page.inner_text('body')

            # Cerca pattern per tariffe mono-orarie fisse
            # Questo è un approccio generico - potrebbe necessitare aggiustamenti
            tariffe_data = {
                "luce": None,
                "gas": None,
                "data_aggiornamento": datetime.now().strftime("%Y-%m-%d")
            }

            # Cerca sezioni luce e gas
            # Cerca pattern comuni: "€/kWh", "€/Smc", "€/mese"

            # Strategia: cerca elementi che contengono prezzi
            # Pattern tipico: numero + €/kWh o €/Smc per energia, numero + €/mese per commercializzazione

            # Estrai tutti i prezzi dalla pagina
            energia_luce_match = re.search(r'(\d+[.,]\d+)\s*€\s*/\s*kWh', text.replace('\n', ' '))

            # Cerca commercializzazione luce (può essere in €/mese o €/anno)
            comm_luce_mese_match = re.search(r'(\d+[.,]\d+)\s*€\s*/\s*mese', text.replace('\n', ' '))
            comm_luce_anno_match = re.search(r'(\d+[.,]\d+)\s*€\s*/\s*anno', text.replace('\n', ' '))

            energia_gas_match = re.search(r'(\d+[.,]\d+)\s*€\s*/\s*Smc', text.replace('\n', ' '))

            # Prova anche pattern alternativi
            if not energia_luce_match:
                # Cerca pattern tipo "0,123 €/kWh" o "0.123€/kWh"
                energia_luce_match = re.search(r'(\d+[.,]\d+)\s*€?\s*kWh', text.replace('\n', ' '))

            # Calcola commercializzazione luce in €/anno
            comm_luce_anno = None
            if comm_luce_anno_match:
                comm_luce_anno = float(comm_luce_anno_match.group(1).replace(',', '.'))
            elif comm_luce_mese_match:
                # Converti da €/mese a €/anno
                comm_luce_anno = float(comm_luce_mese_match.group(1).replace(',', '.')) * 12
                print(f"ℹ️  Commercializzazione luce convertita da €/mese a €/anno")

            if energia_luce_match:
                tariffe_data["luce"] = {
                    "energia": float(energia_luce_match.group(1).replace(',', '.')),
                    "commercializzazione": comm_luce_anno,
                    "nome_tariffa": "Mono-oraria Fissa"
                }
                print(f"✅ Luce trovata: €{tariffe_data['luce']['energia']:.3f}/kWh, comm: €{comm_luce_anno:.2f}/anno" if comm_luce_anno else f"✅ Luce trovata: €{tariffe_data['luce']['energia']:.3f}/kWh")

            if energia_gas_match:
                # Per gas, cerca commercializzazione (può essere in €/mese o €/anno)
                all_mese = re.findall(r'(\d+[.,]\d+)\s*€\s*/\s*mese', text.replace('\n', ' '))
                all_anno = re.findall(r'(\d+[.,]\d+)\s*€\s*/\s*anno', text.replace('\n', ' '))

                comm_gas_anno = None
                if all_anno and len(all_anno) > 1:
                    # Seconda occorrenza di €/anno (prima è luce)
                    comm_gas_anno = float(all_anno[1].replace(',', '.'))
                elif all_mese and len(all_mese) > 1:
                    # Seconda occorrenza di €/mese (prima è luce) - converti
                    comm_gas_anno = float(all_mese[1].replace(',', '.')) * 12
                    print(f"ℹ️  Commercializzazione gas convertita da €/mese a €/anno")
                elif all_mese:
                    # Solo una occorrenza di €/mese - probabilmente è gas
                    comm_gas_anno = float(all_mese[0].replace(',', '.')) * 12
                    print(f"ℹ️  Commercializzazione gas convertita da €/mese a €/anno")

                tariffe_data["gas"] = {
                    "energia": float(energia_gas_match.group(1).replace(',', '.')),
                    "commercializzazione": comm_gas_anno,
                    "nome_tariffa": "Mono-oraria Fissa"
                }
                print(f"✅ Gas trovato: €{tariffe_data['gas']['energia']:.3f}/Smc, comm: €{comm_gas_anno:.2f}/anno" if comm_gas_anno else f"✅ Gas trovato: €{tariffe_data['gas']['energia']:.3f}/Smc")

            # Se non troviamo nulla con regex, proviamo a cercare elementi specifici
            if not tariffe_data["luce"] or not tariffe_data["gas"]:
                print("⚠️  Pattern regex non hanno trovato tutto, provo con selettori...")

                # Cerca carte/sezioni tariffe
                cards = page.query_selector_all('[class*="card"], [class*="tariffa"], [class*="price"]')

                for card in cards:
                    card_text = card.inner_text()

                    # Cerca luce
                    if 'luce' in card_text.lower() or 'elettric' in card_text.lower():
                        energia_match = re.search(r'(\d+[.,]\d+).*?kWh', card_text, re.IGNORECASE)
                        comm_mese_match = re.search(r'(\d+[.,]\d+).*?€\s*/\s*mese', card_text, re.IGNORECASE)
                        comm_anno_match = re.search(r'(\d+[.,]\d+).*?€\s*/\s*anno', card_text, re.IGNORECASE)

                        if energia_match and not tariffe_data["luce"]:
                            # Calcola commercializzazione in €/anno
                            comm_anno = None
                            if comm_anno_match:
                                comm_anno = float(comm_anno_match.group(1).replace(',', '.'))
                            elif comm_mese_match:
                                comm_anno = float(comm_mese_match.group(1).replace(',', '.')) * 12
                                print(f"ℹ️  Commercializzazione luce convertita da €/mese a €/anno (da card)")

                            tariffe_data["luce"] = {
                                "energia": float(energia_match.group(1).replace(',', '.')),
                                "commercializzazione": comm_anno,
                                "nome_tariffa": "Mono-oraria Fissa"
                            }
                            print(f"✅ Luce trovata (da card): €{tariffe_data['luce']['energia']:.3f}/kWh")

                    # Cerca gas
                    if 'gas' in card_text.lower():
                        energia_match = re.search(r'(\d+[.,]\d+).*?Smc', card_text, re.IGNORECASE)
                        comm_mese_match = re.search(r'(\d+[.,]\d+).*?€\s*/\s*mese', card_text, re.IGNORECASE)
                        comm_anno_match = re.search(r'(\d+[.,]\d+).*?€\s*/\s*anno', card_text, re.IGNORECASE)

                        if energia_match and not tariffe_data["gas"]:
                            # Calcola commercializzazione in €/anno
                            comm_anno = None
                            if comm_anno_match:
                                comm_anno = float(comm_anno_match.group(1).replace(',', '.'))
                            elif comm_mese_match:
                                comm_anno = float(comm_mese_match.group(1).replace(',', '.')) * 12
                                print(f"ℹ️  Commercializzazione gas convertita da €/mese a €/anno (da card)")

                            tariffe_data["gas"] = {
                                "energia": float(energia_match.group(1).replace(',', '.')),
                                "commercializzazione": comm_anno,
                                "nome_tariffa": "Mono-oraria Fissa"
                            }
                            print(f"✅ Gas trovato (da card): €{tariffe_data['gas']['energia']:.3f}/Smc")

            # Salva screenshot per debug
            screenshot_path = DATA_DIR / "last_scrape.png"
            page.screenshot(path=str(screenshot_path))
            print(f"📸 Screenshot salvato: {screenshot_path}")

        except Exception as e:
            print(f"❌ Errore durante scraping: {e}")
            raise

        finally:
            browser.close()

    # Salva risultati
    DATA_DIR.mkdir(exist_ok=True)
    with open(RATES_FILE, 'w') as f:
        json.dump(tariffe_data, f, indent=2)

    print(f"💾 Tariffe salvate in {RATES_FILE}")

    return tariffe_data

if __name__ == '__main__':
    result = scrape_octopus_tariffe()
    print("\n📊 Tariffe estratte:")
    print(json.dumps(result, indent=2))
