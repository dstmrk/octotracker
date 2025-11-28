"""
Test per feedback.py - Sistema feedback utenti

Testa:
- Comando /feedback con rate limiting
- Conversazione rating e commento
- Salvataggio nel database
- Funzioni database feedback
"""

import os
import sys
import tempfile
from datetime import datetime, timedelta
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram import CallbackQuery, Message, Update, User
from telegram.ext import ConversationHandler

# Mock WEBHOOK_SECRET prima di importare bot
os.environ["WEBHOOK_SECRET"] = "test_secret_token_for_testing_only"

# Import funzioni del bot e database
sys.path.insert(0, str(Path(__file__).parent.parent))

import database
from database import (
    get_feedback_count,
    get_last_feedback_time,
    get_recent_feedbacks,
    init_db,
    remove_user,
    save_feedback,
    save_user,
)
from handlers.feedback import (
    COMMENT,
    MAX_COMMENT_LENGTH,
    RATING,
    feedback_cancel,
    feedback_command,
    feedback_comment,
    feedback_rating,
    feedback_skip_comment,
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
    query.message = MagicMock(spec=Message)
    query.data = ""

    return query


@pytest.fixture
def mock_context():
    """Crea mock context"""
    context = MagicMock()
    context.user_data = {}
    return context


# ========== TEST DATABASE FEEDBACK ==========


def test_migration_feedback_schema():
    """Test che la migration aggiunge correttamente la colonna last_feedback_at"""
    # Verifica che la colonna esista (è stata creata da init_db nel fixture)
    with database.get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = [row[1] for row in cursor.fetchall()]
        assert "last_feedback_at" in columns

        # Verifica che la tabella feedback esista
        cursor = conn.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name='feedback'"
        )
        assert cursor.fetchone() is not None


def test_migration_idempotent():
    """Test che la migration può essere eseguita più volte senza errori"""
    # Esegui migration una seconda volta (è già stata eseguita nel fixture)
    from database import _migrate_feedback_schema

    # Non deve sollevare eccezioni
    _migrate_feedback_schema()

    # Verifica che la colonna esista ancora
    with database.get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = [row[1] for row in cursor.fetchall()]
        assert "last_feedback_at" in columns


def test_save_feedback_success():
    """Test salvataggio feedback con successo"""
    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    result = save_feedback(
        user_id="123456789", feedback_type="command", rating=5, comment="Ottimo bot!"
    )

    assert result is True
    assert get_feedback_count() == 1


def test_save_feedback_no_comment():
    """Test salvataggio feedback senza commento"""
    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    result = save_feedback(user_id="123456789", feedback_type="command", rating=4, comment=None)

    assert result is True
    assert get_feedback_count() == 1


def test_get_last_feedback_time_none():
    """Test get_last_feedback_time quando utente non ha mai dato feedback"""
    # Crea utente senza feedback
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    last_time = get_last_feedback_time("123456789")
    assert last_time is None


def test_get_last_feedback_time_after_feedback():
    """Test get_last_feedback_time dopo aver dato feedback"""
    # Crea utente
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    # Salva feedback
    save_feedback("123456789", "command", 5, "Test")

    last_time = get_last_feedback_time("123456789")
    assert last_time is not None
    # Verifica che sia un timestamp valido
    datetime.fromisoformat(last_time)  # Solleva eccezione se non valido


def test_get_recent_feedbacks():
    """Test recupero feedback recenti"""
    # Crea utenti prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("111", user_data)
    save_user("222", user_data)
    save_user("333", user_data)

    # Salva 3 feedback
    save_feedback("111", "command", 5, "Ottimo")
    save_feedback("222", "command", 3, "Buono")
    save_feedback("333", "command", 1, "Pessimo")

    feedbacks = get_recent_feedbacks(limit=2)

    assert len(feedbacks) == 2
    # Verifica ordine decrescente per data
    assert feedbacks[0]["user_id"] == "333"  # Ultimo inserito
    assert feedbacks[0]["rating"] == 1
    assert feedbacks[1]["user_id"] == "222"


