"""
Test per registration.py - Conversazione registrazione tariffe

Testa tutti i flussi conversazionali di registrazione:
- Validazione input numerici
- Formattazione messaggi di conferma
- Conversazione registrazione con input validi
- Gestione errori con input non validi
- Flussi completi: luce fissa, variabile mono/tri, con/senza gas
- Raccolta consumi luce e gas
"""

from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram import CallbackQuery, InlineKeyboardMarkup, Message, Update
from telegram.ext import ContextTypes

from database import load_user, save_user
from handlers.registration import (
    GAS_COMM,
    GAS_CONSUMO,
    GAS_ENERGIA,
    GAS_TIPO,
    HA_GAS,
    LUCE_COMM,
    LUCE_CONSUMO_F1,
    LUCE_CONSUMO_F2,
    LUCE_CONSUMO_F3,
    LUCE_ENERGIA,
    LUCE_TIPO_VARIABILE,
    TIPO_TARIFFA,
    VUOI_CONSUMI_GAS,
    VUOI_CONSUMI_LUCE,
    _format_confirmation_message,
    gas_comm,
    gas_consumo,
    gas_energia,
    gas_tipo_tariffa,
    ha_gas,
    luce_comm,
    luce_consumo_f1,
    luce_consumo_f2,
    luce_consumo_f3,
    luce_energia,
    luce_tipo_variabile,
    start,
    tipo_tariffa,
    validate_numeric_input,
    vuoi_consumi_gas,
    vuoi_consumi_luce,
)

# ========== TEST COMANDI BASE ==========


@pytest.mark.asyncio
async def test_start_new_user(mock_update, mock_context):
    """Test /start per nuovo utente"""
    result = await start(mock_update, mock_context)

    assert result == TIPO_TARIFFA
    mock_update.message.reply_text.assert_called_once()

    # Verifica messaggio contiene "Benvenuto"
    call_args = mock_update.message.reply_text.call_args
    assert "Benvenuto" in call_args[0][0]

    # Verifica keyboard con Fissa/Variabile
    assert "reply_markup" in call_args[1]
    assert isinstance(call_args[1]["reply_markup"], InlineKeyboardMarkup)


@pytest.mark.asyncio
async def test_update_command(mock_update, mock_context):
    """Test /update (alias di /start per aggiornare tariffe)"""
    # Prepara dati esistenti nel database
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    # /update deve chiamare start() che riavvia registrazione
    result = await start(mock_update, mock_context)

    assert result == TIPO_TARIFFA
    mock_update.message.reply_text.assert_called_once()

    # Deve chiedere di aggiornare
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]
    # Può contenere "Aggiorniamo" o simile se è update, o "Benvenuto" se nuovo
    assert "tipo di tariffa" in message_text.lower()


@pytest.mark.asyncio
async def test_start_with_telegram_channel(mock_update, mock_context, monkeypatch):
    """Test /start con canale Telegram configurato"""
    # Imposta variabile d'ambiente TELEGRAM_CHANNEL (con @)
    monkeypatch.setenv("TELEGRAM_CHANNEL", "@octotracker_updates")

    result = await start(mock_update, mock_context)

    assert result == TIPO_TARIFFA
    mock_update.message.reply_text.assert_called_once()

    # Verifica che il messaggio contenga il link al canale
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]
    assert "Per avere aggiornamenti sulle nuove funzionalità" in message_text
    assert "@octotracker_updates" in message_text
    assert "https://t.me/octotracker_updates" in message_text


@pytest.mark.asyncio
async def test_start_without_telegram_channel(mock_update, mock_context, monkeypatch):
    """Test /start senza canale Telegram configurato"""
    # Rimuovi o imposta a vuoto TELEGRAM_CHANNEL
    monkeypatch.delenv("TELEGRAM_CHANNEL", raising=False)

    result = await start(mock_update, mock_context)

    assert result == TIPO_TARIFFA
    mock_update.message.reply_text.assert_called_once()

    # Verifica che il messaggio NON contenga il link al canale
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]
    assert "Per avere aggiornamenti sulle nuove funzionalità" not in message_text
    assert (
        "@" not in message_text or "tipo di tariffa" in message_text
    )  # @ può apparire in altri contesti


