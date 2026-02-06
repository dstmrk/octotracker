"""
Funzioni di formattazione condivise per messaggi utente
"""

from typing import Any

from constants import LABEL_FIXED_PRICE, LABEL_VARIABLE_ELECTRICITY, LABEL_VARIABLE_GAS


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


def format_utility_header(utility_name: str, user_data: dict[str, Any]) -> tuple[str, str, str]:
    """Formatta header per sezione utility (luce o gas)

    Args:
        utility_name: "luce" o "gas"
        user_data: Dati utente con tipo/fascia

    Returns:
        Tupla (tipo_display, label, unit)
        - tipo_display: Tipo formattato (es. "Fissa Monoraria")
        - label: Label tariffa (es. "Prezzo fisso", "Spread (PUN +)")
        - unit: Unità misura (es. "€/kWh")

    Examples:
        >>> format_utility_header("luce", {"tipo": "fissa", "fascia": "monoraria"})
        ("Fissa Monoraria", "Prezzo fisso", "€/kWh")
    """
    tipo = user_data["tipo"]
    fascia = user_data["fascia"]

    tipo_display = format_utility_type_display(tipo, fascia)
    label = get_utility_label(tipo, utility_name)
    unit = get_utility_unit(utility_name)

    return tipo_display, label, unit


def format_luce_consumption(luce_data: dict[str, Any], prefix: str = "- ") -> str:
    """Formatta la riga dei consumi luce in base alla fascia.

    Args:
        luce_data: Dati luce dell'utente (con fascia, consumo_f1, consumo_f2, consumo_f3)
        prefix: Prefisso per la riga (es. "- " o "  - ")

    Returns:
        Stringa formattata con i consumi, o stringa vuota se non presenti
    """
    consumo_f1 = luce_data.get("consumo_f1")
    if consumo_f1 is None:
        return ""

    fascia = luce_data["fascia"]
    consumo_f2 = luce_data.get("consumo_f2")
    consumo_f3 = luce_data.get("consumo_f3")

    if fascia == "monoraria":
        return f"{prefix}Consumo: <b>{format_number(consumo_f1, max_decimals=0)}</b> kWh/anno\n"

    if fascia == "bioraria":
        totale = consumo_f1 + consumo_f2
        return (
            f"{prefix}Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
            f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
            f"F23: {format_number(consumo_f2, max_decimals=0)} kWh\n"
        )

    if fascia == "trioraria":
        totale = consumo_f1 + consumo_f2 + consumo_f3
        return (
            f"{prefix}Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
            f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
            f"F2: {format_number(consumo_f2, max_decimals=0)} kWh | "
            f"F3: {format_number(consumo_f3, max_decimals=0)} kWh\n"
        )

    return ""
