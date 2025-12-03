#!/usr/bin/env python3
"""
Controlla se ci sono tariffe più convenienti e notifica gli utenti
"""
import asyncio
import json
import logging
import os
import time
from pathlib import Path
from typing import Any

from dotenv import load_dotenv
from telegram import Bot
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut

from constants import MAX_DECIMALS_COST, MAX_DECIMALS_ENERGY
from database import load_users, save_user
from formatters import format_utility_type_display, get_utility_label

load_dotenv()

# Setup logger
logger = logging.getLogger(__name__)

# File dati
DATA_DIR = Path(__file__).parent / "data"
RATES_FILE = DATA_DIR / "current_rates.json"

# Errori Telegram che indicano che l'utente deve essere rimosso dal database
TELEGRAM_ERRORS_TO_DELETE = [
    "bot was blocked by the user",
    "user is deactivated",  # Account Telegram disabilitato
    "bot was kicked",  # Bot rimosso da gruppo/canale
    "chat not found",  # Chat non esiste più
]


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
            except OSError:
                pass  # Debug read fallito (include PermissionError), non critico
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
    # Confronta luce
    luce_result = _check_utility_rates(user_rates["luce"], current_rates, "luce")

    # Confronta gas (se presente)
    has_gas = user_rates.get("gas") is not None
    gas_result = (
        _check_utility_rates(user_rates["gas"], current_rates, "gas")
        if has_gas
        else {
            "energia_saving": None,
            "comm_saving": None,
            "energia_worse": False,
            "comm_worse": False,
            "has_savings": False,
        }
    )

    # Determina se è un caso "mixed" PER FORNITURA (una componente migliora, l'altra peggiora)
    luce_has_improvement = luce_result["energia_saving"] or luce_result["comm_saving"]
    luce_has_worsening = luce_result["energia_worse"] or luce_result["comm_worse"]
    luce_is_mixed = luce_has_improvement and luce_has_worsening

    gas_has_improvement = gas_result["energia_saving"] or gas_result["comm_saving"]
    gas_has_worsening = gas_result["energia_worse"] or gas_result["comm_worse"]
    gas_is_mixed = gas_has_improvement and gas_has_worsening

    # Mantieni is_mixed globale per backward compatibility
    is_mixed = luce_is_mixed or gas_is_mixed

    # Costruisci risultato finale
    return {
        "luce_energia": luce_result["energia_saving"],
        "luce_comm": luce_result["comm_saving"],
        "gas_energia": gas_result["energia_saving"],
        "gas_comm": gas_result["comm_saving"],
        "luce_energia_worse": luce_result["energia_worse"],
        "luce_comm_worse": luce_result["comm_worse"],
        "gas_energia_worse": gas_result["energia_worse"],
        "gas_comm_worse": gas_result["comm_worse"],
        "has_savings": luce_result["has_savings"] or gas_result["has_savings"],
        "is_mixed": is_mixed,
        "luce_is_mixed": luce_is_mixed,
        "gas_is_mixed": gas_is_mixed,
        "luce_tipo": user_rates["luce"]["tipo"],
        "luce_fascia": user_rates["luce"]["fascia"],
        "gas_tipo": user_rates["gas"]["tipo"] if has_gas else None,
        "gas_fascia": user_rates["gas"]["fascia"] if has_gas else None,
    }


# ========== HELPER FUNCTIONS ==========


def _compare_rate_field(
    user_value: float, current_value: float | None
) -> tuple[dict[str, float] | None, bool]:
    """Confronta un singolo campo tariffa (energia o commercializzazione)

    Args:
        user_value: Valore attuale dell'utente
        current_value: Valore della nuova tariffa (None se non disponibile)

    Returns:
        Tuple (risparmio_dict, is_worse)
        - risparmio_dict: Dict con attuale/nuova/risparmio se c'è miglioramento, altrimenti None
        - is_worse: True se la nuova tariffa è peggiore, False altrimenti
    """
    if current_value is None:
        return None, False

    if current_value < user_value:
        # Miglioramento
        return {
            "attuale": user_value,
            "nuova": current_value,
            "risparmio": user_value - current_value,
        }, False
    elif current_value > user_value:
        # Peggioramento
        return None, True

    # Nessun cambiamento
    return None, False