@pytest.mark.asyncio
async def test_start_with_empty_telegram_channel(mock_update, mock_context, monkeypatch):
    """Test /start con TELEGRAM_CHANNEL vuoto o con soli spazi"""
    # Imposta TELEGRAM_CHANNEL a stringa vuota
    monkeypatch.setenv("TELEGRAM_CHANNEL", "   ")

    result = await start(mock_update, mock_context)

    assert result == TIPO_TARIFFA
    mock_update.message.reply_text.assert_called_once()

    # Verifica che il messaggio NON contenga il link al canale
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]
    assert "Per avere aggiornamenti sulle nuove funzionalità" not in message_text


# ========== TEST FLUSSO CONVERSAZIONE ==========


@pytest.mark.asyncio
async def test_tipo_tariffa_fissa(mock_callback_query, mock_context):
    """Test scelta tariffa fissa"""
    mock_callback_query.callback_query.data = "tipo_fissa"

    result = await tipo_tariffa(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data["is_variabile"] is False
    assert mock_context.user_data["luce_tipo"] == "fissa"
    assert mock_context.user_data["luce_fascia"] == "monoraria"

    mock_callback_query.callback_query.answer.assert_called_once()
    mock_callback_query.callback_query.edit_message_text.assert_called_once()


@pytest.mark.asyncio
async def test_tipo_tariffa_variabile(mock_callback_query, mock_context):
    """Test scelta tariffa variabile"""
    mock_callback_query.callback_query.data = "tipo_variabile"

    result = await tipo_tariffa(mock_callback_query, mock_context)

    assert result == LUCE_TIPO_VARIABILE
    assert mock_context.user_data["is_variabile"] is True

    mock_callback_query.callback_query.answer.assert_called_once()


@pytest.mark.asyncio
async def test_luce_tipo_variabile_mono(mock_callback_query, mock_context):
    """Test scelta luce variabile monoraria"""
    mock_callback_query.callback_query.data = "luce_mono"

    result = await luce_tipo_variabile(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data["luce_tipo"] == "variabile"
    assert mock_context.user_data["luce_fascia"] == "monoraria"
    # gas_tipo non viene più impostato qui, verrà chiesto separatamente in GAS_TIPO


@pytest.mark.asyncio
async def test_luce_tipo_variabile_tri(mock_callback_query, mock_context):
    """Test scelta luce variabile trioraria"""
    mock_callback_query.callback_query.data = "luce_tri"

    result = await luce_tipo_variabile(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data["luce_tipo"] == "variabile"
    assert mock_context.user_data["luce_fascia"] == "trioraria"


# ========== TEST INPUT VALIDI ==========


@pytest.mark.asyncio
async def test_luce_energia_valid_input(mock_update, mock_context):
    """Test input valido per energia luce"""
    mock_update.message.text = "0,145"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data["luce_energia"] == 0.145
    mock_update.message.reply_text.assert_called_once()


@pytest.mark.asyncio
async def test_luce_energia_dot_separator(mock_update, mock_context):
    """Test input con punto come separatore decimale"""
    mock_update.message.text = "0.145"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data["luce_energia"] == 0.145


@pytest.mark.asyncio
async def test_luce_comm_valid_input(mock_update, mock_context):
    """Test input valido per commercializzazione luce"""
    mock_update.message.text = "72"

    result = await luce_comm(mock_update, mock_context)

    assert result == VUOI_CONSUMI_LUCE
    assert mock_context.user_data["luce_comm"] == 72.0
    mock_update.message.reply_text.assert_called_once()


# ========== TEST INPUT NON VALIDI ==========


@pytest.mark.asyncio
async def test_luce_energia_invalid_string(mock_update, mock_context):
    """Test input non numerico per energia luce"""
    mock_update.message.text = "abc"
    mock_context.user_data["is_variabile"] = False

    result = await luce_energia(mock_update, mock_context)

    # Deve tornare allo stesso stato
    assert result == LUCE_ENERGIA
    # Deve mostrare errore
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "❌" in call_args[0][0]
    assert "valido" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_luce_energia_empty_string(mock_update, mock_context):
    """Test input vuoto per energia luce"""
    mock_update.message.text = ""
    mock_context.user_data["is_variabile"] = False

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_ENERGIA
    mock_update.message.reply_text.assert_called_once()


@pytest.mark.asyncio
async def test_luce_energia_special_chars(mock_update, mock_context):
    """Test input con caratteri speciali"""
    mock_update.message.text = "0,14€"
    mock_context.user_data["is_variabile"] = False

    result = await luce_energia(mock_update, mock_context)

    # ValueError perché € non è valido
    assert result == LUCE_ENERGIA
    mock_update.message.reply_text.assert_called_once()


@pytest.mark.asyncio
async def test_luce_comm_invalid_input(mock_update, mock_context):
    """Test input non valido per commercializzazione"""
    mock_update.message.text = "settantadue"

    result = await luce_comm(mock_update, mock_context)

    assert result == LUCE_COMM
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "❌" in call_args[0][0]


@pytest.mark.asyncio
async def test_gas_energia_invalid_input(mock_update, mock_context):
    """Test input non valido per energia gas"""
    mock_update.message.text = "invalid"
    mock_context.user_data["gas_tipo"] = "fissa"

    result = await gas_energia(mock_update, mock_context)

    assert result == GAS_ENERGIA
    mock_update.message.reply_text.assert_called_once()


@pytest.mark.asyncio
async def test_gas_comm_invalid_input(mock_update, mock_context):
    """Test input non valido per commercializzazione gas"""
    mock_update.message.text = "xyz123"

    result = await gas_comm(mock_update, mock_context)

    assert result == GAS_COMM
    mock_update.message.reply_text.assert_called_once()


# ========== TEST FLUSSI COMPLETI ==========


@pytest.mark.asyncio
async def test_has_gas_yes(mock_callback_query, mock_context):
    """Test flusso quando utente ha gas - ora va a GAS_TIPO per chiedere tipo tariffa"""
    mock_callback_query.callback_query.data = "gas_si"

    result = await ha_gas(mock_callback_query, mock_context)

    assert result == GAS_TIPO  # Ora chiede tipo tariffa gas
    mock_callback_query.callback_query.edit_message_text.assert_called_once()
    # Verifica che mostri la domanda sul tipo tariffa gas
    call_args = mock_callback_query.callback_query.edit_message_text.call_args
    assert (
        "tipo di tariffa" in call_args[1]["text"]
        if "text" in call_args[1]
        else "tipo di tariffa" in call_args[0][0]
    )


@pytest.mark.asyncio
async def test_has_gas_no(mock_callback_query, mock_context):
    """Test flusso quando utente non ha gas"""
    mock_callback_query.callback_query.data = "gas_no"
    mock_callback_query.callback_query.from_user.id = 123456789

    # Setup dati luce
    mock_context.user_data = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": 0.145,
        "luce_comm": 72.0,
    }

    result = await ha_gas(mock_callback_query, mock_context)

    # Deve salvare e terminare conversazione
    assert result == -1  # ConversationHandler.END

    # Verifica salvataggio nel database
    user_data = load_user("123456789")
    assert user_data is not None
    assert "luce" in user_data
    # Bot salva gas: None quando utente non ha gas
    assert user_data.get("gas") is None


@pytest.mark.asyncio
async def test_gas_tipo_tariffa_fissa(mock_callback_query, mock_context):
    """Test scelta gas fisso"""
    mock_callback_query.callback_query.data = "gas_tipo_fissa"

    result = await gas_tipo_tariffa(mock_callback_query, mock_context)

    assert result == GAS_ENERGIA
    assert mock_context.user_data["gas_tipo"] == "fissa"
    assert mock_context.user_data["gas_fascia"] == "monoraria"
    mock_callback_query.callback_query.edit_message_text.assert_called_once()
    # Verifica messaggio per gas fisso
    call_args = mock_callback_query.callback_query.edit_message_text.call_args
    msg = call_args[0][0]
    assert "Gas fisso" in msg


@pytest.mark.asyncio
async def test_gas_tipo_tariffa_variabile(mock_callback_query, mock_context):
    """Test scelta gas variabile"""
    mock_callback_query.callback_query.data = "gas_tipo_variabile"

    result = await gas_tipo_tariffa(mock_callback_query, mock_context)

    assert result == GAS_ENERGIA
    assert mock_context.user_data["gas_tipo"] == "variabile"
    assert mock_context.user_data["gas_fascia"] == "monoraria"
    mock_callback_query.callback_query.edit_message_text.assert_called_once()
    # Verifica messaggio per gas variabile
    call_args = mock_callback_query.callback_query.edit_message_text.call_args
    msg = call_args[0][0]
    assert "Gas variabile" in msg
    assert "PSV" in msg


@pytest.mark.asyncio
async def test_gas_energia_valid_input(mock_update, mock_context):
    """Test input valido per energia gas"""
    mock_update.message.text = "0,456"

    result = await gas_energia(mock_update, mock_context)

    assert result == GAS_COMM
    assert mock_context.user_data["gas_energia"] == 0.456


@pytest.mark.asyncio
async def test_complete_flow_fissa_with_gas(mock_update, mock_context):
    """Test flusso completo: tariffa fissa con gas"""
    user_id = "123456789"
    mock_update.effective_user.id = int(user_id)

    # Setup context con tutti i dati
    mock_context.user_data = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": 0.145,
        "luce_comm": 72.0,
        "gas_tipo": "fissa",
        "gas_fascia": "monoraria",
        "gas_energia": 0.456,
        "gas_comm": 84.0,
    }

    # Simula step gas_comm
    mock_update.message.text = "84"
    result = await gas_comm(mock_update, mock_context)

    assert result == VUOI_CONSUMI_GAS  # Chiede se vuole indicare consumo gas

    # Simula risposta "No" alla domanda consumo gas
    from types import SimpleNamespace

    mock_user = SimpleNamespace(id=int(user_id))
    mock_query = MagicMock(spec=CallbackQuery)
    mock_query.data = "consumi_gas_no"
    mock_query.answer = AsyncMock()
    mock_query.edit_message_text = AsyncMock()
    mock_query.from_user = mock_user

    mock_update.callback_query = mock_query

    result = await vuoi_consumi_gas(mock_update, mock_context)

    assert result == -1  # Fine conversazione

    # Verifica salvataggio completo nel database
    user_data = load_user(user_id)
    assert user_data is not None
    assert user_data["luce"]["energia"] == 0.145
    assert user_data["gas"]["energia"] == 0.456


@pytest.mark.asyncio
async def test_complete_flow_mixed_luce_fissa_gas_variabile(mock_update, mock_context):
    """Test flusso completo: luce fissa + gas variabile (combinazione mista)"""
    user_id = "123456789"
    mock_update.effective_user.id = int(user_id)

    # Setup context con luce fissa e gas variabile (combinazione mista)
    mock_context.user_data = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": 0.145,
        "luce_comm": 72.0,
        "gas_tipo": "variabile",  # Gas variabile mentre luce è fissa
        "gas_fascia": "monoraria",
        "gas_energia": 0.08,  # Spread PSV
        "gas_comm": 84.0,
    }

    # Simula step gas_comm
    mock_update.message.text = "84"
    result = await gas_comm(mock_update, mock_context)

    assert result == VUOI_CONSUMI_GAS

    # Simula risposta "No" alla domanda consumo gas
    from types import SimpleNamespace

    mock_user = SimpleNamespace(id=int(user_id))
    mock_query = MagicMock(spec=CallbackQuery)
    mock_query.data = "consumi_gas_no"
    mock_query.answer = AsyncMock()
    mock_query.edit_message_text = AsyncMock()
    mock_query.from_user = mock_user

    mock_update.callback_query = mock_query

    result = await vuoi_consumi_gas(mock_update, mock_context)

    assert result == -1  # Fine conversazione

    # Verifica salvataggio completo nel database
    user_data = load_user(user_id)
    assert user_data is not None
    # Luce fissa
    assert user_data["luce"]["tipo"] == "fissa"
    assert user_data["luce"]["energia"] == 0.145
    # Gas variabile (combinazione mista!)
    assert user_data["gas"]["tipo"] == "variabile"
    assert user_data["gas"]["energia"] == 0.08


