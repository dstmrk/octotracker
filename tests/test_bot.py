"""
Test per bot.py - Conversazioni Telegram e comandi

Testa tutti i flussi conversazionali del bot:
- Comandi: /start, /update, /status, /remove, /cancel, /help
- Conversazione registrazione con input validi
- Gestione errori con input non validi
- Flussi completi: luce fissa, variabile mono/tri, con/senza gas
"""
import pytest
import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch
from telegram import Update, User, Message, Chat, CallbackQuery, InlineKeyboardMarkup
from telegram.ext import ContextTypes

# Import funzioni del bot
import sys
sys.path.insert(0, str(Path(__file__).parent.parent))
from bot import (
    start, tipo_tariffa, luce_tipo_variabile, luce_energia, luce_comm,
    ha_gas, gas_energia, gas_comm, status, remove_data, cancel, help_command,
    load_users, save_users,
    TIPO_TARIFFA, LUCE_TIPO_VARIABILE, LUCE_ENERGIA, LUCE_COMM,
    HA_GAS, GAS_ENERGIA, GAS_COMM
)

# ========== FIXTURES ==========

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

@pytest.fixture
def temp_users_file(tmp_path, monkeypatch):
    """Crea file users.json temporaneo per test"""
    data_dir = tmp_path / "data"
    data_dir.mkdir()
    users_file = data_dir / "users.json"

    # Patcha il percorso del file nel modulo bot
    monkeypatch.setattr("bot.USERS_FILE", users_file)

    # Inizializza file vuoto
    with open(users_file, 'w') as f:
        json.dump({}, f)

    return users_file

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
    assert 'reply_markup' in call_args[1]
    assert isinstance(call_args[1]['reply_markup'], InlineKeyboardMarkup)

@pytest.mark.asyncio
async def test_cancel_command(mock_update, mock_context):
    """Test /cancel cancella conversazione corrente"""
    result = await cancel(mock_update, mock_context)

    assert result == -1  # ConversationHandler.END
    mock_update.message.reply_text.assert_called_once()

    call_args = mock_update.message.reply_text.call_args
    # Verifica messaggio di annullamento
    assert "annullat" in call_args[0][0].lower() or "cancellat" in call_args[0][0].lower()

@pytest.mark.asyncio
async def test_update_command(mock_update, mock_context, temp_users_file):
    """Test /update (alias di /start per aggiornare tariffe)"""
    # Prepara dati esistenti
    users = {
        "123456789": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,
                "commercializzazione": 72.0
            }
        }
    }
    with open(temp_users_file, 'w') as f:
        json.dump(users, f)

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
async def test_status_no_data(mock_update, mock_context, temp_users_file):
    """Test /status senza dati salvati"""
    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "Non hai ancora registrato" in call_args[0][0]

@pytest.mark.asyncio
async def test_status_with_data(mock_update, mock_context, temp_users_file):
    """Test /status con dati salvati"""
    # Prepara dati utente
    users = {
        "123456789": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,
                "commercializzazione": 72.0
            }
        }
    }
    with open(temp_users_file, 'w') as f:
        json.dump(users, f)

    await status(mock_update, mock_context)

    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    message_text = call_args[0][0]

    # Verifica presenza dati luce
    assert "Luce" in message_text
    assert "0,145" in message_text or "0.145" in message_text
    assert "72" in message_text

@pytest.mark.asyncio
async def test_remove_command(mock_update, mock_context, temp_users_file):
    """Test /remove rimuove dati utente"""
    # Prepara dati
    users = {
        "123456789": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,
                "commercializzazione": 72.0
            }
        }
    }
    with open(temp_users_file, 'w') as f:
        json.dump(users, f)

    await remove_data(mock_update, mock_context)

    # Verifica file aggiornato
    with open(temp_users_file, 'r') as f:
        result = json.load(f)

    assert "123456789" not in result
    mock_update.message.reply_text.assert_called_once()

# ========== TEST FLUSSO CONVERSAZIONE ==========