def _check_utility_rates(
    user_utility: dict[str, Any],
    current_rates: dict[str, Any],
    utility_name: str,
) -> dict[str, Any]:
    """Confronta tariffe luce o gas e ritorna risparmi/peggioramenti

    Args:
        user_utility: Tariffe utente per luce/gas con tipo, fascia, energia, commercializzazione
        current_rates: Tariffe correnti complete
        utility_name: "luce" o "gas"

    Returns:
        Dict con campi: energia_saving, comm_saving, energia_worse, comm_worse, has_savings
    """
    result = {
        "energia_saving": None,
        "comm_saving": None,
        "energia_worse": False,
        "comm_worse": False,
        "has_savings": False,
    }

    tipo = user_utility["tipo"]
    fascia = user_utility["fascia"]

    # Accedi alla tariffa corrente specifica
    utility_rate = current_rates.get(utility_name, {}).get(tipo, {}).get(fascia)
    if not utility_rate:
        return result

    # Confronta energia
    energia_saving, energia_worse = _compare_rate_field(
        user_utility["energia"], utility_rate.get("energia")
    )
    result["energia_saving"] = energia_saving
    result["energia_worse"] = energia_worse
    if energia_saving:
        result["has_savings"] = True

    # Confronta commercializzazione
    comm_saving, comm_worse = _compare_rate_field(
        user_utility["commercializzazione"], utility_rate.get("commercializzazione")
    )
    result["comm_saving"] = comm_saving
    result["comm_worse"] = comm_worse
    if comm_saving:
        result["has_savings"] = True

    return result


def _build_current_octopus_rates(
    user_rates: dict[str, Any], current_rates: dict[str, Any]
) -> dict[str, dict[str, float]]:
    """Costruisce oggetto con tariffe Octopus attuali per l'utente

    Args:
        user_rates: Tariffe utente con tipo e fascia
        current_rates: Tariffe correnti complete

    Returns:
        Dict con luce (e gas se presente) con energia e commercializzazione
    """
    result = {}

    # Luce
    luce_tipo = user_rates["luce"]["tipo"]
    luce_fascia = user_rates["luce"]["fascia"]
    luce_rate = current_rates.get("luce", {}).get(luce_tipo, {}).get(luce_fascia)

    if luce_rate:
        result["luce"] = {
            "energia": luce_rate["energia"],
            "commercializzazione": luce_rate["commercializzazione"],
        }

    # Gas (se l'utente ce l'ha)
    if user_rates.get("gas"):
        gas_tipo = user_rates["gas"]["tipo"]
        gas_fascia = user_rates["gas"]["fascia"]
        gas_rate = current_rates.get("gas", {}).get(gas_tipo, {}).get(gas_fascia)

        if gas_rate:
            result["gas"] = {
                "energia": gas_rate["energia"],
                "commercializzazione": gas_rate["commercializzazione"],
            }

    return result


def _should_notify_user(
    user_rates: dict[str, Any], current_octopus: dict[str, dict[str, float]]
) -> bool:
    """Controlla se l'utente dovrebbe essere notificato

    Args:
        user_rates: Tariffe utente incluso last_notified_rates
        current_octopus: Tariffe Octopus correnti per questo utente

    Returns:
        True se dobbiamo notificare, False se già notificato in precedenza
    """
    last_notified = user_rates.get("last_notified_rates", {})
    return last_notified != current_octopus