@pytest.mark.asyncio
async def test_complete_flow_mixed_luce_variabile_gas_fissa(mock_update, mock_context):
    """Test flusso completo: luce variabile + gas fissa (combinazione mista inversa)"""
    user_id = "123456789"
    mock_update.effective_user.id = int(user_id)

    # Setup context con luce variabile e gas fissa (combinazione mista inversa)
    mock_context.user_data = {
        "luce_tipo": "variabile",
        "luce_fascia": "trioraria",
        "luce_energia": 0.025,  # Spread PUN
        "luce_comm": 72.0,
        "gas_tipo": "fissa",  # Gas fisso mentre luce è variabile
        "gas_fascia": "monoraria",
        "gas_energia": 0.456,  # Prezzo fisso
        "gas_comm": 84.0,
    }

    # Simula step gas_comm
    mock_update.message.text = "84"
    result = await gas_comm(mock_update, mock_context)

    assert result == VUOI_CONSUMI_GAS

    # Simula risposta "No" alla domanda consumo gas
    from types import SimpleNamespace

    mock_user = SimpleNamespace(id=int(user_id))
    mock_query = MagicMock(spec=CallbackQuery)
    mock_query.data = "consumi_gas_no"
    mock_query.answer = AsyncMock()
    mock_query.edit_message_text = AsyncMock()
    mock_query.from_user = mock_user

    mock_update.callback_query = mock_query

    result = await vuoi_consumi_gas(mock_update, mock_context)

    assert result == -1  # Fine conversazione

    # Verifica salvataggio completo nel database
    user_data = load_user(user_id)
    assert user_data is not None
    # Luce variabile
    assert user_data["luce"]["tipo"] == "variabile"
    assert user_data["luce"]["fascia"] == "trioraria"
    assert user_data["luce"]["energia"] == 0.025
    # Gas fisso (combinazione mista!)
    assert user_data["gas"]["tipo"] == "fissa"
    assert user_data["gas"]["energia"] == 0.456


