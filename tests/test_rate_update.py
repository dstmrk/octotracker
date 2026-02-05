"""
Test per handlers/rate_update.py - Aggiornamento tariffe via pulsanti inline

Testa:
- Callback "Aggiorna tariffe" con e senza pending_rates
- Callback "No grazie"
- Aggiornamento messaggio dopo click
- Funzioni database pending_rates
- Funzione _build_pending_rates del checker
"""

import os
import sys
import tempfile
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from telegram import CallbackQuery, Message, Update, User
from telegram.constants import ParseMode

# Mock WEBHOOK_SECRET prima di importare bot
os.environ["WEBHOOK_SECRET"] = "test_secret_token_for_testing_only"

sys.path.insert(0, str(Path(__file__).parent.parent))

import database
from database import (
    clear_pending_rates,
    init_db,
    load_pending_rates,
    load_user,
    save_pending_rates,
    save_user,
)
from handlers.rate_update import (
    CONFIRMED_TEXT,
    DECLINED_TEXT,
    PROMPT_TEXT,
    rate_update_no,
    rate_update_yes,
)

# ========== FIXTURES ==========


@pytest.fixture(autouse=True)
def temp_database(monkeypatch):
    """Usa database temporaneo per ogni test"""
    with tempfile.TemporaryDirectory() as tmpdir:
        temp_db = Path(tmpdir) / "test_octotracker.db"
        monkeypatch.setattr(database, "DB_FILE", temp_db)
        init_db()
        yield temp_db


@pytest.fixture
def sample_user_data():
    """Dati utente di esempio"""
    return {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
    }


@pytest.fixture
def sample_user_data_with_gas():
    """Dati utente di esempio con gas"""
    return {
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
        },
    }


@pytest.fixture
def sample_pending_rates():
    """Tariffe pendenti di esempio"""
    return {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.130,
            "commercializzazione": 65.0,
        },
    }


@pytest.fixture
def mock_callback_query():
    """Crea mock CallbackQuery per pulsanti inline"""
    query = MagicMock(spec=CallbackQuery)
    query.answer = AsyncMock()
    query.edit_message_text = AsyncMock()
    query.message = MagicMock(spec=Message)
    query.message.text_html = (
        "⚡️ <b>Buone notizie!</b>\n"
        "Testo notifica...\n\n"
        f"{PROMPT_TEXT}\n\n"
        "🔗 Maggiori info: https://octopusenergy.it"
    )
    query.data = ""
    return query


@pytest.fixture
def mock_update(mock_callback_query):
    """Crea mock Update con callback_query"""
    update = MagicMock(spec=Update)
    update.effective_user = MagicMock(spec=User)
    update.effective_user.id = 123456789
    update.callback_query = mock_callback_query
    return update


@pytest.fixture
def mock_context():
    """Crea mock context"""
    context = MagicMock()
    context.user_data = {}
    return context


# ========== TEST DATABASE PENDING RATES ==========


def test_save_and_load_pending_rates(sample_user_data, sample_pending_rates):
    """Test salvataggio e caricamento pending_rates"""
    user_id = "123456789"
    save_user(user_id, sample_user_data)

    result = save_pending_rates(user_id, sample_pending_rates)
    assert result is True

    loaded = load_pending_rates(user_id)
    assert loaded is not None
    assert loaded["luce"]["energia"] == 0.130
    assert loaded["luce"]["commercializzazione"] == 65.0


def test_load_pending_rates_empty(sample_user_data):
    """Test caricamento pending_rates quando non ci sono"""
    user_id = "123456789"
    save_user(user_id, sample_user_data)

    loaded = load_pending_rates(user_id)
    assert loaded is None


def test_load_pending_rates_nonexistent_user():
    """Test caricamento pending_rates per utente inesistente"""
    loaded = load_pending_rates("999999")
    assert loaded is None


def test_clear_pending_rates(sample_user_data, sample_pending_rates):
    """Test rimozione pending_rates"""
    user_id = "123456789"
    save_user(user_id, sample_user_data)
    save_pending_rates(user_id, sample_pending_rates)

    result = clear_pending_rates(user_id)
    assert result is True

    loaded = load_pending_rates(user_id)
    assert loaded is None


def test_pending_rates_migration():
    """Test che la migration aggiunge correttamente la colonna pending_rates"""
    with database.get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = [row[1] for row in cursor.fetchall()]
        assert "pending_rates" in columns


def test_pending_rates_migration_idempotent():
    """Test che la migration pending_rates è idempotente"""
    from database import _migrate_pending_rates

    _migrate_pending_rates()

    with database.get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = [row[1] for row in cursor.fetchall()]
        assert "pending_rates" in columns


# ========== TEST HANDLER RATE UPDATE YES ==========