def test_get_recent_feedbacks_empty():
    """Test get_recent_feedbacks senza feedback"""
    feedbacks = get_recent_feedbacks()
    assert feedbacks == []


def test_get_feedback_count():
    """Test conteggio feedback"""
    assert get_feedback_count() == 0

    # Crea utenti prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("111", user_data)
    save_user("222", user_data)

    save_feedback("111", "command", 5)
    assert get_feedback_count() == 1

    save_feedback("222", "command", 4)
    assert get_feedback_count() == 2


def test_save_feedback_without_user():
    """Test salvataggio feedback per utente non esistente (FK constraint failure)"""
    # Ora che le FK sono abilitate, il salvataggio deve fallire
    result = save_feedback("999999", "command", 5, "Test utente inesistente")
    # Deve fallire con FK constraint error
    assert result is False


def test_get_last_feedback_time_user_not_exists():
    """Test get_last_feedback_time per utente inesistente"""
    result = get_last_feedback_time("999999")
    assert result is None


def test_save_feedback_database_error(monkeypatch):
    """Test gestione errore database in save_feedback"""
    import sqlite3

    def mock_get_connection_error():
        raise sqlite3.Error("Database locked")

    # Salva riferimento originale
    original_get_connection = database.get_connection

    # Mock per sollevare errore
    monkeypatch.setattr(database, "get_connection", mock_get_connection_error)

    result = save_feedback("123", "command", 5, "Test")

    # Deve ritornare False in caso di errore
    assert result is False

    # Ripristina
    monkeypatch.setattr(database, "get_connection", original_get_connection)


def test_get_last_feedback_time_database_error(monkeypatch):
    """Test gestione errore database in get_last_feedback_time"""
    import sqlite3

    def mock_get_connection_error():
        raise sqlite3.Error("Database locked")

    original_get_connection = database.get_connection
    monkeypatch.setattr(database, "get_connection", mock_get_connection_error)

    result = get_last_feedback_time("123")

    assert result is None

    monkeypatch.setattr(database, "get_connection", original_get_connection)


def test_get_recent_feedbacks_database_error(monkeypatch):
    """Test gestione errore database in get_recent_feedbacks"""
    import sqlite3

    def mock_get_connection_error():
        raise sqlite3.Error("Database locked")

    original_get_connection = database.get_connection
    monkeypatch.setattr(database, "get_connection", mock_get_connection_error)

    result = get_recent_feedbacks()

    assert result == []

    monkeypatch.setattr(database, "get_connection", original_get_connection)


def test_get_feedback_count_database_error(monkeypatch):
    """Test gestione errore database in get_feedback_count"""
    import sqlite3

    def mock_get_connection_error():
        raise sqlite3.Error("Database locked")

    original_get_connection = database.get_connection
    monkeypatch.setattr(database, "get_connection", mock_get_connection_error)

    result = get_feedback_count()

    assert result == 0

    monkeypatch.setattr(database, "get_connection", original_get_connection)


# ========== TEST CONVERSAZIONE FEEDBACK ==========


@pytest.mark.asyncio
async def test_feedback_command_success(mock_update, mock_context):
    """Test comando /feedback quando utente può dare feedback"""
    # Crea utente senza feedback precedente
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    result = await feedback_command(mock_update, mock_context)

    assert result == RATING
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "Come valuteresti OctoTracker?" in call_args[0][0]


@pytest.mark.asyncio
async def test_feedback_command_rate_limited(mock_update, mock_context):
    """Test comando /feedback con rate limiting (già dato feedback <24h fa)"""
    # Crea utente e salva feedback
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)
    save_feedback("123456789", "command", 5, "Test")

    result = await feedback_command(mock_update, mock_context)

    assert result == ConversationHandler.END
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "già inviato un feedback" in call_args[0][0]