# ========== TEST EDGE CASES ==========


@pytest.mark.asyncio
async def test_negative_values_rejected(mock_update, mock_context):
    """Test che valori negativi vengano rifiutati"""
    mock_update.message.text = "-0.5"

    result = await luce_energia(mock_update, mock_context)

    # Con validazione, il bot ora rifiuta negativi
    assert result == LUCE_ENERGIA  # Rimane nello stesso stato
    assert "luce_energia" not in mock_context.user_data  # Valore non salvato

    # Verifica messaggio di errore inviato
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "maggiore o uguale a zero" in call_args[0][0]


@pytest.mark.asyncio
async def test_very_large_numbers(mock_update, mock_context):
    """Test numeri molto grandi"""
    mock_update.message.text = "999999.99"

    result = await luce_energia(mock_update, mock_context)

    # Il bot accetta qualunque numero valido
    assert result == LUCE_COMM
    assert mock_context.user_data["luce_energia"] == 999999.99


@pytest.mark.asyncio
async def test_zero_values(mock_update, mock_context):
    """Test valori zero"""
    mock_update.message.text = "0"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data["luce_energia"] == 0.0


# ========== TEST CONFIRMATION MESSAGE WITH CONSUMPTION ==========


def test_format_confirmation_message_with_luce_monoraria_consumption():
    """Test messaggio di conferma con consumo luce monoraria"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
        },
        "gas": None,
    }

    message = _format_confirmation_message(user_data)

    assert "Consumo: <b>2700</b> kWh/anno" in message
    assert "Abbiamo finito!" in message
    assert "Luce (Fissa Monoraria)" in message


def test_format_confirmation_message_with_luce_trioraria_consumption():
    """Test messaggio di conferma con consumo luce trioraria"""
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.025,
            "commercializzazione": 72.0,
            "consumo_f1": 900.0,
            "consumo_f2": 850.0,
            "consumo_f3": 950.0,
        },
        "gas": None,
    }

    message = _format_confirmation_message(user_data)

    assert "Consumo: <b>2700</b> kWh/anno" in message
    assert "F1: 900 kWh" in message
    assert "F2: 850 kWh" in message
    assert "F3: 950 kWh" in message
    assert "Luce (Variabile Trioraria)" in message


def test_format_confirmation_message_with_gas_consumption():
    """Test messaggio di conferma con consumo gas"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
            "consumo_annuo": 1200.0,
        },
    }

    message = _format_confirmation_message(user_data)

    assert "Consumo: <b>1200</b> Smc/anno" in message
    assert "Gas (Fissa Monoraria)" in message