def _format_header(is_mixed: bool) -> str:
    """Formatta intestazione notifica"""
    if is_mixed:
        header = "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n"
        header += "OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n"
    else:
        header = "⚡️ <b>Buone notizie!</b>\n"
        header += "OctoTracker ha trovato una tariffa Octopus Energy più conveniente rispetto a quella che hai attiva.\n\n"
    return header


def _format_utility_section(
    utility_name: str,
    savings: dict[str, Any],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
    emoji: str,
    unit: str,
    estimated_savings: float | None = None,
) -> str:
    """Formatta sezione utility (luce o gas) della notifica

    Args:
        utility_name: "luce" o "gas"
        savings: Dizionario con risparmi/peggioramenti
        user_rates: Dati utente
        current_rates: Tariffe correnti
        emoji: Emoji da usare (💡 per luce, 🔥 per gas)
        unit: Unità di misura energia (€/kWh per luce, €/Smc per gas)
        estimated_savings: Risparmio stimato in €/anno (solo per utility mixed con consumi)

    Returns:
        Stringa HTML formattata per la sezione utility
    """
    # Check se utility è presente e ha risparmi
    energia_key = f"{utility_name}_energia"
    comm_key = f"{utility_name}_comm"

    if utility_name == "gas" and user_rates.get("gas") is None:
        return ""

    if not (savings[energia_key] or savings[comm_key]):
        return ""

    # Estrai tipo e fascia
    tipo_key = f"{utility_name}_tipo"
    fascia_key = f"{utility_name}_fascia"
    tipo = savings[tipo_key]
    fascia = savings[fascia_key]

    # Formatta tipo e label usando helper functions
    tipo_display = format_utility_type_display(tipo, fascia)
    label = get_utility_label(tipo, utility_name)

    # Header sezione
    utility_display = utility_name.capitalize()
    section = f"{emoji} <b>{utility_display} ({tipo_display}):</b>\n"

    # Formatta tariffe utente
    user_energia = format_number(
        user_rates[utility_name]["energia"], max_decimals=MAX_DECIMALS_ENERGY
    )
    user_comm = format_number(
        user_rates[utility_name]["commercializzazione"], max_decimals=MAX_DECIMALS_COST
    )
    section += f"Tua tariffa: {label} {user_energia} {unit}, Comm. {user_comm} €/anno\n"

    # Formatta nuove tariffe se disponibili
    if current_rates.get(utility_name, {}).get(tipo, {}).get(fascia):
        energia_new = current_rates[utility_name][tipo][fascia]["energia"]
        comm_new = current_rates[utility_name][tipo][fascia]["commercializzazione"]

        energia_formatted = format_number(energia_new, max_decimals=MAX_DECIMALS_ENERGY)
        comm_formatted = format_number(comm_new, max_decimals=MAX_DECIMALS_COST)

        # Formatta energia con grassetto/sottolineato in base a risparmio/peggioramento
        energia_worse_key = f"{utility_name}_energia_worse"
        if savings[energia_key]:
            energia_str = f"<b>{energia_formatted} {unit}</b>"
        elif savings[energia_worse_key]:
            energia_str = f"<u>{energia_formatted} {unit}</u>"
        else:
            energia_str = f"{energia_formatted} {unit}"

        # Formatta commercializzazione
        comm_worse_key = f"{utility_name}_comm_worse"
        if savings[comm_key]:
            comm_str = f"<b>{comm_formatted} €/anno</b>"
        elif savings[comm_worse_key]:
            comm_str = f"<u>{comm_formatted} €/anno</u>"
        else:
            comm_str = f"{comm_formatted} €/anno"

        section += f"Nuova tariffa: {label} {energia_str}, Comm. {comm_str}\n"

        # Aggiungi codice offerta se disponibile
        cod_offerta = current_rates[utility_name][tipo][fascia].get("cod_offerta")
        if cod_offerta:
            section += f"📋 Codice offerta: <code>{cod_offerta}</code>\n"

    # Aggiungi stima risparmio se disponibile (per utility mixed con consumi)
    if estimated_savings is not None:
        risparmio_formatted = format_number(abs(estimated_savings), max_decimals=MAX_DECIMALS_COST)
        section += f"💰 In base ai tuoi consumi di {utility_name}, stimiamo un risparmio di circa {risparmio_formatted} €/anno.\n"

    section += "\n"
    return section