@pytest.mark.asyncio
async def test_feedback_rating(mock_update, mock_callback_query, mock_context):
    """Test selezione rating"""
    mock_update.callback_query = mock_callback_query
    mock_callback_query.data = "rating_4"

    result = await feedback_rating(mock_update, mock_context)

    assert result == COMMENT
    assert mock_context.user_data["rating"] == 4
    mock_callback_query.answer.assert_called_once()
    mock_callback_query.edit_message_text.assert_called_once()
    call_args = mock_callback_query.edit_message_text.call_args
    assert "⭐⭐⭐⭐" in call_args[0][0]


@pytest.mark.asyncio
async def test_feedback_comment(mock_update, mock_context):
    """Test invio commento"""
    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    # Setup context con rating
    mock_context.user_data["rating"] = 5
    mock_update.message.text = "Ottimo bot, molto utile!"

    result = await feedback_comment(mock_update, mock_context)

    assert result == ConversationHandler.END
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "Feedback ricevuto" in call_args[0][0]

    # Verifica salvato nel database
    assert get_feedback_count() == 1
    feedbacks = get_recent_feedbacks(limit=1)
    assert feedbacks[0]["rating"] == 5
    assert feedbacks[0]["comment"] == "Ottimo bot, molto utile!"


@pytest.mark.asyncio
async def test_feedback_skip_comment(mock_update, mock_callback_query, mock_context):
    """Test skip commento"""
    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    mock_update.callback_query = mock_callback_query
    mock_context.user_data["rating"] = 3

    result = await feedback_skip_comment(mock_update, mock_context)

    assert result == ConversationHandler.END
    mock_callback_query.answer.assert_called_once()
    mock_callback_query.edit_message_text.assert_called_once()

    # Verifica salvato senza commento
    assert get_feedback_count() == 1
    feedbacks = get_recent_feedbacks(limit=1)
    assert feedbacks[0]["rating"] == 3
    assert feedbacks[0]["comment"] is None


@pytest.mark.asyncio
async def test_feedback_cancel(mock_update, mock_context):
    """Test annullamento conversazione feedback"""
    result = await feedback_cancel(mock_update, mock_context)

    assert result == ConversationHandler.END
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "annullato" in call_args[0][0].lower()


@pytest.mark.asyncio
async def test_feedback_cancel_with_callback_query(mock_update, mock_callback_query, mock_context):
    """Test annullamento da CallbackQuery"""
    mock_update.message = None
    mock_update.callback_query = mock_callback_query

    result = await feedback_cancel(mock_update, mock_context)

    assert result == ConversationHandler.END
    mock_callback_query.edit_message_text.assert_called_once()


# ========== TEST RATE LIMITING ==========


@pytest.mark.asyncio
async def test_feedback_rate_limiting_integration(mock_update, mock_context, monkeypatch):
    """Test completo rate limiting 24h"""
    user_id = "123456789"

    # Crea utente
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user(user_id, user_data)

    # Primo feedback: deve funzionare
    result1 = await feedback_command(mock_update, mock_context)
    assert result1 == RATING

    # Salva feedback
    save_feedback(user_id, "command", 5)

    # Secondo feedback immediato: deve essere bloccato
    result2 = await feedback_command(mock_update, mock_context)
    assert result2 == ConversationHandler.END

    # Simula passaggio di 24 ore modificando il timestamp
    with database.get_connection() as conn:
        old_time = (datetime.now() - timedelta(hours=25)).isoformat()
        conn.execute("UPDATE users SET last_feedback_at = ? WHERE user_id = ?", (old_time, user_id))

    # Terzo feedback dopo 24h: deve funzionare
    result3 = await feedback_command(mock_update, mock_context)
    assert result3 == RATING