def test_format_confirmation_message_without_consumption():
    """Test messaggio di conferma senza consumi (backward compatibility)"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    message = _format_confirmation_message(user_data)

    assert "Consumo" not in message
    assert "Abbiamo finito!" in message


# ========== TEST CONSUMPTION COLLECTION FLOW ==========


@pytest.mark.asyncio
async def test_vuoi_consumi_luce_yes_monoraria(mock_update, mock_context):
    """Test risposta Sì a domanda consumi luce monoraria"""
    mock_context.user_data = {"luce_fascia": "monoraria"}

    mock_query = MagicMock(spec=CallbackQuery)
    mock_query.data = "consumi_luce_si"
    mock_query.answer = AsyncMock()
    mock_query.edit_message_text = AsyncMock()
    mock_update.callback_query = mock_query

    result = await vuoi_consumi_luce(mock_update, mock_context)

    assert result == LUCE_CONSUMO_F1
    mock_query.edit_message_text.assert_called_once()
    call_args = mock_query.edit_message_text.call_args[0][0]
    assert "consumo annuo totale di energia elettrica" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f1_monoraria_valid(mock_update, mock_context):
    """Test inserimento consumo luce monoraria valido"""
    mock_context.user_data = {"luce_fascia": "monoraria"}
    mock_update.message.text = "2700"

    result = await luce_consumo_f1(mock_update, mock_context)

    assert mock_context.user_data["luce_consumo_f1"] == 2700.0
    # Per monoraria dovrebbe andare a HA_GAS
    assert result == HA_GAS


@pytest.mark.asyncio
async def test_vuoi_consumi_gas_yes(mock_update, mock_context):
    """Test risposta Sì a domanda consumi gas"""
    from types import SimpleNamespace

    user_id = "123456789"
    mock_user = SimpleNamespace(id=int(user_id))
    mock_query = MagicMock(spec=CallbackQuery)
    mock_query.data = "consumi_gas_si"
    mock_query.answer = AsyncMock()
    mock_query.edit_message_text = AsyncMock()
    mock_query.from_user = mock_user
    mock_update.callback_query = mock_query

    result = await vuoi_consumi_gas(mock_update, mock_context)

    assert result == GAS_CONSUMO
    mock_query.edit_message_text.assert_called_once()
    call_args = mock_query.edit_message_text.call_args[0][0]
    assert "consumo annuo di gas in Smc" in call_args


@pytest.mark.asyncio
async def test_gas_consumo_valid(mock_update, mock_context):
    """Test inserimento consumo gas valido"""
    user_id = "123456789"
    mock_update.effective_user.id = int(user_id)
    mock_context.user_data = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": 0.145,
        "luce_comm": 72.0,
        "gas_tipo": "fissa",
        "gas_fascia": "monoraria",
        "gas_energia": 0.456,
        "gas_comm": 84.0,
    }
    mock_update.message.text = "1200"

    result = await gas_consumo(mock_update, mock_context)

    assert mock_context.user_data["gas_consumo_annuo"] == 1200.0
    assert result == -1  # Fine conversazione

    # Verifica salvataggio nel database
    user_data = load_user(user_id)
    assert user_data is not None
    assert user_data["gas"]["consumo_annuo"] == 1200.0


# ========== TEST ERROR HANDLING AND EDGE CASES ==========


@pytest.mark.asyncio
async def test_vuoi_consumi_luce_no():
    """Test quando l'utente non vuole inserire i consumi luce"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    query = AsyncMock(spec=CallbackQuery)
    query.data = "consumi_luce_no"
    query.answer = AsyncMock()
    query.edit_message_text = AsyncMock()
    update.callback_query = query

    context.user_data = {}

    result = await vuoi_consumi_luce(update, context)

    assert result == HA_GAS
    query.answer.assert_called_once()
    query.edit_message_text.assert_called_once()