def _format_luce_section(
    savings: dict[str, Any],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
    estimated_savings: float | None = None,
) -> str:
    """Formatta sezione luce della notifica"""
    return _format_utility_section(
        utility_name="luce",
        savings=savings,
        user_rates=user_rates,
        current_rates=current_rates,
        emoji="💡",
        unit="€/kWh",
        estimated_savings=estimated_savings,
    )


def _format_gas_section(
    savings: dict[str, Any],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
    estimated_savings: float | None = None,
) -> str:
    """Formatta sezione gas della notifica"""
    return _format_utility_section(
        utility_name="gas",
        savings=savings,
        user_rates=user_rates,
        current_rates=current_rates,
        emoji="🔥",
        unit="€/Smc",
        estimated_savings=estimated_savings,
    )


def _calculate_utility_savings(
    utility_type: str, user_rates: dict[str, Any], current_rates: dict[str, Any]
) -> float | None:
    """
    Calcola il risparmio stimato annuo in € per una singola utility (luce o gas).

    Args:
        utility_type: "luce" o "gas"
        user_rates: Dati utente (tariffe e consumi)
        current_rates: Tariffe correnti Octopus

    Returns:
        Risparmio stimato in €/anno (positivo = risparmio, negativo = aumento)
        None se l'utente non ha inserito consumi per questa utility
    """
    if utility_type == "luce":
        # Verifica consumi luce
        luce_consumo_f1 = user_rates["luce"].get("consumo_f1")
        if luce_consumo_f1 is None:
            return None

        # Calcola consumo totale luce
        luce_consumo_f2 = user_rates["luce"].get("consumo_f2", 0)
        luce_consumo_f3 = user_rates["luce"].get("consumo_f3", 0)
        consumo_totale = luce_consumo_f1 + luce_consumo_f2 + luce_consumo_f3

        # Ottieni tipo e fascia
        tipo = user_rates["luce"]["tipo"]
        fascia = user_rates["luce"]["fascia"]

        # Tariffe attuali utente
        user_energia = user_rates["luce"]["energia"]
        user_comm = user_rates["luce"]["commercializzazione"]

        # Nuove tariffe Octopus
        if not current_rates.get("luce", {}).get(tipo, {}).get(fascia):
            return None

        new_energia = current_rates["luce"][tipo][fascia]["energia"]
        new_comm = current_rates["luce"][tipo][fascia]["commercializzazione"]

        # Calcola risparmio
        risparmio_energia = (user_energia - new_energia) * consumo_totale
        risparmio_comm = user_comm - new_comm

        return risparmio_energia + risparmio_comm

    elif utility_type == "gas":
        # Verifica che l'utente abbia il gas
        if not user_rates.get("gas"):
            return None

        # Verifica consumi gas
        gas_consumo = user_rates["gas"].get("consumo_annuo")
        if gas_consumo is None:
            return None

        # Ottieni tipo e fascia
        tipo = user_rates["gas"]["tipo"]
        fascia = user_rates["gas"]["fascia"]

        # Tariffe attuali utente
        user_energia = user_rates["gas"]["energia"]
        user_comm = user_rates["gas"]["commercializzazione"]

        # Nuove tariffe Octopus
        if not current_rates.get("gas", {}).get(tipo, {}).get(fascia):
            return None

        new_energia = current_rates["gas"][tipo][fascia]["energia"]
        new_comm = current_rates["gas"][tipo][fascia]["commercializzazione"]

        # Calcola risparmio
        risparmio_energia = (user_energia - new_energia) * gas_consumo
        risparmio_comm = user_comm - new_comm

        return risparmio_energia + risparmio_comm

    return None