@pytest.mark.asyncio
async def test_feedback_command_invalid_timestamp(mock_update, mock_context, monkeypatch):
    """Test gestione timestamp invalido (graceful fallback)"""
    user_id = "123456789"

    # Crea utente
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user(user_id, user_data)

    # Inserisci timestamp malformato
    with database.get_connection() as conn:
        conn.execute(
            "UPDATE users SET last_feedback_at = ? WHERE user_id = ?", ("invalid", user_id)
        )

    # Deve permettere di procedere (safe default)
    result = await feedback_command(mock_update, mock_context)
    assert result == RATING


@pytest.mark.asyncio
async def test_feedback_comment_database_error(mock_update, mock_context, monkeypatch):
    """Test gestione errore database durante salvataggio commento"""
    mock_context.user_data["rating"] = 5
    mock_update.message.text = "Test commento"

    # Mock save_feedback per ritornare False (errore)
    def mock_save_feedback_error(*args, **kwargs):
        return False

    import handlers.feedback as feedback

    original_save = feedback.save_feedback
    monkeypatch.setattr(feedback, "save_feedback", mock_save_feedback_error)

    result = await feedback_comment(mock_update, mock_context)

    assert result == ConversationHandler.END
    # Verifica che viene mostrato messaggio di errore
    call_args = mock_update.message.reply_text.call_args
    assert "errore" in call_args[0][0].lower()

    monkeypatch.setattr(feedback, "save_feedback", original_save)


@pytest.mark.asyncio
async def test_feedback_skip_comment_database_error(
    mock_update, mock_callback_query, mock_context, monkeypatch
):
    """Test gestione errore database durante skip commento"""
    mock_update.callback_query = mock_callback_query
    mock_context.user_data["rating"] = 3

    # Mock save_feedback per ritornare False (errore)
    def mock_save_feedback_error(*args, **kwargs):
        return False

    import handlers.feedback as feedback

    original_save = feedback.save_feedback
    monkeypatch.setattr(feedback, "save_feedback", mock_save_feedback_error)

    result = await feedback_skip_comment(mock_update, mock_context)

    assert result == ConversationHandler.END
    # Verifica che viene mostrato messaggio di errore
    call_args = mock_callback_query.edit_message_text.call_args
    assert "errore" in call_args[0][0].lower()

    monkeypatch.setattr(feedback, "save_feedback", original_save)


# ========== TEST VALIDAZIONE LUNGHEZZA COMMENTO ==========


@pytest.mark.asyncio
async def test_feedback_comment_too_long(mock_update, mock_context):
    """Test validazione commento troppo lungo (>1000 caratteri)"""
    # Setup context con rating
    mock_context.user_data["rating"] = 5

    # Crea commento più lungo di MAX_COMMENT_LENGTH
    long_comment = "A" * (MAX_COMMENT_LENGTH + 1)
    mock_update.message.text = long_comment

    result = await feedback_comment(mock_update, mock_context)

    # Deve rimanere in stato COMMENT per permettere un nuovo tentativo
    assert result == COMMENT
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "troppo lungo" in call_args[0][0]
    assert str(MAX_COMMENT_LENGTH + 1) in call_args[0][0]  # Mostra lunghezza attuale
    assert str(MAX_COMMENT_LENGTH) in call_args[0][0]  # Mostra limite

    # Verifica che NON sia stato salvato nel database
    assert get_feedback_count() == 0


@pytest.mark.asyncio
async def test_feedback_comment_exact_max_length(mock_update, mock_context):
    """Test commento esattamente di 1000 caratteri (limite massimo)"""
    # Setup context con rating
    mock_context.user_data["rating"] = 4

    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    # Crea commento esattamente di MAX_COMMENT_LENGTH caratteri
    exact_length_comment = "B" * MAX_COMMENT_LENGTH
    mock_update.message.text = exact_length_comment

    result = await feedback_comment(mock_update, mock_context)

    # Deve completare con successo
    assert result == ConversationHandler.END
    mock_update.message.reply_text.assert_called_once()
    call_args = mock_update.message.reply_text.call_args
    assert "Feedback ricevuto" in call_args[0][0]

    # Verifica salvato nel database
    assert get_feedback_count() == 1
    feedbacks = get_recent_feedbacks(limit=1)
    assert feedbacks[0]["rating"] == 4
    assert feedbacks[0]["comment"] == exact_length_comment
    assert len(feedbacks[0]["comment"]) == MAX_COMMENT_LENGTH