@pytest.mark.asyncio
async def test_vuoi_consumi_luce_yes_trioraria():
    """Test quando l'utente vuole inserire consumi luce trioraria"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    query = AsyncMock(spec=CallbackQuery)
    query.data = "consumi_luce_si"
    query.answer = AsyncMock()
    query.edit_message_text = AsyncMock()
    update.callback_query = query

    context.user_data = {"luce_fascia": "trioraria"}

    result = await vuoi_consumi_luce(update, context)

    assert result == LUCE_CONSUMO_F1
    query.answer.assert_called_once()
    # Verifica che il messaggio menzioni F1
    call_args = query.edit_message_text.call_args[0][0]
    assert "F1" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f1_negative():
    """Test valore negativo per consumo F1"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "-100"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {"luce_fascia": "monoraria"}

    result = await luce_consumo_f1(update, context)

    assert result == LUCE_CONSUMO_F1
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "maggiore o uguale a zero" in call_args.lower()


@pytest.mark.asyncio
async def test_luce_consumo_f1_value_error():
    """Test ValueError per input non numerico F1"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "abc"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {"luce_fascia": "monoraria"}

    result = await luce_consumo_f1(update, context)

    assert result == LUCE_CONSUMO_F1
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "numero valido" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f1_trioraria_valid():
    """Test valore valido F1 trioraria che va a F2"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "900"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {"luce_fascia": "trioraria"}

    result = await luce_consumo_f1(update, context)

    assert result == LUCE_CONSUMO_F2
    assert context.user_data["luce_consumo_f1"] == 900.0
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "F2" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f2_negative():
    """Test valore negativo per consumo F2"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "-100"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f2(update, context)

    assert result == LUCE_CONSUMO_F2
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "maggiore o uguale a zero" in call_args.lower()


@pytest.mark.asyncio
async def test_luce_consumo_f2_value_error():
    """Test ValueError per input non numerico F2"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "xyz"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f2(update, context)

    assert result == LUCE_CONSUMO_F2
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "numero valido" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f2_valid():
    """Test valore valido F2 che va a F3"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "850"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f2(update, context)

    assert result == LUCE_CONSUMO_F3
    assert context.user_data["luce_consumo_f2"] == 850.0
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "F3" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f3_negative():
    """Test valore negativo per consumo F3"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "-100"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f3(update, context)

    assert result == LUCE_CONSUMO_F3
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "maggiore o uguale a zero" in call_args.lower()