@pytest.mark.asyncio
async def test_rate_update_yes_success(
    mock_update, mock_context, sample_user_data, sample_pending_rates
):
    """Test click 'Aggiorna tariffe' con successo"""
    user_id = str(mock_update.effective_user.id)
    save_user(user_id, sample_user_data)
    save_pending_rates(user_id, sample_pending_rates)

    await rate_update_yes(mock_update, mock_context)

    # Verifica che query.answer sia stata chiamata
    mock_update.callback_query.answer.assert_called_once()

    # Verifica che il messaggio sia stato aggiornato con conferma
    mock_update.callback_query.edit_message_text.assert_called_once()
    call_args = mock_update.callback_query.edit_message_text.call_args
    assert CONFIRMED_TEXT in call_args.kwargs["text"]
    assert PROMPT_TEXT not in call_args.kwargs["text"]

    # Verifica che le tariffe siano state aggiornate nel DB
    updated_user = load_user(user_id)
    assert updated_user["luce"]["energia"] == 0.130
    assert updated_user["luce"]["commercializzazione"] == 65.0

    # Verifica che le pending_rates siano state rimosse
    assert load_pending_rates(user_id) is None


@pytest.mark.asyncio
async def test_rate_update_yes_with_gas(mock_update, mock_context, sample_user_data_with_gas):
    """Test click 'Aggiorna tariffe' con gas"""
    user_id = str(mock_update.effective_user.id)
    save_user(user_id, sample_user_data_with_gas)

    pending_with_gas = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.130,
            "commercializzazione": 65.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.420,
            "commercializzazione": 80.0,
        },
    }
    save_pending_rates(user_id, pending_with_gas)

    await rate_update_yes(mock_update, mock_context)

    # Verifica aggiornamento tariffe luce e gas
    updated_user = load_user(user_id)
    assert updated_user["luce"]["energia"] == 0.130
    assert updated_user["gas"]["energia"] == 0.420
    assert updated_user["gas"]["commercializzazione"] == 80.0


@pytest.mark.asyncio
async def test_rate_update_yes_no_pending(mock_update, mock_context, sample_user_data):
    """Test click 'Aggiorna tariffe' senza tariffe pendenti"""
    user_id = str(mock_update.effective_user.id)
    save_user(user_id, sample_user_data)

    await rate_update_yes(mock_update, mock_context)

    # Verifica che il messaggio sia stato aggiornato con messaggio di fallback
    mock_update.callback_query.edit_message_text.assert_called_once()
    call_args = mock_update.callback_query.edit_message_text.call_args
    assert DECLINED_TEXT in call_args.kwargs["text"]

    # Verifica che le tariffe originali non siano cambiate
    user = load_user(user_id)
    assert user["luce"]["energia"] == 0.145


@pytest.mark.asyncio
async def test_rate_update_yes_preserves_last_notified(
    mock_update, mock_context, sample_user_data, sample_pending_rates
):
    """Test che l'aggiornamento preserva last_notified_rates"""
    user_id = str(mock_update.effective_user.id)
    last_notified = {"luce": {"energia": 0.130, "commercializzazione": 65.0}}
    sample_user_data["last_notified_rates"] = last_notified
    save_user(user_id, sample_user_data)
    save_pending_rates(user_id, sample_pending_rates)

    await rate_update_yes(mock_update, mock_context)

    updated_user = load_user(user_id)
    assert updated_user.get("last_notified_rates") == last_notified


@pytest.mark.asyncio
async def test_rate_update_yes_preserves_consumption(mock_update, mock_context):
    """Test che l'aggiornamento preserva i consumi se presenti nelle pending_rates"""
    user_id = str(mock_update.effective_user.id)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
        },
    }
    save_user(user_id, user_data)

    pending = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.130,
            "commercializzazione": 65.0,
            "consumo_f1": 2700.0,
        },
    }
    save_pending_rates(user_id, pending)

    await rate_update_yes(mock_update, mock_context)

    updated_user = load_user(user_id)
    assert updated_user["luce"]["energia"] == 0.130
    assert updated_user["luce"]["consumo_f1"] == 2700.0


# ========== TEST HANDLER RATE UPDATE NO ==========


@pytest.mark.asyncio
async def test_rate_update_no(mock_update, mock_context, sample_user_data, sample_pending_rates):
    """Test click 'No grazie'"""
    user_id = str(mock_update.effective_user.id)
    save_user(user_id, sample_user_data)
    save_pending_rates(user_id, sample_pending_rates)

    await rate_update_no(mock_update, mock_context)

    # Verifica che query.answer sia stata chiamata
    mock_update.callback_query.answer.assert_called_once()

    # Verifica che il messaggio sia stato aggiornato
    mock_update.callback_query.edit_message_text.assert_called_once()
    call_args = mock_update.callback_query.edit_message_text.call_args
    assert DECLINED_TEXT in call_args.kwargs["text"]
    assert PROMPT_TEXT not in call_args.kwargs["text"]
    assert call_args.kwargs["parse_mode"] == ParseMode.HTML

    # Verifica che le pending_rates siano state rimosse
    assert load_pending_rates(user_id) is None

    # Verifica che le tariffe originali non siano cambiate
    user = load_user(user_id)
    assert user["luce"]["energia"] == 0.145


