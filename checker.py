#!/usr/bin/env python3
"""
Controlla se ci sono tariffe più convenienti e notifica gli utenti
"""
import asyncio
import json
import logging
import os
from pathlib import Path
from typing import Any

from dotenv import load_dotenv
from telegram import Bot
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut

from database import load_users, save_user

load_dotenv()

# Setup logger
logger = logging.getLogger(__name__)

# Constants
MAX_DECIMALS_ENERGY = 4  # Per prezzi energia e spread (es. 0.0088 €/kWh)
MAX_DECIMALS_COST = 2  # Per costi commercializzazione (€/anno)

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"


def load_json(file_path: Path) -> dict[str, Any] | None:
    """Carica file JSON con gestione errori"""
    if file_path.exists():
        try:
            with open(file_path) as f:
                content = f.read()
                if not content.strip():
                    logger.warning(f"⚠️  {file_path.name} è vuoto")
                    return None
                return json.loads(content)
        except json.JSONDecodeError as e:
            logger.error(f"❌ Errore parsing {file_path.name}: {e}")
            logger.debug(f"   File location: {file_path}")
            # Mostra prime righe del file per debug
            try:
                with open(file_path) as f:
                    first_lines = f.read(200)
                    logger.debug(f"   Prime righe: {repr(first_lines)}")
            except (OSError, PermissionError):
                pass  # Debug read fallito, non critico
            return None
        except FileNotFoundError:
            logger.warning(f"📁 File non trovato: {file_path.name}")
            return None
        except PermissionError:
            logger.error(f"🔒 Permesso negato per leggere: {file_path.name}")
            return None
        except OSError as e:
            logger.error(f"💾 Errore I/O lettura {file_path.name}: {e}")
            return None
    return None


def format_number(value: float, max_decimals: int = 3) -> str:
    """
    Formatta numero con logica intelligente per i decimali:
    - Se intero (es. 72.0) → "72" (nessun decimale)
    - Se ha decimali → mostra almeno 2 decimali, rimuovi zeri trailing oltre il secondo
    Usa virgola come separatore decimale (stile italiano)

    Esempi:
    - 72.0 → "72"
    - 72.5 → "72,50"
    - 0.145 → "0,145"
    - 0.140 → "0,14"
    - 0.100 → "0,10"
    """
    # Arrotonda al massimo di decimali
    rounded = round(value, max_decimals)

    # Controlla se è un numero intero
    if rounded == int(rounded):
        return str(int(rounded))

    # Ha decimali: formatta con max decimali e poi sistema
    formatted = f"{rounded:.{max_decimals}f}"

    # Rimuovi zeri trailing
    formatted = formatted.rstrip("0")

    # Assicurati di avere almeno 2 decimali se ci sono decimali
    parts = formatted.split(".")
    if len(parts) > 1 and len(parts[1]) < 2:
        parts[1] = parts[1].ljust(2, "0")
        formatted = ".".join(parts)

    # Sostituisci punto con virgola (stile italiano)
    return formatted.replace(".", ",")