@pytest.mark.asyncio
async def test_luce_consumo_f3_value_error():
    """Test ValueError per input non numerico F3"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "invalid"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f3(update, context)

    assert result == LUCE_CONSUMO_F3
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "numero valido" in call_args


@pytest.mark.asyncio
async def test_luce_consumo_f3_valid():
    """Test valore valido F3 che va a HA_GAS"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "950"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await luce_consumo_f3(update, context)

    assert result == HA_GAS
    assert context.user_data["luce_consumo_f3"] == 950.0
    message.reply_text.assert_called_once()


@pytest.mark.asyncio
async def test_gas_energia_negative():
    """Test valore negativo per gas energia"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "-0.5"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await gas_energia(update, context)

    assert result == GAS_ENERGIA
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "maggiore o uguale a zero" in call_args.lower()


@pytest.mark.asyncio
async def test_gas_energia_value_error_variabile():
    """Test ValueError per gas energia variabile"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "invalid"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {"gas_tipo": "variabile"}

    result = await gas_energia(update, context)

    assert result == GAS_ENERGIA
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "numero valido" in call_args
    assert "0,08" in call_args  # Esempio per variabile


@pytest.mark.asyncio
async def test_gas_energia_value_error_fissa():
    """Test ValueError per gas energia fissa"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "invalid"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {"gas_tipo": "fissa"}

    result = await gas_energia(update, context)

    assert result == GAS_ENERGIA
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "numero valido" in call_args
    assert "0,456" in call_args  # Esempio per fissa


@pytest.mark.asyncio
async def test_gas_comm_negative():
    """Test valore negativo per gas commercializzazione"""
    update = MagicMock(spec=Update)
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)

    message = AsyncMock(spec=Message)
    message.text = "-50"
    message.reply_text = AsyncMock()
    update.message = message

    context.user_data = {}

    result = await gas_comm(update, context)

    assert result == GAS_COMM
    message.reply_text.assert_called_once()
    call_args = message.reply_text.call_args[0][0]
    assert "maggiore o uguale a zero" in call_args.lower()


# ========== TEST VALIDAZIONE INPUT NUMERICI (PROTEZIONE ATTACCHI) ==========


def test_validate_numeric_input_valid():
    """Test validazione input numerico valido"""
    value, error = validate_numeric_input("123.45")
    assert value == 123.45
    assert error is None


def test_validate_numeric_input_valid_with_comma():
    """Test validazione input numerico valido con virgola"""
    value, error = validate_numeric_input("123,45")
    assert value == 123.45
    assert error is None


def test_validate_numeric_input_zero():
    """Test validazione input zero (valido)"""
    value, error = validate_numeric_input("0")
    assert value == 0.0
    assert error is None


def test_validate_numeric_input_small_decimal():
    """Test validazione input decimale piccolo (es. spread)"""
    value, error = validate_numeric_input("0.0088")
    assert value == 0.0088
    assert error is None


def test_validate_numeric_input_negative():
    """Test validazione input negativo (invalido)"""
    value, error = validate_numeric_input("-10")
    assert value is None
    assert error is not None
    assert "maggiore o uguale a zero" in error.lower()


def test_validate_numeric_input_too_long():
    """Test validazione input troppo lungo (protezione attacchi)"""
    # Input con 11 caratteri (max 10)
    value, error = validate_numeric_input("12345678901")
    assert value is None
    assert error is not None
    assert "troppo lungo" in error.lower()
    assert "10" in error  # Verifica menzione del limite


def test_validate_numeric_input_very_long():
    """Test validazione input molto lungo (simulazione attacco)"""
    # Input con 100 caratteri
    value, error = validate_numeric_input("1" * 100)
    assert value is None
    assert error is not None
    assert "troppo lungo" in error.lower()


def test_validate_numeric_input_invalid_string():
    """Test validazione input non numerico"""
    value, error = validate_numeric_input("abc")
    assert value is None
    assert error is None  # None significa errore di conversione, gestito dal chiamante


def test_validate_numeric_input_empty():
    """Test validazione input vuoto"""
    value, error = validate_numeric_input("")
    assert value is None
    assert error is None  # ValueError gestito dal chiamante


def test_validate_numeric_input_special_chars():
    """Test validazione input con caratteri speciali"""
    value, error = validate_numeric_input("12.34€")
    assert value is None
    assert error is None  # ValueError gestito dal chiamante


def test_validate_numeric_input_at_max_length():
    """Test validazione input esattamente al limite (10 caratteri)"""
    # 10 caratteri esatti - dovrebbe essere valido
    value, error = validate_numeric_input("1234567.89")
    assert value == 1234567.89
    assert error is None


@pytest.mark.asyncio
async def test_luce_energia_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per energia luce (protezione attacchi)"""
    # Input con 50 cifre
    mock_update.message.text = "1" * 50
    mock_context.user_data["is_variabile"] = False

    result = await luce_energia(mock_update, mock_context)

    # Deve tornare allo stesso stato
    assert result == LUCE_ENERGIA
    # Deve mostrare errore specifico
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    error_msg = call_args[0][0]
    assert "troppo lungo" in error_msg.lower()
    assert "10" in error_msg


