#!/usr/bin/env python3
"""
Gestione conversazione registrazione tariffe per OctoTracker
Conversation handler per raccolta dati tariffe luce/gas utenti
"""
import logging
from enum import IntEnum
from typing import Any

from telegram import InlineKeyboardButton, InlineKeyboardMarkup, Update
from telegram.constants import ParseMode
from telegram.ext import ContextTypes, ConversationHandler

from checker import format_number
from constants import ERROR_INPUT_TOO_LONG, ERROR_VALUE_NEGATIVE, MAX_NUMERIC_INPUT_LENGTH
from database import save_user, user_exists
from formatters import format_utility_header

# Setup logger
logger = logging.getLogger(__name__)

# Costanti messaggi
MSG_HAS_GAS = "Hai anche una fornitura gas attiva con Octopus Energy?"
MSG_GAS_CONSUMO = "Inserisci il tuo consumo annuo di gas in Smc.\n\n💬 Esempio: 1200"


# ========== INPUT VALIDATION ==========


def validate_numeric_input(text: str) -> tuple[float | None, str | None]:
    """
    Valida un input numerico da parte dell'utente.

    Args:
        text: Testo inserito dall'utente

    Returns:
        Tupla (valore, messaggio_errore):
        - valore: float convertito se valido, None se invalido
        - messaggio_errore: messaggio di errore se presente, None se valido

    Validazioni eseguite:
    1. Lunghezza massima (protezione da attacchi con input molto lunghi)
    2. Conversione a float
    3. Valore non negativo
    """
    # 1. Verifica lunghezza (protezione attacchi)
    if len(text) > MAX_NUMERIC_INPUT_LENGTH:
        return None, ERROR_INPUT_TOO_LONG

    # 2. Prova a convertire in float
    try:
        value = float(text.replace(",", "."))
    except ValueError:
        return None, None  # Errore di conversione, gestito dal chiamante

    # 3. Verifica che non sia negativo
    if value < 0:
        return None, ERROR_VALUE_NEGATIVE

    return value, None


# ========== CONVERSATION STATES ==========


class ConversationState(IntEnum):
    """Stati del conversation handler per registrazione tariffe"""

    TIPO_TARIFFA = 0
    LUCE_TIPO_VARIABILE = 1
    LUCE_ENERGIA = 2
    LUCE_COMM = 3
    VUOI_CONSUMI_LUCE = 7
    LUCE_CONSUMO_F1 = 8
    LUCE_CONSUMO_F2 = 9
    LUCE_CONSUMO_F3 = 10
    HA_GAS = 4
    GAS_ENERGIA = 5
    GAS_COMM = 6
    VUOI_CONSUMI_GAS = 11
    GAS_CONSUMO = 12


# Backward compatibility: mantieni le costanti per i test e il codice esistente
TIPO_TARIFFA = ConversationState.TIPO_TARIFFA
LUCE_TIPO_VARIABILE = ConversationState.LUCE_TIPO_VARIABILE
LUCE_ENERGIA = ConversationState.LUCE_ENERGIA
LUCE_COMM = ConversationState.LUCE_COMM
VUOI_CONSUMI_LUCE = ConversationState.VUOI_CONSUMI_LUCE
LUCE_CONSUMO_F1 = ConversationState.LUCE_CONSUMO_F1
LUCE_CONSUMO_F2 = ConversationState.LUCE_CONSUMO_F2
LUCE_CONSUMO_F3 = ConversationState.LUCE_CONSUMO_F3
HA_GAS = ConversationState.HA_GAS
GAS_ENERGIA = ConversationState.GAS_ENERGIA
GAS_COMM = ConversationState.GAS_COMM
VUOI_CONSUMI_GAS = ConversationState.VUOI_CONSUMI_GAS
GAS_CONSUMO = ConversationState.GAS_CONSUMO


# ========== CONVERSATION HANDLERS ==========