def check_better_rates(user_rates: dict[str, Any], current_rates: dict[str, Any]) -> dict[str, Any]:
    """
    Confronta tariffe utente con tariffe attuali dello stesso tipo
    Ritorna dizionario con risparmi e peggioramenti trovati
    """
    savings = {
        "luce_energia": None,
        "luce_comm": None,
        "gas_energia": None,
        "gas_comm": None,
        "luce_energia_worse": False,
        "luce_comm_worse": False,
        "gas_energia_worse": False,
        "gas_comm_worse": False,
        "has_savings": False,
        "is_mixed": False,
        "luce_tipo": user_rates["luce"]["tipo"],
        "luce_fascia": user_rates["luce"]["fascia"],
        "gas_tipo": user_rates["gas"]["tipo"] if user_rates.get("gas") else None,
        "gas_fascia": user_rates["gas"]["fascia"] if user_rates.get("gas") else None,
    }

    # Accesso diretto con struttura nested
    luce_tipo = user_rates["luce"]["tipo"]
    luce_fascia = user_rates["luce"]["fascia"]

    # Controlla luce (accesso diretto alla tariffa)
    if current_rates.get("luce", {}).get(luce_tipo, {}).get(luce_fascia):
        luce_rate = current_rates["luce"][luce_tipo][luce_fascia]

        if luce_rate.get("energia") is not None:
            if luce_rate["energia"] < user_rates["luce"]["energia"]:
                savings["luce_energia"] = {
                    "attuale": user_rates["luce"]["energia"],
                    "nuova": luce_rate["energia"],
                    "risparmio": user_rates["luce"]["energia"] - luce_rate["energia"],
                }
                savings["has_savings"] = True
            elif luce_rate["energia"] > user_rates["luce"]["energia"]:
                savings["luce_energia_worse"] = True

        if luce_rate.get("commercializzazione") is not None:
            if luce_rate["commercializzazione"] < user_rates["luce"]["commercializzazione"]:
                savings["luce_comm"] = {
                    "attuale": user_rates["luce"]["commercializzazione"],
                    "nuova": luce_rate["commercializzazione"],
                    "risparmio": user_rates["luce"]["commercializzazione"]
                    - luce_rate["commercializzazione"],
                }
                savings["has_savings"] = True
            elif luce_rate["commercializzazione"] > user_rates["luce"]["commercializzazione"]:
                savings["luce_comm_worse"] = True

    # Controlla gas (solo se l'utente ha il gas)
    if user_rates.get("gas") is not None:
        gas_tipo = user_rates["gas"]["tipo"]
        gas_fascia = user_rates["gas"]["fascia"]

        if current_rates.get("gas", {}).get(gas_tipo, {}).get(gas_fascia):
            gas_rate = current_rates["gas"][gas_tipo][gas_fascia]

            if gas_rate.get("energia") is not None:
                if gas_rate["energia"] < user_rates["gas"]["energia"]:
                    savings["gas_energia"] = {
                        "attuale": user_rates["gas"]["energia"],
                        "nuova": gas_rate["energia"],
                        "risparmio": user_rates["gas"]["energia"] - gas_rate["energia"],
                    }
                    savings["has_savings"] = True
                elif gas_rate["energia"] > user_rates["gas"]["energia"]:
                    savings["gas_energia_worse"] = True

            if gas_rate.get("commercializzazione") is not None:
                if gas_rate["commercializzazione"] < user_rates["gas"]["commercializzazione"]:
                    savings["gas_comm"] = {
                        "attuale": user_rates["gas"]["commercializzazione"],
                        "nuova": gas_rate["commercializzazione"],
                        "risparmio": user_rates["gas"]["commercializzazione"]
                        - gas_rate["commercializzazione"],
                    }
                    savings["has_savings"] = True
                elif gas_rate["commercializzazione"] > user_rates["gas"]["commercializzazione"]:
                    savings["gas_comm_worse"] = True

    # Determina se è un caso "mixed" (una componente migliora, l'altra peggiora)
    # Per luce
    luce_has_improvement = savings["luce_energia"] or savings["luce_comm"]
    luce_has_worsening = savings["luce_energia_worse"] or savings["luce_comm_worse"]

    # Per gas
    gas_has_improvement = savings["gas_energia"] or savings["gas_comm"]
    gas_has_worsening = savings["gas_energia_worse"] or savings["gas_comm_worse"]

    # È mixed se almeno una componente (luce o gas) ha sia miglioramenti che peggioramenti
    if (luce_has_improvement and luce_has_worsening) or (gas_has_improvement and gas_has_worsening):
        savings["is_mixed"] = True

    return savings


# ========== HELPER FUNCTIONS ==========


def _format_header(is_mixed: bool) -> str:
    """Formatta intestazione notifica"""
    if is_mixed:
        header = "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n"
        header += "OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n"
    else:
        header = "⚡️ <b>Buone notizie!</b>\n"
        header += "OctoTracker ha trovato una tariffa Octopus Energy più conveniente rispetto a quella che hai attiva.\n\n"
    return header


def _format_luce_section(
    savings: dict[str, Any], user_rates: dict[str, Any], current_rates: dict[str, Any]
) -> str:
    """Formatta sezione luce della notifica"""
    if not (savings["luce_energia"] or savings["luce_comm"]):
        return ""

    luce_tipo = savings["luce_tipo"]
    luce_fascia = savings["luce_fascia"]
    tipo_display = f"{luce_tipo.capitalize()} {luce_fascia.capitalize()}"

    # Determina label in base al tipo
    luce_label = "Prezzo fisso" if luce_tipo == "fissa" else "Spread (PUN +)"

    section = f"💡 <b>Luce ({tipo_display}):</b>\n"

    # Formatta energia con max_decimals=4 per spread
    user_energia = format_number(user_rates["luce"]["energia"], max_decimals=MAX_DECIMALS_ENERGY)
    user_comm = format_number(
        user_rates["luce"]["commercializzazione"], max_decimals=MAX_DECIMALS_COST
    )
    section += f"Tua tariffa: {luce_label} {user_energia} €/kWh, Comm. {user_comm} €/anno\n"

    # Accesso diretto nested
    if current_rates.get("luce", {}).get(luce_tipo, {}).get(luce_fascia):
        energia_new = current_rates["luce"][luce_tipo][luce_fascia]["energia"]
        comm_new = current_rates["luce"][luce_tipo][luce_fascia]["commercializzazione"]

        energia_formatted = format_number(energia_new, max_decimals=MAX_DECIMALS_ENERGY)
        comm_formatted = format_number(comm_new, max_decimals=MAX_DECIMALS_COST)

        if savings["luce_energia"]:
            energia_str = f"<b>{energia_formatted} €/kWh</b>"
        elif savings["luce_energia_worse"]:
            energia_str = f"<u>{energia_formatted} €/kWh</u>"
        else:
            energia_str = f"{energia_formatted} €/kWh"

        if savings["luce_comm"]:
            comm_str = f"<b>{comm_formatted} €/anno</b>"
        elif savings["luce_comm_worse"]:
            comm_str = f"<u>{comm_formatted} €/anno</u>"
        else:
            comm_str = f"{comm_formatted} €/anno"

        section += f"Nuova tariffa: {luce_label} {energia_str}, Comm. {comm_str}\n\n"

    return section