@pytest.mark.asyncio
async def test_luce_comm_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per commercializzazione luce"""
    mock_update.message.text = "9" * 20

    result = await luce_comm(mock_update, mock_context)

    assert result == LUCE_COMM
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_luce_consumo_f1_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per consumo luce F1"""
    mock_update.message.text = "8" * 15

    result = await luce_consumo_f1(mock_update, mock_context)

    assert result == LUCE_CONSUMO_F1
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_luce_consumo_f2_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per consumo luce F2"""
    mock_update.message.text = "7" * 12

    result = await luce_consumo_f2(mock_update, mock_context)

    assert result == LUCE_CONSUMO_F2
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_luce_consumo_f3_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per consumo luce F3"""
    mock_update.message.text = "6" * 11

    result = await luce_consumo_f3(mock_update, mock_context)

    assert result == LUCE_CONSUMO_F3
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_gas_energia_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per energia gas"""
    mock_update.message.text = "5" * 30
    mock_context.user_data["gas_tipo"] = "fissa"

    result = await gas_energia(mock_update, mock_context)

    assert result == GAS_ENERGIA
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_gas_comm_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per commercializzazione gas"""
    mock_update.message.text = "4" * 25

    result = await gas_comm(mock_update, mock_context)

    assert result == GAS_COMM
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_gas_consumo_too_long_input(mock_update, mock_context):
    """Test input troppo lungo per consumo gas"""
    mock_update.message.text = "3" * 40

    result = await gas_consumo(mock_update, mock_context)

    assert result == GAS_CONSUMO
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0].lower()