# ========== TEST CHECKER _build_pending_rates ==========


def test_build_pending_rates_luce_only():
    """Test costruzione pending_rates solo luce"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}},
        "gas": {},
    }

    pending = _build_pending_rates(user_rates, current_rates)

    assert pending["luce"]["tipo"] == "fissa"
    assert pending["luce"]["fascia"] == "monoraria"
    assert pending["luce"]["energia"] == 0.130
    assert pending["luce"]["commercializzazione"] == 65.0
    assert "gas" not in pending


def test_build_pending_rates_with_gas():
    """Test costruzione pending_rates con gas"""
    from checker import _build_pending_rates

    user_rates = {
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
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.420, "commercializzazione": 80.0}}},
    }

    pending = _build_pending_rates(user_rates, current_rates)

    assert pending["luce"]["energia"] == 0.130
    assert pending["gas"]["energia"] == 0.420
    assert pending["gas"]["commercializzazione"] == 80.0


def test_build_pending_rates_preserves_consumption():
    """Test che _build_pending_rates preserva i consumi"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
            "consumo_f2": 500.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
            "consumo_annuo": 1200.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.420, "commercializzazione": 80.0}}},
    }

    pending = _build_pending_rates(user_rates, current_rates)

    assert pending["luce"]["consumo_f1"] == 2700.0
    assert pending["luce"]["consumo_f2"] == 500.0
    assert pending["gas"]["consumo_annuo"] == 1200.0


def test_build_pending_rates_missing_current_rates():
    """Test _build_pending_rates quando le tariffe correnti non sono disponibili"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
    }

    current_rates = {"luce": {}, "gas": {}}

    pending = _build_pending_rates(user_rates, current_rates)

    # Dovrebbe mantenere le tariffe attuali
    assert pending["luce"]["energia"] == 0.145
    assert pending["luce"]["commercializzazione"] == 72.0


# ========== TEST CHECKER build_rate_update_keyboard ==========


def test_build_rate_update_keyboard():
    """Test costruzione tastiera inline"""
    from checker import build_rate_update_keyboard

    keyboard = build_rate_update_keyboard()

    assert keyboard is not None
    # Verifica che ci siano 2 pulsanti in una riga
    assert len(keyboard.inline_keyboard) == 1
    assert len(keyboard.inline_keyboard[0]) == 2
    assert keyboard.inline_keyboard[0][0].callback_data == "rate_update_yes"
    assert keyboard.inline_keyboard[0][1].callback_data == "rate_update_no"


# ========== TEST EDGE CASES HANDLER ==========


@pytest.mark.asyncio
async def test_rate_update_yes_user_not_in_db(mock_update, mock_context):
    """Test click 'Aggiorna tariffe' quando l'utente non esiste nel DB ma ha pending_rates"""
    # Simula pending_rates presenti ma utente non nel DB
    with patch("handlers.rate_update.load_pending_rates", return_value={"luce": {}}):
        with patch("handlers.rate_update.load_user", return_value=None):
            await rate_update_yes(mock_update, mock_context)

    # Verifica che il messaggio sia stato aggiornato con il testo di fallback
    mock_update.callback_query.edit_message_text.assert_called_once()
    call_args = mock_update.callback_query.edit_message_text.call_args
    assert DECLINED_TEXT in call_args.kwargs["text"]


@pytest.mark.asyncio
async def test_rate_update_yes_save_failure(
    mock_update, mock_context, sample_user_data, sample_pending_rates
):
    """Test click 'Aggiorna tariffe' quando save_user fallisce"""
    user_id = str(mock_update.effective_user.id)
    save_user(user_id, sample_user_data)
    save_pending_rates(user_id, sample_pending_rates)

    with patch("handlers.rate_update.save_user", return_value=False):
        await rate_update_yes(mock_update, mock_context)

    # Verifica che il messaggio contenga l'errore
    mock_update.callback_query.edit_message_text.assert_called_once()
    call_args = mock_update.callback_query.edit_message_text.call_args
    assert "Errore nell'aggiornamento" in call_args.kwargs["text"]


# ========== TEST DATABASE PENDING RATES ERROR HANDLING ==========


def test_save_pending_rates_db_error(sample_user_data):
    """Test save_pending_rates con errore database"""
    with patch("database.get_connection", side_effect=database.sqlite3.Error("test error")):
        result = save_pending_rates("123", {"luce": {}})
    assert result is False