def _format_gas_section(
    savings: dict[str, Any], user_rates: dict[str, Any], current_rates: dict[str, Any]
) -> str:
    """Formatta sezione gas della notifica"""
    if user_rates.get("gas") is None or not (savings["gas_energia"] or savings["gas_comm"]):
        return ""

    gas_tipo = savings["gas_tipo"]
    gas_fascia = savings["gas_fascia"]
    tipo_display = f"{gas_tipo.capitalize()} {gas_fascia.capitalize()}"

    # Determina label in base al tipo
    gas_label = "Prezzo fisso" if gas_tipo == "fissa" else "Spread (PSV +)"

    section = f"🔥 <b>Gas ({tipo_display}):</b>\n"

    # Formatta energia con max_decimals=4 per spread
    user_gas_energia = format_number(user_rates["gas"]["energia"], max_decimals=MAX_DECIMALS_ENERGY)
    user_gas_comm = format_number(
        user_rates["gas"]["commercializzazione"], max_decimals=MAX_DECIMALS_COST
    )
    section += f"Tua tariffa: {gas_label} {user_gas_energia} €/Smc, Comm. {user_gas_comm} €/anno\n"

    # Accesso diretto nested
    if current_rates.get("gas", {}).get(gas_tipo, {}).get(gas_fascia):
        energia_new = current_rates["gas"][gas_tipo][gas_fascia]["energia"]
        comm_new = current_rates["gas"][gas_tipo][gas_fascia]["commercializzazione"]

        energia_formatted = format_number(energia_new, max_decimals=MAX_DECIMALS_ENERGY)
        comm_formatted = format_number(comm_new, max_decimals=MAX_DECIMALS_COST)

        if savings["gas_energia"]:
            energia_str = f"<b>{energia_formatted} €/Smc</b>"
        elif savings["gas_energia_worse"]:
            energia_str = f"<u>{energia_formatted} €/Smc</u>"
        else:
            energia_str = f"{energia_formatted} €/Smc"

        if savings["gas_comm"]:
            comm_str = f"<b>{comm_formatted} €/anno</b>"
        elif savings["gas_comm_worse"]:
            comm_str = f"<u>{comm_formatted} €/anno</u>"
        else:
            comm_str = f"{comm_formatted} €/anno"

        section += f"Nuova tariffa: {gas_label} {energia_str}, Comm. {comm_str}\n\n"

    return section


def _format_footer(is_mixed: bool) -> str:
    """Formatta footer notifica"""
    footer = ""

    # Footer diverso per caso mixed
    if is_mixed:
        footer += "📊 In questi casi la convenienza dipende dai tuoi consumi.\n"
        footer += "Ti consiglio di fare una verifica in base ai kWh/Smc che usi mediamente ogni anno, puoi trovare i dati nelle tue bollette.\n\n"

    footer += "🔧 Se vuoi aggiornare le tariffe che hai registrato, puoi farlo in qualsiasi momento con il comando /update.\n\n"
    footer += "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n"
    footer += "☕️ Se pensi che questo bot ti sia utile, puoi offrirmi un caffè su ko-fi.com/dstmrk — grazie di cuore! 💙"

    return footer


def format_notification(
    savings: dict[str, Any], user_rates: dict[str, Any], current_rates: dict[str, Any]
) -> str:
    """Formatta messaggio di notifica"""
    message = _format_header(savings["is_mixed"])
    message += _format_luce_section(savings, user_rates, current_rates)
    message += _format_gas_section(savings, user_rates, current_rates)
    message += _format_footer(savings["is_mixed"])
    return message


async def send_notification(bot: Bot, user_id: str, message: str) -> bool:
    """Invia notifica Telegram"""
    try:
        await bot.send_message(chat_id=user_id, text=message, parse_mode="HTML")
        return True
    except RetryAfter as e:
        logger.warning(f"⏱️  Rate limit per utente {user_id}: riprova tra {e.retry_after}s")
        return False
    except TimedOut:
        logger.error(f"⏱️  Timeout invio messaggio a {user_id}")
        return False
    except NetworkError as e:
        logger.error(f"🌐 Errore di rete invio messaggio a {user_id}: {e}")
        return False
    except TelegramError as e:
        logger.error(f"❌ Errore Telegram invio messaggio a {user_id}: {e}")
        return False