@pytest.mark.asyncio
async def test_feedback_comment_retry_after_too_long(mock_update, mock_context):
    """Test invio commento corretto dopo tentativo con commento troppo lungo"""
    # Setup context con rating
    mock_context.user_data["rating"] = 5

    # Crea utente prima di salvare feedback (required con FK enabled)
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user("123456789", user_data)

    # Primo tentativo: commento troppo lungo
    long_comment = "X" * (MAX_COMMENT_LENGTH + 500)
    mock_update.message.text = long_comment

    result1 = await feedback_comment(mock_update, mock_context)
    assert result1 == COMMENT
    assert get_feedback_count() == 0

    # Reset mock per secondo tentativo
    mock_update.message.reply_text.reset_mock()

    # Secondo tentativo: commento valido
    valid_comment = "Ottimo bot!"
    mock_update.message.text = valid_comment

    result2 = await feedback_comment(mock_update, mock_context)
    assert result2 == ConversationHandler.END

    # Verifica salvato nel database
    assert get_feedback_count() == 1
    feedbacks = get_recent_feedbacks(limit=1)
    assert feedbacks[0]["comment"] == valid_comment


# ========== TEST ON DELETE CASCADE ==========


def test_cascade_delete_removes_feedback():
    """Test che rimuovendo un utente vengono cancellati anche i suoi feedback"""
    user_id = "123456789"

    # Crea utente
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user(user_id, user_data)

    # Salva 3 feedback per questo utente
    save_feedback(user_id, "command", 5, "Primo feedback")
    save_feedback(user_id, "command", 4, "Secondo feedback")
    save_feedback(user_id, "command", 3, "Terzo feedback")

    # Verifica che ci siano 3 feedback
    assert get_feedback_count() == 3

    # Rimuovi utente
    result = remove_user(user_id)
    assert result is True

    # Verifica che i feedback siano stati cancellati automaticamente
    assert get_feedback_count() == 0


def test_cascade_delete_preserves_other_users_feedback():
    """Test che la cancellazione a cascata non tocca i feedback di altri utenti"""
    user1_id = "111111111"
    user2_id = "222222222"

    # Crea due utenti
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    save_user(user1_id, user_data)
    save_user(user2_id, user_data)

    # Salva feedback per entrambi
    save_feedback(user1_id, "command", 5, "User 1 feedback 1")
    save_feedback(user1_id, "command", 4, "User 1 feedback 2")
    save_feedback(user2_id, "command", 3, "User 2 feedback 1")

    assert get_feedback_count() == 3

    # Rimuovi solo user1
    remove_user(user1_id)

    # Verifica che rimanga solo 1 feedback (di user2)
    assert get_feedback_count() == 1

    # Verifica che il feedback rimanente sia di user2
    feedbacks = get_recent_feedbacks(limit=10)
    assert len(feedbacks) == 1
    assert feedbacks[0]["user_id"] == user2_id
    assert feedbacks[0]["comment"] == "User 2 feedback 1"


def test_migration_cascade_adds_on_delete():
    """Test che la migration aggiunge ON DELETE CASCADE"""
    from database import _has_cascade_delete

    # Verifica che la tabella feedback abbia ON DELETE CASCADE
    with database.get_connection() as conn:
        has_cascade = _has_cascade_delete(conn, "feedback")
        assert has_cascade is True