def _should_show_utility(
    utility_type: str,
    savings: dict[str, Any],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
) -> tuple[bool, float | None]:
    """
    Determina se mostrare una utility (luce o gas) nel messaggio di notifica.

    Logica:
    - Non mixed (ha savings) → MOSTRA sempre (con stima risparmio se disponibili consumi)
    - Mixed senza consumi → MOSTRA (con suggerimento)
    - Mixed con consumi:
      - Risparmio > 0 → MOSTRA (con stima)
      - Risparmio ≤ 0 → NON MOSTRA

    Args:
        utility_type: "luce" o "gas"
        savings: Dizionario con risparmi/peggioramenti
        user_rates: Dati utente
        current_rates: Tariffe correnti Octopus

    Returns:
        (should_show, estimated_savings)
        - should_show: True se va inclusa nel messaggio
        - estimated_savings: risparmio stimato (se disponibili consumi), None altrimenti
    """
    if utility_type == "luce":
        is_mixed = savings["luce_is_mixed"]
        has_savings = savings["luce_energia"] or savings["luce_comm"]
    elif utility_type == "gas":
        # Se utente non ha gas, non mostrare
        if not user_rates.get("gas"):
            return False, None

        is_mixed = savings["gas_is_mixed"]
        has_savings = savings["gas_energia"] or savings["gas_comm"]
    else:
        return False, None

    # Non mixed con savings → mostra sempre, calcola risparmio se ci sono consumi
    if not is_mixed and has_savings:
        estimated_savings = _calculate_utility_savings(utility_type, user_rates, current_rates)
        return True, estimated_savings

    # Mixed → calcola risparmio se ci sono consumi
    if is_mixed:
        estimated_savings = _calculate_utility_savings(utility_type, user_rates, current_rates)

        if estimated_savings is None:
            # Nessun consumo → mostra con suggerimento
            return True, None

        # Ha consumi → mostra solo se risparmio > 0
        return estimated_savings > 0, estimated_savings

    # Nessun savings → non mostrare
    return False, None


def _get_mixed_utilities(
    show_luce: bool,
    show_gas: bool,
    luce_is_mixed: bool,
    gas_is_mixed: bool,
    luce_estimated_savings: float | None,
    gas_estimated_savings: float | None,
) -> list[tuple[str, float | None]]:
    """Restituisce lista delle utility mixed da mostrare"""
    mixed_utilities = []
    if show_luce and luce_is_mixed:
        mixed_utilities.append(("luce", luce_estimated_savings))
    if show_gas and gas_is_mixed:
        mixed_utilities.append(("gas", gas_estimated_savings))
    return mixed_utilities


def _format_savings_estimates() -> str:
    """Formatta le stime di risparmio per utility con consumi

    NOTA: Le stime di risparmio sono ora mostrate inline nelle sezioni utility,
    quindi questa funzione restituisce una stringa vuota.
    """
    return ""


def _format_mixed_consumption_message(mixed_utilities: list[tuple[str, float | None]]) -> str:
    """Formatta messaggio consumi per utility mixed

    NOTA: Le stime di risparmio sono ora mostrate inline nelle sezioni utility,
    quindi questa funzione mostra solo i suggerimenti per le utility senza consumi.
    """
    if not mixed_utilities:
        return ""

    has_missing_consumption = any(savings is None for _, savings in mixed_utilities)
    has_consumption = any(savings is not None for _, savings in mixed_utilities)

    if has_missing_consumption and not has_consumption:
        # Tutte le utility mixed non hanno consumi
        return (
            "📊 In questi casi la convenienza dipende dai tuoi consumi.\n"
            "Se vuoi una stima più precisa, puoi indicare i tuoi consumi usando il comando /update.\n\n"
        )

    if has_consumption and has_missing_consumption:
        # Alcune utility hanno consumi, altre no → suggerisci di aggiungere tutti i consumi
        return "📊 Per una stima ancora più precisa, puoi indicare tutti i tuoi consumi con /update.\n\n"

    # Tutte le utility hanno consumi → le stime sono già mostrate inline, nessun messaggio aggiuntivo
    return ""