async def check_and_notify_users(bot_token: str) -> None:
    """Controlla tariffe e invia notifiche in parallelo (chiamata da bot.py)"""
    logger.info("🔍 Inizio controllo tariffe...")

    # Carica dati
    users = load_users()  # Da database SQLite
    current_rates = load_json(RATES_FILE)  # Da JSON (nessuna race condition)

    if not users:
        logger.warning("⚠️  Nessun utente registrato")
        return

    if not current_rates:
        logger.error("❌ Nessuna tariffa disponibile. Esegui prima scraper.py")
        return

    # Inizializza bot
    bot = Bot(token=bot_token)

    # ========== FASE 1: Prepara tutte le notifiche ==========
    notifications_to_send = []

    for user_id, user_rates in users.items():
        logger.info(f"📊 Controllo utente {user_id}...")

        savings = check_better_rates(user_rates, current_rates)

        if savings["has_savings"]:
            # Costruisci oggetto con tariffe Octopus attuali (struttura nested)
            current_octopus = {}

            # Luce: accesso diretto
            luce_tipo = user_rates["luce"]["tipo"]
            luce_fascia = user_rates["luce"]["fascia"]

            if current_rates.get("luce", {}).get(luce_tipo, {}).get(luce_fascia):
                current_octopus["luce"] = {
                    "energia": current_rates["luce"][luce_tipo][luce_fascia]["energia"],
                    "commercializzazione": current_rates["luce"][luce_tipo][luce_fascia][
                        "commercializzazione"
                    ],
                }

            # Gas: aggiungi solo se l'utente ce l'ha
            if user_rates.get("gas") is not None:
                gas_tipo = user_rates["gas"]["tipo"]
                gas_fascia = user_rates["gas"]["fascia"]

                if current_rates.get("gas", {}).get(gas_tipo, {}).get(gas_fascia):
                    current_octopus["gas"] = {
                        "energia": current_rates["gas"][gas_tipo][gas_fascia]["energia"],
                        "commercializzazione": current_rates["gas"][gas_tipo][gas_fascia][
                            "commercializzazione"
                        ],
                    }

            # Controlla se abbiamo già notificato queste stesse tariffe
            last_notified = user_rates.get("last_notified_rates", {})

            if last_notified == current_octopus:
                logger.info("  ⏭️  Tariffe migliori già notificate in precedenza, skip")
            else:
                # Tariffe diverse o prima notifica - aggiungi alla coda
                message = format_notification(savings, user_rates, current_rates)
                notifications_to_send.append((user_id, user_rates, current_octopus, message))
                logger.info("  📤 Notifica accodata per invio")
        else:
            logger.info("  ℹ️  Nessun risparmio trovato")

    # ========== FASE 2: Invia notifiche in parallelo con rate limiting ==========
    if notifications_to_send:
        logger.info(
            f"📨 Invio {len(notifications_to_send)} notifiche in parallelo (max 10 simultanee)..."
        )

        # Semaphore per limitare richieste concorrenti (rispetta rate limits Telegram)
        semaphore = asyncio.Semaphore(10)

        async def send_with_limit(
            user_id: str, user_rates: dict, current_octopus: dict, message: str
        ) -> bool:
            """Invia notifica con rate limiting"""
            async with semaphore:
                success = await send_notification(bot, user_id, message)
                if success:
                    # Aggiorna last_notified_rates per questo utente
                    user_rates["last_notified_rates"] = current_octopus
                    save_user(user_id, user_rates)
                    logger.info(f"  ✅ Notifica inviata a {user_id}")
                    return True
                else:
                    logger.warning(f"  ❌ Notifica fallita per {user_id}")
                    return False

        # Crea task per tutte le notifiche
        tasks = [
            send_with_limit(user_id, user_rates, current_octopus, message)
            for user_id, user_rates, current_octopus, message in notifications_to_send
        ]

        # Esegui tutte le notifiche in parallelo (con semaphore che limita a 10 simultanee)
        results = await asyncio.gather(*tasks, return_exceptions=True)

        # Conta successi (ignora eccezioni)
        notifications_sent = sum(1 for r in results if r is True)
    else:
        notifications_sent = 0

    logger.info(f"✅ Controllo completato. Notifiche inviate: {notifications_sent}/{len(users)}")


async def main() -> None:
    """Main per esecuzione standalone"""
    token = os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")
    await check_and_notify_users(token)


if __name__ == "__main__":
    import asyncio

    # Configura logging per esecuzione standalone (usa env var LOG_LEVEL)
    LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, LOG_LEVEL, logging.INFO),
        format="%(asctime)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    asyncio.run(main())