async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Avvia registrazione tariffe"""
    user_id = str(update.effective_user.id)
    is_update = user_exists(user_id)

    # Reset context per nuova conversazione
    context.user_data.clear()

    keyboard = [
        [
            InlineKeyboardButton("📊 Fissa", callback_data="tipo_fissa"),
            InlineKeyboardButton("📈 Variabile", callback_data="tipo_variabile"),
        ]
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)

    if is_update:
        messaggio = (
            "♻️ <b>Aggiorniamo le tue tariffe!</b>\n\n"
            "Inserisci di nuovo i valori attuali così OctoTracker potrà confrontarli "
            "con le nuove offerte di Octopus Energy.\n\n"
            "Ti guiderò passo passo come la prima volta: prima la luce, poi (se ce l'hai) il gas.\n\n"
            "👉 Iniziamo: che tipo di tariffa hai?"
        )
    else:
        messaggio = (
            "🐙 <b>Benvenuto su OctoTracker!</b>\n\n"
            "Questo bot controlla ogni giorno le tariffe di Octopus Energy e ti avvisa "
            "se ne trova di più convenienti rispetto alle tue attuali.\n\n"
            "Ti farò qualche semplice domanda per registrare le tue tariffe luce e (se ce l'hai) gas.\n"
            "Rispondi passo passo ai messaggi: ci vorrà meno di un minuto. ⚡️\n\n"
            "👉 Iniziamo: che tipo di tariffa hai?"
        )

    await update.message.reply_text(messaggio, reply_markup=reply_markup, parse_mode=ParseMode.HTML)
    return TIPO_TARIFFA


async def tipo_tariffa(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci scelta tipo tariffa (Fissa/Variabile)"""
    query = update.callback_query
    await query.answer()

    if query.data == "tipo_fissa":
        context.user_data["is_variabile"] = False
        context.user_data["luce_tipo"] = "fissa"
        context.user_data["luce_fascia"] = "monoraria"
        context.user_data["gas_tipo"] = "fissa"  # Se ha gas, sarà fissa
        context.user_data["gas_fascia"] = "monoraria"

        await query.edit_message_text(
            "📊 <b>Tariffa Fissa</b>\n\n"
            "Perfetto! Ora inserisci i dati della tua tariffa luce.\n\n"
            "👉 Quanto paghi per la materia energia luce (€/kWh)?\n\n"
            "ℹ️ Inserisci il prezzo <b>IVA e imposte escluse, perdite incluse</b> "
            "(come riportato sul sito Octopus Energy/ARERA)\n\n"
            "💬 Esempio: 0,145",
            parse_mode=ParseMode.HTML,
        )
        return LUCE_ENERGIA

    else:  # tipo_variabile
        context.user_data["is_variabile"] = True

        keyboard = [
            [
                InlineKeyboardButton("⏱️ Monoraria", callback_data="luce_mono"),
                InlineKeyboardButton("⏱️⏱️⏱️ Trioraria", callback_data="luce_tri"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await query.edit_message_text(
            "📈 <b>Tariffa Variabile</b>\n\n"
            "La tua tariffa luce è monoraria o trioraria (F1/F2/F3)?",
            reply_markup=reply_markup,
            parse_mode=ParseMode.HTML,
        )
        return LUCE_TIPO_VARIABILE


async def luce_tipo_variabile(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci scelta tipo luce variabile (Monoraria/Trioraria)"""
    query = update.callback_query
    await query.answer()

    if query.data == "luce_mono":
        context.user_data["luce_tipo"] = "variabile"
        context.user_data["luce_fascia"] = "monoraria"
        tipo_msg = "monoraria (PUN)"
    else:  # luce_tri
        context.user_data["luce_tipo"] = "variabile"
        context.user_data["luce_fascia"] = "trioraria"
        tipo_msg = "trioraria (PUN)"

    # Gas variabile è sempre monorario
    context.user_data["gas_tipo"] = "variabile"
    context.user_data["gas_fascia"] = "monoraria"

    await query.edit_message_text(
        f"⚡ <b>Luce variabile {tipo_msg}</b>\n\n"
        f"Ora inserisci lo spread della tua tariffa rispetto al PUN.\n\n"
        f"ℹ️ Inserisci il valore <b>IVA e imposte escluse, perdite incluse</b> "
        f"(come riportato sul sito Octopus Energy/ARERA)\n\n"
        f"💬 Esempio: se la tua tariffa è <b>PUN + 0,0088</b> €/kWh, scrivi <code>0,0088</code>",
        parse_mode=ParseMode.HTML,
    )
    return LUCE_ENERGIA


async def luce_energia(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo energia luce (spread o prezzo fisso)"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return LUCE_ENERGIA

    if value is None:
        # Errore di conversione (non è un numero)
        is_variabile = context.user_data.get("is_variabile", False)
        if is_variabile:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,0088)")
        else:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,145)")
        return LUCE_ENERGIA

    context.user_data["luce_energia"] = value
    await update.message.reply_text(
        "Perfetto! Ora indica il costo di commercializzazione luce, in euro/anno.\n\n"
        "💬 Esempio: 72 (se paghi 6 €/mese)"
    )
    return LUCE_COMM


async def luce_comm(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo commercializzazione luce e chiedi se vuole inserire consumi"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return LUCE_COMM

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 96.50)")
        return LUCE_COMM

    context.user_data["luce_comm"] = value

    keyboard = [
        [
            InlineKeyboardButton("✅ Sì", callback_data="consumi_luce_si"),
            InlineKeyboardButton("❌ No", callback_data="consumi_luce_no"),
        ]
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)

    await update.message.reply_text(
        "Vuoi indicare anche il tuo consumo annuale di energia elettrica (in kWh)?\n\n"
        "💡 Serve solo per valutare meglio quando una tariffa può convenirti.",
        reply_markup=reply_markup,
    )
    return VUOI_CONSUMI_LUCE


async def vuoi_consumi_luce(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se vuole inserire consumi luce"""
    query = update.callback_query
    await query.answer()

    if query.data == "consumi_luce_si":
        # Inizia raccolta consumi in base alla fascia
        luce_fascia = context.user_data.get("luce_fascia", "monoraria")

        if luce_fascia == "monoraria":
            messaggio = (
                "Inserisci il tuo consumo annuo totale di energia elettrica in kWh.\n\n"
                "💬 Esempio: 2700"
            )
        elif luce_fascia == "trioraria":
            messaggio = (
                "Inserisci il tuo consumo annuo in fascia F1 in kWh.\n\n"
                "(F1 = feriali 8–19)\n\n"
                "💬 Esempio: 900"
            )
        else:
            # Fallback (non dovrebbe mai succedere)
            messaggio = "Inserisci il tuo consumo annuo in kWh."

        await query.edit_message_text(messaggio, parse_mode=ParseMode.HTML)
        return LUCE_CONSUMO_F1
    else:
        # Non vuole inserire consumi, vai direttamente a chiedere se ha gas
        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)
        await query.edit_message_text(MSG_HAS_GAS, reply_markup=reply_markup)
        return HA_GAS


async def luce_consumo_f1(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F1 luce e procedi in base alla fascia"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return LUCE_CONSUMO_F1

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 2700)")
        return LUCE_CONSUMO_F1

    context.user_data["luce_consumo_f1"] = value

    # Se monoraria, abbiamo finito con i consumi luce → vai a HA_GAS
    luce_fascia = context.user_data.get("luce_fascia", "monoraria")
    if luce_fascia == "monoraria":
        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no"),
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)
        await update.message.reply_text(MSG_HAS_GAS, reply_markup=reply_markup)
        return HA_GAS

    # Se trioraria, chiedi F2
    await update.message.reply_text(
        "Ora inserisci il tuo consumo annuo in fascia F2 in kWh.\n\n"
        "(F2 = feriali 7–8 e 19–23, sabato 7–23)\n\n"
        "💬 Esempio: 900"
    )
    return LUCE_CONSUMO_F2


async def luce_consumo_f2(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F2 luce (solo trioraria)"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return LUCE_CONSUMO_F2

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 900)")
        return LUCE_CONSUMO_F2

    context.user_data["luce_consumo_f2"] = value

    # Chiedi F3
    await update.message.reply_text(
        "Infine, inserisci il tuo consumo annuo in fascia F3 in kWh.\n\n"
        "(F3 = notte, domeniche e festivi)\n\n"
        "💬 Esempio: 900"
    )
    return LUCE_CONSUMO_F3


async def luce_consumo_f3(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo F3 luce (solo trioraria) e vai a HA_GAS"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return LUCE_CONSUMO_F3

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 900)")
        return LUCE_CONSUMO_F3

    context.user_data["luce_consumo_f3"] = value

    # Consumi luce completati, vai a chiedere se ha gas
    keyboard = [
        [
            InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
            InlineKeyboardButton("❌ No", callback_data="gas_no"),
        ]
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)
    await update.message.reply_text(MSG_HAS_GAS, reply_markup=reply_markup)
    return HA_GAS


async def ha_gas(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se ha gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "gas_si":
        is_variabile = context.user_data.get("is_variabile", False)

        if is_variabile:
            msg = (
                "🔥 <b>Gas variabile</b>\n\n"
                "Ora inserisci lo spread della tua tariffa rispetto al PSV.\n\n"
                "ℹ️ Inserisci il valore <b>IVA e imposte escluse</b> "
                "(come riportato sul sito Octopus Energy/ARERA)\n\n"
                "💬 Esempio: se la tua tariffa è <b>PSV + 0,08</b> €/Smc, scrivi <code>0,08</code>"
            )
        else:
            msg = (
                "🔥 <b>Gas fisso</b>\n\n"
                "Perfetto! Inserisci il costo materia energia gas (€/Smc).\n\n"
                "ℹ️ Inserisci il prezzo <b>IVA e imposte escluse</b> "
                "(come riportato sul sito Octopus Energy/ARERA)\n\n"
                "💬 Esempio: 0,456"
            )

        await query.edit_message_text(msg, parse_mode=ParseMode.HTML)
        return GAS_ENERGIA
    else:
        return await salva_e_conferma(query, context, solo_luce=True)


async def gas_energia(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva costo energia gas (spread o prezzo fisso)"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return GAS_ENERGIA

    if value is None:
        # Errore di conversione (non è un numero)
        is_variabile = context.user_data.get("is_variabile", False)
        if is_variabile:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,08)")
        else:
            await update.message.reply_text("❌ Inserisci un numero valido (es: 0,456)")
        return GAS_ENERGIA

    context.user_data["gas_energia"] = value
    await update.message.reply_text(
        "Perfetto! Ora indica il costo di commercializzazione gas, in euro/anno.\n\n"
        "💬 Esempio: 84 (se paghi 7 €/mese)"
    )
    return GAS_COMM


async def gas_comm(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva commercializzazione gas e chiedi se vuole inserire consumi"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return GAS_COMM

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 144.00)")
        return GAS_COMM

    context.user_data["gas_comm"] = value

    keyboard = [
        [
            InlineKeyboardButton("✅ Sì", callback_data="consumi_gas_si"),
            InlineKeyboardButton("❌ No", callback_data="consumi_gas_no"),
        ]
    ]
    reply_markup = InlineKeyboardMarkup(keyboard)

    await update.message.reply_text(
        "Vuoi indicare anche il tuo consumo annuale di gas (in Smc)?\n\n"
        "🔥 Serve solo per valutare meglio quando una tariffa può convenirti.",
        reply_markup=reply_markup,
    )
    return VUOI_CONSUMI_GAS


async def vuoi_consumi_gas(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Gestisci risposta se vuole inserire consumi gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "consumi_gas_si":
        # Chiedi consumo gas
        await query.edit_message_text(MSG_GAS_CONSUMO)
        return GAS_CONSUMO
    else:
        # Non vuole inserire consumi, salva e conferma
        return await salva_e_conferma(query, context, solo_luce=False)


async def gas_consumo(update: Update, context: ContextTypes.DEFAULT_TYPE) -> int:
    """Salva consumo gas e vai a conferma"""
    value, error = validate_numeric_input(update.message.text)

    if error:
        await update.message.reply_text(error)
        return GAS_CONSUMO

    if value is None:
        # Errore di conversione (non è un numero)
        await update.message.reply_text("❌ Inserisci un numero valido (es: 1200)")
        return GAS_CONSUMO

    context.user_data["gas_consumo_annuo"] = value

    # Consumi completati, salva e conferma
    return await salva_e_conferma(update, context, solo_luce=False)


# ========== HELPER FUNCTIONS ==========


def _build_user_data(context: ContextTypes.DEFAULT_TYPE, solo_luce: bool) -> dict[str, Any]:
    """
    Costruisce la struttura dati utente da salvare nel database.

    Args:
        context: Context del conversation handler
        solo_luce: True se l'utente ha solo luce, False se ha anche gas

    Returns:
        Dizionario con struttura nested luce/gas

    Raises:
        KeyError: Se mancano dati necessari nel context.user_data
    """
    # Verifica che tutti i dati necessari per luce esistano
    required_luce_keys = ["luce_tipo", "luce_fascia", "luce_energia", "luce_comm"]
    missing_keys = [key for key in required_luce_keys if key not in context.user_data]

    if missing_keys:
        # Log dettagliato per debug
        available_keys = list(context.user_data.keys())
        logger.error(
            f"Dati mancanti in context.user_data. "
            f"Richiesti: {required_luce_keys}, "
            f"Mancanti: {missing_keys}, "
            f"Disponibili: {available_keys}"
        )
        raise KeyError(f"Dati mancanti per luce: {', '.join(missing_keys)}")

    user_data = {
        "luce": {
            "tipo": context.user_data["luce_tipo"],
            "fascia": context.user_data["luce_fascia"],
            "energia": context.user_data["luce_energia"],
            "commercializzazione": context.user_data["luce_comm"],
        }
    }

    # Aggiungi consumi luce se presenti
    if "luce_consumo_f1" in context.user_data:
        user_data["luce"]["consumo_f1"] = context.user_data["luce_consumo_f1"]
    if "luce_consumo_f2" in context.user_data:
        user_data["luce"]["consumo_f2"] = context.user_data["luce_consumo_f2"]
    if "luce_consumo_f3" in context.user_data:
        user_data["luce"]["consumo_f3"] = context.user_data["luce_consumo_f3"]

    if not solo_luce:
        # Verifica che tutti i dati necessari per gas esistano
        required_gas_keys = ["gas_tipo", "gas_fascia", "gas_energia", "gas_comm"]
        missing_gas_keys = [key for key in required_gas_keys if key not in context.user_data]

        if missing_gas_keys:
            # Log dettagliato per debug
            available_keys = list(context.user_data.keys())
            logger.error(
                f"Dati mancanti in context.user_data. "
                f"Richiesti: {required_gas_keys}, "
                f"Mancanti: {missing_gas_keys}, "
                f"Disponibili: {available_keys}"
            )
            raise KeyError(f"Dati mancanti per gas: {', '.join(missing_gas_keys)}")

        user_data["gas"] = {
            "tipo": context.user_data["gas_tipo"],
            "fascia": context.user_data["gas_fascia"],
            "energia": context.user_data["gas_energia"],
            "commercializzazione": context.user_data["gas_comm"],
        }
        # Aggiungi consumo gas se presente
        if "gas_consumo_annuo" in context.user_data:
            user_data["gas"]["consumo_annuo"] = context.user_data["gas_consumo_annuo"]
    else:
        user_data["gas"] = None

    return user_data


def _format_confirmation_message(user_data: dict[str, Any]) -> str:
    """
    Formatta il messaggio di conferma con i dati inseriti dall'utente.

    Args:
        user_data: Dizionario con struttura nested luce/gas

    Returns:
        Messaggio HTML formattato per Telegram
    """
    # Formatta numeri luce
    luce_energia_fmt = format_number(user_data["luce"]["energia"], max_decimals=4)
    luce_comm_fmt = format_number(user_data["luce"]["commercializzazione"], max_decimals=2)

    # Formatta header luce
    tipo_display, luce_label, luce_unit = format_utility_header("luce", user_data["luce"])

    messaggio = (
        "✅ <b>Abbiamo finito!</b>\n\n"
        "Ecco i dati che hai inserito:\n\n"
        f"💡 <b>Luce ({tipo_display})</b>\n"
        f"- {luce_label}: {luce_energia_fmt} {luce_unit}\n"
        f"- Commercializzazione: {luce_comm_fmt} €/anno\n"
    )

    # Aggiungi consumi luce se presenti
    consumo_f1 = user_data["luce"].get("consumo_f1")
    if consumo_f1 is not None:
        consumo_f2 = user_data["luce"].get("consumo_f2")
        consumo_f3 = user_data["luce"].get("consumo_f3")
        luce_fascia = user_data["luce"]["fascia"]

        if luce_fascia == "monoraria":
            messaggio += f"- Consumo: <b>{format_number(consumo_f1, max_decimals=0)}</b> kWh/anno\n"

        elif luce_fascia == "bioraria":
            totale = consumo_f1 + consumo_f2
            messaggio += (
                f"- Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F23: {format_number(consumo_f2, max_decimals=0)} kWh\n"
            )

        elif luce_fascia == "trioraria":
            totale = consumo_f1 + consumo_f2 + consumo_f3
            messaggio += (
                f"- Consumo: <b>{format_number(totale, max_decimals=0)}</b> kWh/anno - "
                f"F1: {format_number(consumo_f1, max_decimals=0)} kWh | "
                f"F2: {format_number(consumo_f2, max_decimals=0)} kWh | "
                f"F3: {format_number(consumo_f3, max_decimals=0)} kWh\n"
            )

    # Aggiungi sezione gas se presente
    if user_data["gas"] is not None:
        gas_energia_fmt = format_number(user_data["gas"]["energia"], max_decimals=4)
        gas_comm_fmt = format_number(user_data["gas"]["commercializzazione"], max_decimals=2)

        # Formatta header gas
        tipo_display_gas, gas_label, gas_unit = format_utility_header("gas", user_data["gas"])

        messaggio += (
            f"\n🔥 <b>Gas ({tipo_display_gas})</b>\n"
            f"- {gas_label}: {gas_energia_fmt} {gas_unit}\n"
            f"- Commercializzazione: {gas_comm_fmt} €/anno\n"
        )

        # Aggiungi consumo gas se presente
        consumo_gas = user_data["gas"].get("consumo_annuo")
        if consumo_gas is not None:
            messaggio += (
                f"- Consumo: <b>{format_number(consumo_gas, max_decimals=0)}</b> Smc/anno\n"
            )

    # Aggiungi footer
    messaggio += (
        "\nTutto corretto?\n"
        "Se in futuro vuoi modificare qualcosa, puoi usare il comando /update.\n\n"
        "⚠️ OctoTracker non è affiliato né collegato in alcun modo a Octopus Energy."
    )

    return messaggio


async def salva_e_conferma(
    update_or_query: Update | Any, context: ContextTypes.DEFAULT_TYPE, solo_luce: bool
) -> int:
    """Salva dati utente e mostra conferma"""
    # Distingui tra Update (con message) e CallbackQuery
    if hasattr(update_or_query, "effective_user"):
        # È un Update
        user_id = str(update_or_query.effective_user.id)
        send_message = lambda text, **kwargs: update_or_query.message.reply_text(text, **kwargs)
    else:
        # È un CallbackQuery
        user_id = str(update_or_query.from_user.id)
        send_message = lambda text, **kwargs: update_or_query.edit_message_text(text, **kwargs)

    try:
        # Costruisci struttura dati utente
        user_data = _build_user_data(context, solo_luce)
    except KeyError as e:
        # Dati mancanti - chiedi all'utente di riprovare
        logger.error(f"User {user_id}: Errore costruzione dati utente - {e}")
        await send_message(
            "❌ Si è verificato un errore durante il salvataggio dei dati.\n\n"
            "Per favore riprova usando il comando /start per ricominciare la registrazione.",
            parse_mode=ParseMode.HTML,
        )
        return ConversationHandler.END

    # Salva nel database
    save_user(user_id, user_data)

    # Formatta messaggio di conferma
    messaggio = _format_confirmation_message(user_data)

    await send_message(messaggio, parse_mode=ParseMode.HTML)
    return ConversationHandler.END