def _format_footer(
    luce_is_mixed: bool,
    gas_is_mixed: bool,
    luce_estimated_savings: float | None,
    gas_estimated_savings: float | None,
    show_luce: bool,
    show_gas: bool,
) -> str:
    """Formatta footer notifica con gestione per-utility"""
    mixed_utilities = _get_mixed_utilities(
        show_luce,
        show_gas,
        luce_is_mixed,
        gas_is_mixed,
        luce_estimated_savings,
        gas_estimated_savings,
    )

    footer = _format_mixed_consumption_message(mixed_utilities)
    footer += "🔧 Se vuoi aggiornare le tariffe che hai registrato, puoi farlo in qualsiasi momento con il comando /update.\n\n"
    footer += "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n"
    footer += "☕️ Se pensi che questo bot ti sia utile, puoi offrirmi un caffè su ko-fi.com/dstmrk — grazie di cuore! 💙"

    return footer


def format_notification(
    savings: dict[str, Any],
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
    show_luce: bool = True,
    show_gas: bool = True,
    luce_estimated_savings: float | None = None,
    gas_estimated_savings: float | None = None,
) -> str:
    """
    Formatta messaggio di notifica.

    Args:
        savings: Dizionario con risparmi/peggioramenti
        user_rates: Dati utente
        current_rates: Tariffe correnti
        show_luce: Se True, include sezione luce
        show_gas: Se True, include sezione gas
        luce_estimated_savings: Risparmio stimato luce (per mixed)
        gas_estimated_savings: Risparmio stimato gas (per mixed)
    """
    # Determina se mostrare header mixed
    is_mixed = savings["is_mixed"]

    message = _format_header(is_mixed)

    # Aggiungi sezioni solo per le utility da mostrare
    if show_luce:
        message += _format_luce_section(savings, user_rates, current_rates, luce_estimated_savings)
    if show_gas:
        message += _format_gas_section(savings, user_rates, current_rates, gas_estimated_savings)

    message += _format_footer(
        luce_is_mixed=savings["luce_is_mixed"],
        gas_is_mixed=savings["gas_is_mixed"],
        luce_estimated_savings=luce_estimated_savings,
        gas_estimated_savings=gas_estimated_savings,
        show_luce=show_luce,
        show_gas=show_gas,
    )
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
        error_msg = str(e).lower()  # Case-insensitive per robustezza

        # Controlla se l'errore indica che dobbiamo rimuovere l'utente
        if any(pattern in error_msg for pattern in TELEGRAM_ERRORS_TO_DELETE):
            logger.warning(
                f"🚫 Utente {user_id} non raggiungibile ('{e}') - rimozione dal database"
            )
            from database import remove_user

            remove_user(user_id)
            return False

        logger.error(f"❌ Errore Telegram invio messaggio a {user_id}: {e}")
        return False


def _validate_checker_data(
    users: dict[str, Any], current_rates: dict[str, Any], start_time: float
) -> bool:
    """Valida che ci siano utenti e tariffe disponibili"""
    if not users:
        logger.warning(
            f"⚠️  Nessun utente registrato (completato in {time.time() - start_time:.2f}s)"
        )
        return False

    if not current_rates:
        logger.error(
            f"❌ Nessuna tariffa disponibile dopo {time.time() - start_time:.2f}s. "
            "Esegui prima scraper.py"
        )
        return False

    return True