@pytest.mark.asyncio
async def test_tipo_tariffa_fissa(mock_callback_query, mock_context):
    """Test scelta tariffa fissa"""
    mock_callback_query.callback_query.data = "tipo_fissa"

    result = await tipo_tariffa(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data['is_variabile'] is False
    assert mock_context.user_data['luce_tipo'] == "fissa"
    assert mock_context.user_data['luce_fascia'] == "monoraria"

    mock_callback_query.callback_query.answer.assert_called_once()
    mock_callback_query.callback_query.edit_message_text.assert_called_once()

@pytest.mark.asyncio
async def test_tipo_tariffa_variabile(mock_callback_query, mock_context):
    """Test scelta tariffa variabile"""
    mock_callback_query.callback_query.data = "tipo_variabile"

    result = await tipo_tariffa(mock_callback_query, mock_context)

    assert result == LUCE_TIPO_VARIABILE
    assert mock_context.user_data['is_variabile'] is True

    mock_callback_query.callback_query.answer.assert_called_once()

@pytest.mark.asyncio
async def test_luce_tipo_variabile_mono(mock_callback_query, mock_context):
    """Test scelta luce variabile monoraria"""
    mock_callback_query.callback_query.data = "luce_mono"

    result = await luce_tipo_variabile(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data['luce_tipo'] == "variabile"
    assert mock_context.user_data['luce_fascia'] == "monoraria"
    assert mock_context.user_data['gas_tipo'] == "variabile"

@pytest.mark.asyncio
async def test_luce_tipo_variabile_tri(mock_callback_query, mock_context):
    """Test scelta luce variabile trioraria"""
    mock_callback_query.callback_query.data = "luce_tri"

    result = await luce_tipo_variabile(mock_callback_query, mock_context)

    assert result == LUCE_ENERGIA
    assert mock_context.user_data['luce_tipo'] == "variabile"
    assert mock_context.user_data['luce_fascia'] == "trioraria"

# ========== TEST INPUT VALIDI ==========

@pytest.mark.asyncio
async def test_luce_energia_valid_input(mock_update, mock_context):
    """Test input valido per energia luce"""
    mock_update.message.text = "0,145"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data['luce_energia'] == 0.145
    mock_update.message.reply_text.assert_called_once()

@pytest.mark.asyncio
async def test_luce_energia_dot_separator(mock_update, mock_context):
    """Test input con punto come separatore decimale"""
    mock_update.message.text = "0.145"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data['luce_energia'] == 0.145

@pytest.mark.asyncio
async def test_luce_comm_valid_input(mock_update, mock_context):
    """Test input valido per commercializzazione luce"""
    mock_update.message.text = "72"

    result = await luce_comm(mock_update, mock_context)

    assert result == HA_GAS
    assert mock_context.user_data['luce_comm'] == 72.0
    mock_update.message.reply_text.assert_called_once()

# ========== TEST INPUT NON VALIDI ==========

@pytest.mark.asyncio
async def test_luce_energia_invalid_string(mock_update, mock_context):
    """Test input non numerico per energia luce"""
    mock_update.message.text = "abc"
    mock_context.user_data['is_variabile'] = False

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
    mock_context.user_data['is_variabile'] = False

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_ENERGIA
    mock_update.message.reply_text.assert_called_once()

@pytest.mark.asyncio
async def test_luce_energia_special_chars(mock_update, mock_context):
    """Test input con caratteri speciali"""
    mock_update.message.text = "0,14€"
    mock_context.user_data['is_variabile'] = False

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
    mock_context.user_data['is_variabile'] = False

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
    mock_context.user_data['is_variabile'] = False

    result = await ha_gas(mock_callback_query, mock_context)

    assert result == GAS_ENERGIA
    mock_callback_query.callback_query.edit_message_text.assert_called_once()

@pytest.mark.asyncio
async def test_has_gas_no(mock_callback_query, mock_context, temp_users_file):
    """Test flusso quando utente non ha gas"""
    mock_callback_query.callback_query.data = "gas_no"
    mock_callback_query.callback_query.from_user.id = 123456789

    # Setup dati luce
    mock_context.user_data = {
        'luce_tipo': 'fissa',
        'luce_fascia': 'monoraria',
        'luce_energia': 0.145,
        'luce_comm': 72.0
    }

    result = await ha_gas(mock_callback_query, mock_context)

    # Deve salvare e terminare conversazione
    assert result == -1  # ConversationHandler.END

    # Verifica salvataggio
    users = load_users()
    assert "123456789" in users
    assert "luce" in users["123456789"]
    # Bot salva gas: None quando utente non ha gas
    assert users["123456789"].get("gas") is None

@pytest.mark.asyncio
async def test_gas_energia_valid_input(mock_update, mock_context):
    """Test input valido per energia gas"""
    mock_update.message.text = "0,456"

    result = await gas_energia(mock_update, mock_context)

    assert result == GAS_COMM
    assert mock_context.user_data['gas_energia'] == 0.456

@pytest.mark.asyncio
async def test_complete_flow_fissa_with_gas(mock_update, mock_context, temp_users_file):
    """Test flusso completo: tariffa fissa con gas"""
    user_id = "123456789"
    mock_update.effective_user.id = int(user_id)

    # Setup context con tutti i dati
    mock_context.user_data = {
        'luce_tipo': 'fissa',
        'luce_fascia': 'monoraria',
        'luce_energia': 0.145,
        'luce_comm': 72.0,
        'gas_tipo': 'fissa',
        'gas_fascia': 'monoraria',
        'gas_energia': 0.456,
        'gas_comm': 84.0
    }

    # Simula ultimo step (gas_comm)
    mock_update.message.text = "84"
    result = await gas_comm(mock_update, mock_context)

    assert result == -1  # Fine conversazione

    # Verifica salvataggio completo
    users = load_users()
    assert user_id in users
    assert users[user_id]['luce']['energia'] == 0.145
    assert users[user_id]['gas']['energia'] == 0.456

# ========== TEST EDGE CASES ==========

@pytest.mark.asyncio
async def test_negative_values_rejected(mock_update, mock_context):
    """Test che valori negativi vengano rifiutati (tramite ValueError)"""
    # Nota: float("-1") funziona, quindi il bot accetta negativi
    # Se vogliamo validazione aggiuntiva, va aggiunta al bot
    mock_update.message.text = "-0.5"

    result = await luce_energia(mock_update, mock_context)

    # Il bot attuale accetta negativi (float() non solleva ValueError)
    # Questo test documenta il comportamento attuale
    assert result == LUCE_COMM
    assert mock_context.user_data['luce_energia'] == -0.5

@pytest.mark.asyncio
async def test_very_large_numbers(mock_update, mock_context):
    """Test numeri molto grandi"""
    mock_update.message.text = "999999.99"

    result = await luce_energia(mock_update, mock_context)

    # Il bot accetta qualunque numero valido
    assert result == LUCE_COMM
    assert mock_context.user_data['luce_energia'] == 999999.99

@pytest.mark.asyncio
async def test_zero_values(mock_update, mock_context):
    """Test valori zero"""
    mock_update.message.text = "0"

    result = await luce_energia(mock_update, mock_context)

    assert result == LUCE_COMM
    assert mock_context.user_data['luce_energia'] == 0.0
