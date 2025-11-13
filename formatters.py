"""
Funzioni di formattazione condivise per messaggi utente
"""

from typing import Any

from constants import LABEL_FIXED_PRICE, LABEL_VARIABLE_ELECTRICITY, LABEL_VARIABLE_GAS


def format_utility_type_display(tipo: str, fascia: str) -> str:
    """Formatta tipo e fascia per visualizzazione

    Args:
        tipo: Tipo tariffa ("fissa" o "variabile")
        fascia: Fascia ("monoraria", "bioraria", "trioraria")

    Returns:
        Stringa formattata (es. "Fissa Monoraria", "Variabile Trioraria")

    Examples:
        >>> format_utility_type_display("fissa", "monoraria")
        "Fissa Monoraria"
        >>> format_utility_type_display("variabile", "trioraria")
        "Variabile Trioraria"
    """
    return f"{tipo.capitalize()} {fascia.capitalize()}"


def get_utility_label(tipo: str, utility_name: str) -> str:
    """Ottieni label per tipo tariffa e utility

    Args:
        tipo: Tipo tariffa ("fissa" o "variabile")
        utility_name: Nome utility ("luce" o "gas")

    Returns:
        Label appropriata (es. "Prezzo fisso", "Spread (PUN +)")

    Examples:
        >>> get_utility_label("fissa", "luce")
        "Prezzo fisso"
        >>> get_utility_label("variabile", "luce")
        "Spread (PUN +)"
        >>> get_utility_label("variabile", "gas")
        "Spread (PSV +)"
    """
    if tipo == "fissa":
        return LABEL_FIXED_PRICE

    # Variabile
    if utility_name == "luce":
        return LABEL_VARIABLE_ELECTRICITY
    else:  # gas
        return LABEL_VARIABLE_GAS


def get_utility_unit(utility_name: str) -> str:
    """Ottieni unità di misura per utility

    Args:
        utility_name: Nome utility ("luce" o "gas")

    Returns:
        Unità di misura (€/kWh per luce, €/Smc per gas)

    Examples:
        >>> get_utility_unit("luce")
        "€/kWh"
        >>> get_utility_unit("gas")
        "€/Smc"
    """
    return "€/kWh" if utility_name == "luce" else "€/Smc"


def format_utility_header(
    utility_name: str, user_data: dict[str, Any], emoji: str
) -> tuple[str, str, str]:
    """Formatta header per sezione utility (luce o gas)

    Args:
        utility_name: "luce" o "gas"
        user_data: Dati utente con tipo/fascia
        emoji: Emoji da usare (💡 per luce, 🔥 per gas)

    Returns:
        Tupla (tipo_display, label, unit)
        - tipo_display: Tipo formattato (es. "Fissa Monoraria")
        - label: Label tariffa (es. "Prezzo fisso", "Spread (PUN +)")
        - unit: Unità misura (es. "€/kWh")

    Examples:
        >>> format_utility_header("luce", {"tipo": "fissa", "fascia": "monoraria"}, "💡")
        ("Fissa Monoraria", "Prezzo fisso", "€/kWh")
    """
    tipo = user_data["tipo"]
    fascia = user_data["fascia"]

    tipo_display = format_utility_type_display(tipo, fascia)
    label = get_utility_label(tipo, utility_name)
    unit = get_utility_unit(utility_name)

    return tipo_display, label, unit