def _prepare_user_notification(
    user_id: str,
    user_rates: dict[str, Any],
    current_rates: dict[str, Any],
) -> tuple[dict[str, Any], str] | None:
    """
    Valuta un utente e prepara i dati per la notifica.

    Returns:
        Tupla (current_octopus, message) se notifica necessaria, None altrimenti
    """
    logger.info(f"📊 Controllo utente {user_id}...")

    savings = check_better_rates(user_rates, current_rates)

    if not savings["has_savings"]:
        logger.info("  ℹ️  Nessun risparmio trovato")
        return None

    # Costruisci tariffe Octopus correnti per questo utente
    current_octopus = _build_current_octopus_rates(user_rates, current_rates)

    # Controlla se già notificato
    if not _should_notify_user(user_rates, current_octopus):
        logger.info("  ⏭️  Tariffe migliori già notificate in precedenza, skip")
        return None

    # Valuta separatamente luce e gas per determinare cosa mostrare
    show_luce, luce_savings = _should_show_utility("luce", savings, user_rates, current_rates)
    show_gas, gas_savings = _should_show_utility("gas", savings, user_rates, current_rates)

    # Skip se nessuna utility è conveniente
    if not show_luce and not show_gas:
        logger.info("  ⏭️  Nessuna fornitura conveniente da mostrare, skip")
        if savings["luce_is_mixed"] and luce_savings is not None:
            logger.info(f"     Luce MIXED: risparmio stimato {luce_savings:.2f} €/anno (≤ 0)")
        if savings["gas_is_mixed"] and gas_savings is not None:
            logger.info(f"     Gas MIXED: risparmio stimato {gas_savings:.2f} €/anno (≤ 0)")
        return None

    # Log quali utility vengono mostrate
    utilities_shown = []
    if show_luce:
        utilities_shown.append("luce")
    if show_gas:
        utilities_shown.append("gas")
    logger.info(f"  📋 Forniture da mostrare: {', '.join(utilities_shown)}")

    # Genera messaggio
    message = format_notification(
        savings,
        user_rates,
        current_rates,
        show_luce=show_luce,
        show_gas=show_gas,
        luce_estimated_savings=luce_savings,
        gas_estimated_savings=gas_savings,
    )
    logger.info("  📤 Notifica accodata per invio")

    return (current_octopus, message)


async def _send_notifications_parallel(
    bot: Bot, notifications_to_send: list[tuple[str, dict, dict, str]]
) -> int:
    """
    Invia notifiche in parallelo con rate limiting.

    Returns:
        Numero di notifiche inviate con successo
    """
    if not notifications_to_send:
        return 0

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
    return sum(1 for r in results if r is True)


async def check_and_notify_users(bot_token: str) -> None:
    """Controlla tariffe e invia notifiche in parallelo (chiamata da bot.py)"""
    start_time = time.time()
    logger.info("🔍 Inizio controllo tariffe...")

    # Carica e valida dati
    users = load_users()
    current_rates = load_json(RATES_FILE)

    if not _validate_checker_data(users, current_rates, start_time):
        return

    # Inizializza bot
    bot = Bot(token=bot_token)

    # ========== FASE 1: Prepara tutte le notifiche ==========
    notifications_to_send = []

    for user_id, user_rates in users.items():
        result = _prepare_user_notification(user_id, user_rates, current_rates)
        if result is not None:
            current_octopus, message = result
            notifications_to_send.append((user_id, user_rates, current_octopus, message))

    # ========== FASE 2: Invia notifiche in parallelo ==========
    notifications_sent = await _send_notifications_parallel(bot, notifications_to_send)

    # Calcola metriche
    duration = time.time() - start_time
    logger.info(
        f"✅ Checker completato in {duration:.2f}s - Notifiche: {notifications_sent}/{len(users)}"
    )


async def main() -> None:
    """Main per esecuzione standalone"""
    token = os.getenv("TELEGRAM_BOT_TOKEN")
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")
    await check_and_notify_users(token)


if __name__ == "__main__":
    # Configura logging per esecuzione standalone (usa env var LOG_LEVEL)
    LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
    logging.basicConfig(
        level=getattr(logging, LOG_LEVEL, logging.INFO),
        format="%(asctime)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    asyncio.run(main())