def test_load_pending_rates_db_error():
    """Test load_pending_rates con errore database"""
    with patch("database.get_connection", side_effect=database.sqlite3.Error("test error")):
        result = load_pending_rates("123")
    assert result is None


def test_clear_pending_rates_db_error():
    """Test clear_pending_rates con errore database"""
    with patch("database.get_connection", side_effect=database.sqlite3.Error("test error")):
        result = clear_pending_rates("123")
    assert result is False


# ========== TEST MIXED TARIFF SCENARIOS ==========


def test_build_pending_rates_gas_better_luce_worse():
    """Test caso misto: gas migliore, luce peggiore → aggiorna solo gas"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.100,  # Utente ha tariffa migliore
            "commercializzazione": 60.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.500,  # Utente ha tariffa peggiore
            "commercializzazione": 90.0,
        },
    }

    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.130,  # PEGGIORE per l'utente
                    "commercializzazione": 72.0,  # PEGGIORE
                }
            }
        },
        "gas": {
            "fissa": {
                "monoraria": {
                    "energia": 0.420,  # MIGLIORE per l'utente
                    "commercializzazione": 80.0,  # MIGLIORE
                }
            }
        },
    }

    # Solo gas è conveniente
    pending = _build_pending_rates(user_rates, current_rates, show_luce=False, show_gas=True)

    # Gas deve essere aggiornato alle nuove tariffe
    assert pending["gas"]["energia"] == 0.420
    assert pending["gas"]["commercializzazione"] == 80.0

    # Luce deve mantenere le tariffe dell'utente (non aggiornare!)
    assert pending["luce"]["energia"] == 0.100
    assert pending["luce"]["commercializzazione"] == 60.0


def test_build_pending_rates_luce_better_gas_worse():
    """Test caso misto: luce migliore, gas peggiore → aggiorna solo luce"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.150,  # Utente ha tariffa peggiore
            "commercializzazione": 80.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.400,  # Utente ha tariffa migliore
            "commercializzazione": 75.0,
        },
    }

    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.120,  # MIGLIORE per l'utente
                    "commercializzazione": 65.0,  # MIGLIORE
                }
            }
        },
        "gas": {
            "fissa": {
                "monoraria": {
                    "energia": 0.450,  # PEGGIORE per l'utente
                    "commercializzazione": 85.0,  # PEGGIORE
                }
            }
        },
    }

    # Solo luce è conveniente
    pending = _build_pending_rates(user_rates, current_rates, show_luce=True, show_gas=False)

    # Luce deve essere aggiornata alle nuove tariffe
    assert pending["luce"]["energia"] == 0.120
    assert pending["luce"]["commercializzazione"] == 65.0

    # Gas deve mantenere le tariffe dell'utente (non aggiornare!)
    assert pending["gas"]["energia"] == 0.400
    assert pending["gas"]["commercializzazione"] == 75.0


def test_build_pending_rates_both_better():
    """Test caso: entrambe migliori → aggiorna entrambe"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.150,
            "commercializzazione": 80.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.500,
            "commercializzazione": 90.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.120, "commercializzazione": 65.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.420, "commercializzazione": 80.0}}},
    }

    # Entrambe convenienti
    pending = _build_pending_rates(user_rates, current_rates, show_luce=True, show_gas=True)

    # Entrambe devono essere aggiornate
    assert pending["luce"]["energia"] == 0.120
    assert pending["luce"]["commercializzazione"] == 65.0
    assert pending["gas"]["energia"] == 0.420
    assert pending["gas"]["commercializzazione"] == 80.0


def test_build_pending_rates_preserves_consumption_with_mixed():
    """Test che i consumi vengono preservati anche in caso misto"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.100,
            "commercializzazione": 60.0,
            "consumo_f1": 2700.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.500,
            "commercializzazione": 90.0,
            "consumo_annuo": 1200.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.420, "commercializzazione": 80.0}}},
    }

    # Solo gas conveniente
    pending = _build_pending_rates(user_rates, current_rates, show_luce=False, show_gas=True)

    # Consumi devono essere preservati
    assert pending["luce"]["consumo_f1"] == 2700.0
    assert pending["gas"]["consumo_annuo"] == 1200.0

    # Luce mantiene tariffe utente
    assert pending["luce"]["energia"] == 0.100

    # Gas aggiornato
    assert pending["gas"]["energia"] == 0.420


def test_build_pending_rates_backward_compatible():
    """Test che la funzione funziona senza parametri (backward compatibility)"""
    from checker import _build_pending_rates

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}},
        "gas": {},
    }

    # Chiamata senza parametri show_luce/show_gas (default: aggiorna tutto)
    pending = _build_pending_rates(user_rates, current_rates)

    # Deve aggiornare come prima (default behavior)
    assert pending["luce"]["energia"] == 0.130
    assert pending["luce"]["commercializzazione"] == 65.0