def test_migration_cascade_idempotent():
    """Test che la migration cascade è idempotente (può essere eseguita più volte)"""
    from database import _migrate_feedback_cascade

    # Esegui migration una seconda volta (è già stata eseguita nel fixture)
    _migrate_feedback_cascade()  # Non deve sollevare eccezioni

    # Verifica che la tabella esista ancora e abbia CASCADE
    from database import _has_cascade_delete

    with database.get_connection() as conn:
        has_cascade = _has_cascade_delete(conn, "feedback")
        assert has_cascade is True


def test_foreign_keys_enabled():
    """Test che le foreign keys siano abilitate"""
    with database.get_connection() as conn:
        cursor = conn.execute("PRAGMA foreign_keys")
        result = cursor.fetchone()
        # Il valore dovrebbe essere 1 (enabled)
        assert result[0] == 1


def test_migration_cascade_with_orphaned_feedback(monkeypatch, temp_database):
    """Test che la migration rimuove i feedback orfani durante l'upgrade"""
    from database import _migrate_feedback_cascade

    # Crea un database temporaneo separato
    with tempfile.TemporaryDirectory() as tmpdir:
        temp_db = Path(tmpdir) / "orphan_test.db"
        monkeypatch.setattr(database, "DB_FILE", temp_db)

        # Inizializza database con vecchia struttura (senza CASCADE)
        with database.get_connection() as conn:
            # Crea tabella users
            conn.execute(
                """
                CREATE TABLE users (
                    user_id TEXT PRIMARY KEY,
                    luce_tipo TEXT NOT NULL,
                    luce_fascia TEXT NOT NULL,
                    luce_energia REAL NOT NULL,
                    luce_commercializzazione REAL NOT NULL,
                    gas_tipo TEXT,
                    gas_fascia TEXT,
                    gas_energia REAL,
                    gas_commercializzazione REAL,
                    luce_consumo_f1 REAL,
                    luce_consumo_f2 REAL,
                    luce_consumo_f3 REAL,
                    gas_consumo_annuo REAL,
                    last_notified_rates TEXT,
                    last_feedback_at TIMESTAMP,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """
            )

            # Crea vecchia tabella feedback SENZA ON DELETE CASCADE
            conn.execute(
                """
                CREATE TABLE feedback (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id TEXT NOT NULL,
                    feedback_type TEXT NOT NULL,
                    rating INTEGER,
                    comment TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """
            )

            # Inserisci un utente
            conn.execute(
                """
                INSERT INTO users (
                    user_id, luce_tipo, luce_fascia, luce_energia, luce_commercializzazione
                ) VALUES ('111', 'fissa', 'monoraria', 0.145, 72.0)
            """
            )

            # Inserisci 2 feedback: 1 valido, 1 orfano
            conn.execute(
                """
                INSERT INTO feedback (user_id, feedback_type, rating, comment)
                VALUES ('111', 'command', 5, 'Feedback valido')
            """
            )
            conn.execute(
                """
                INSERT INTO feedback (user_id, feedback_type, rating, comment)
                VALUES ('999', 'command', 3, 'Feedback orfano')
            """
            )

        # Verifica che ci siano 2 feedback prima della migration
        with database.get_connection() as conn:
            cursor = conn.execute("SELECT COUNT(*) as count FROM feedback")
            assert cursor.fetchone()["count"] == 2

        # Esegui migration
        _migrate_feedback_cascade()

        # Verifica che rimanga solo 1 feedback (quello valido)
        with database.get_connection() as conn:
            cursor = conn.execute("SELECT COUNT(*) as count FROM feedback")
            assert cursor.fetchone()["count"] == 1

            # Verifica che sia quello dell'utente 111
            cursor = conn.execute("SELECT user_id FROM feedback")
            assert cursor.fetchone()["user_id"] == "111"

            # Verifica che la tabella abbia CASCADE
            from database import _has_cascade_delete

            assert _has_cascade_delete(conn, "feedback") is True
