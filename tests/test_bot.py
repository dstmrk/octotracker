"""
Test per bot.py - Conversazioni Telegram e comandi

Testa tutti i flussi conversazionali del bot:
- Comandi: /start, /update, /status, /remove, /help
- Gestione comandi non riconosciuti
- Conversazione registrazione con input validi
- Gestione errori con input non validi
- Flussi completi: luce fissa, variabile mono/tri, con/senza gas
"""

import os
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram import CallbackQuery, InlineKeyboardMarkup, Message, Update, User
from telegram.ext import ContextTypes

# Mock WEBHOOK_SECRET prima di importare bot (previene ValueError)
os.environ["WEBHOOK_SECRET"] = "test_secret_token_for_testing_only"

# Import funzioni del bot e database
import sys

sys.path.insert(0, str(Path(__file__).parent.parent))
import tempfile

import database
from bot import (
    GAS_COMM,
    GAS_ENERGIA,
    LUCE_COMM,
    LUCE_ENERGIA,
    LUCE_TIPO_VARIABILE,
    TIPO_TARIFFA,
    VUOI_CONSUMI_GAS,
    VUOI_CONSUMI_LUCE,
    gas_comm,
    gas_energia,
    ha_gas,
    help_command,
    luce_comm,
    luce_energia,
    luce_tipo_variabile,
    remove_data,
    start,
    status,
    tipo_tariffa,
    unknown_command,
    vuoi_consumi_gas,
)
from database import init_db, load_user, save_user

# ========== FIXTURES ==========


@pytest.fixture(autouse=True)
def temp_database(monkeypatch):
    """Usa database temporaneo per ogni test"""
    with tempfile.TemporaryDirectory() as tmpdir:
        temp_db = Path(tmpdir) / "test_users.db"
        monkeypatch.setattr(database, "DB_FILE", temp_db)
        init_db()
        yield temp_db


@pytest.fixture
def mock_update():
    """Crea mock Update con message"""
    update = MagicMock(spec=Update)
    update.effective_user = MagicMock(spec=User)
    update.effective_user.id = 123456789
    update.effective_user.first_name = "TestUser"

    update.message = MagicMock(spec=Message)
    update.message.reply_text = AsyncMock()
    update.message.text = ""

    update.callback_query = None

    return update


@pytest.fixture
def mock_callback_query():
    """Crea mock CallbackQuery per pulsanti inline"""
    query = MagicMock(spec=CallbackQuery)
    query.answer = AsyncMock()
    query.edit_message_text = AsyncMock()
    query.from_user = MagicMock(spec=User)
    query.from_user.id = 123456789
    query.data = ""

    # Aggiungi query all'update
    update = MagicMock(spec=Update)
    update.callback_query = query
    update.effective_user = query.from_user
    update.message = None

    return update


@pytest.fixture
def mock_context():
    """Crea mock ContextTypes.DEFAULT_TYPE"""
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)
    context.user_data = {}
    return context


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
async def test_unknown_command(mock_update, mock_context):
    """Test comando non riconosciuto mostra messaggio di aiuto"""
    mock_update.message.text = "/unknown"
    await unknown_command(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica messaggio contiene indicazioni per /help
    assert "non riconosciuto" in message_text.lower()
    assert "/help" in message_text


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
async def test_help_command(mock_update, mock_context):
    """Test /help mostra comandi disponibili"""
    await help_command(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza comandi principali
    assert "/start" in message_text
    assert "/status" in message_text
    assert "/remove" in message_text


@pytest.mark.asyncio
async def test_status_no_data(mock_update, mock_context):
    """Test /status senza dati salvati (database vuoto)"""
    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "Non hai ancora registrato" in call_args[0][0]


@pytest.mark.asyncio
async def test_status_with_data(mock_update, mock_context):
    """Test /status con dati salvati"""
    # Prepara dati utente nel database
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza dati luce
    assert "Luce" in message_text
    assert "0,145" in message_text or "0.145" in message_text
    assert "72" in message_text


@pytest.mark.asyncio
async def test_status_with_consumption_monoraria(mock_update, mock_context):
    """Test /status mostra consumi per tariffa monoraria"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
        }
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza consumo monoraria
    assert "Consumo:" in message_text
    assert "2700" in message_text
    assert "kWh/anno" in message_text
    # Non deve mostrare breakdown fasce per monoraria
    assert "F1:" not in message_text


@pytest.mark.asyncio
async def test_status_with_consumption_bioraria(mock_update, mock_context):
    """Test /status mostra consumi per tariffa bioraria"""
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "bioraria",
            "energia": 0.015,
            "commercializzazione": 80.0,
            "consumo_f1": 1200.0,
            "consumo_f2": 1500.0,
        }
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza consumo bioraria con breakdown
    assert "Consumo:" in message_text
    assert "2700" in message_text  # Totale
    assert "kWh/anno" in message_text
    assert "F1: 1200 kWh" in message_text
    assert "F23: 1500 kWh" in message_text


@pytest.mark.asyncio
async def test_status_with_consumption_trioraria(mock_update, mock_context):
    """Test /status mostra consumi per tariffa trioraria"""
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.012,
            "commercializzazione": 96.0,
            "consumo_f1": 900.0,
            "consumo_f2": 900.0,
            "consumo_f3": 900.0,
        }
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza consumo trioraria con breakdown
    assert "Consumo:" in message_text
    assert "2700" in message_text  # Totale
    assert "kWh/anno" in message_text
    assert "F1: 900 kWh" in message_text
    assert "F2: 900 kWh" in message_text
    assert "F3: 900 kWh" in message_text


@pytest.mark.asyncio
async def test_status_with_consumption_gas(mock_update, mock_context):
    """Test /status mostra consumo gas"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.140,
            "commercializzazione": 70.0,
            "consumo_f1": 2500.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.350,
            "commercializzazione": 120.0,
            "consumo_annuo": 1200.0,
        },
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza consumo luce
    assert "2500" in message_text
    assert "kWh/anno" in message_text

    # Verifica presenza consumo gas
    assert "1200" in message_text
    assert "Smc/anno" in message_text


@pytest.mark.asyncio
async def test_status_backward_compat_no_consumption(mock_update, mock_context):
    """Test /status funziona anche senza consumi (retrocompatibilità)"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            # Nessun campo consumo
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.025,
            "commercializzazione": 100.0,
            # Nessun campo consumo
        },
    }
    save_user("123456789", user_data)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza tariffe (funziona senza consumi)
    assert "Luce" in message_text
    assert "0,145" in message_text or "0.145" in message_text
    assert "Gas" in message_text

    # Verifica che NON ci sia la riga "Consumo:"
    assert "Consumo:" not in message_text


@pytest.mark.asyncio
async def test_remove_command(mock_update, mock_context):
    """Test /remove rimuove dati utente"""
    # Prepara dati nel database
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    await remove_data(mock_update, mock_context)

    # Verifica utente rimosso dal database
    assert load_user("123456789") is None
    mock_update.message.reply_text.assert_called_once()


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
    assert mock_context.user_data["gas_tipo"] == "variabile"


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
    mock_context.user_data["is_variabile"] = False

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
    """Test flusso quando utente ha gas"""
    mock_callback_query.callback_query.data = "gas_si"
    mock_context.user_data["is_variabile"] = False

    result = await ha_gas(mock_callback_query, mock_context)

    assert result == GAS_ENERGIA
    mock_callback_query.callback_query.edit_message_text.assert_called_once()


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
    # Usa SimpleNamespace per avere attributi semplici senza auto-mocking
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


# ========== TEST EDGE CASES ==========


@pytest.mark.asyncio
async def test_negative_values_rejected(mock_update, mock_context):
    """Test che valori negativi vengano rifiutati"""
    mock_update.message.text = "-0.5"

    result = await luce_energia(mock_update, mock_context)

    # Con miglioramento #10, il bot ora rifiuta negativi
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
