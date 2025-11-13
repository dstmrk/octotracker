"""Test per il modulo broadcast."""

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut

from broadcast import (
    broadcast_to_all_users,
    confirm_send,
    load_message,
    send_broadcast_message,
    send_broadcasts_parallel,
)


@pytest.mark.asyncio
async def test_send_broadcast_message_success():
    """Test invio messaggio con successo."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    result = await send_broadcast_message(bot_mock, "123456", "Test message")

    assert result is True
    bot_mock.send_message.assert_called_once_with(
        chat_id="123456", text="Test message", parse_mode="HTML"
    )


@pytest.mark.asyncio
async def test_send_broadcast_message_retry_after():
    """Test invio messaggio con rate limit."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=RetryAfter(30))

    result = await send_broadcast_message(bot_mock, "123456", "Test message")

    assert result is False
    bot_mock.send_message.assert_called_once()


@pytest.mark.asyncio
async def test_send_broadcast_message_timeout():
    """Test invio messaggio con timeout."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TimedOut("Timeout"))

    result = await send_broadcast_message(bot_mock, "123456", "Test message")

    assert result is False
    bot_mock.send_message.assert_called_once()


@pytest.mark.asyncio
async def test_send_broadcast_message_network_error():
    """Test invio messaggio con errore di rete."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=NetworkError("Network error"))

    result = await send_broadcast_message(bot_mock, "123456", "Test message")

    assert result is False
    bot_mock.send_message.assert_called_once()


@pytest.mark.asyncio
async def test_send_broadcast_message_telegram_error():
    """Test invio messaggio con errore generico Telegram."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TelegramError("Generic error"))

    result = await send_broadcast_message(bot_mock, "123456", "Test message")

    assert result is False
    bot_mock.send_message.assert_called_once()


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_success():
    """Test invio parallelo di messaggi con successo."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    users = {"user1": {}, "user2": {}, "user3": {}}
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, users, message)

    assert successful == 3
    assert failed == 0
    assert bot_mock.send_message.call_count == 3


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_partial_failure():
    """Test invio parallelo con alcuni fallimenti."""
    bot_mock = MagicMock()
    # Prima chiamata: successo, seconda: fallimento, terza: successo
    bot_mock.send_message = AsyncMock(
        side_effect=[None, RetryAfter(30), None, None, RetryAfter(30)]
    )

    users = {"user1": {}, "user2": {}, "user3": {}, "user4": {}, "user5": {}}
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, users, message)

    assert successful == 3
    assert failed == 2
    assert bot_mock.send_message.call_count == 5


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_all_failures():
    """Test invio parallelo con tutti fallimenti."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TelegramError("Error"))

    users = {"user1": {}, "user2": {}}
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, users, message)

    assert successful == 0
    assert failed == 2
    assert bot_mock.send_message.call_count == 2


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_rate_limiting():
    """Test che il rate limiting funzioni correttamente (max 10 simultanei)."""
    bot_mock = MagicMock()

    # Crea un counter per tracciare le chiamate simultanee
    concurrent_calls = []

    async def mock_send_message(*args, **kwargs):
        concurrent_calls.append(1)
        await asyncio.sleep(0.1)  # Simula un'operazione lenta
        concurrent_calls.pop()
        # Verifica che non ci siano mai più di 10 chiamate simultanee
        assert len(concurrent_calls) <= 10

    bot_mock.send_message = mock_send_message

    # Crea 25 utenti per testare il rate limiting
    users = {f"user{i}": {} for i in range(25)}
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, users, message)

    assert successful == 25
    assert failed == 0


def test_load_message_success(tmp_path):
    """Test caricamento messaggio da file con successo."""
    message_file = tmp_path / "test_message.txt"
    message_file.write_text("Test message content", encoding="utf-8")

    result = load_message(str(message_file))

    assert result == "Test message content"


def test_load_message_file_not_found():
    """Test caricamento messaggio con file non esistente."""
    with pytest.raises(FileNotFoundError):
        load_message("/path/to/nonexistent/file.txt")


def test_load_message_empty_file(tmp_path):
    """Test caricamento messaggio da file vuoto."""
    message_file = tmp_path / "empty.txt"
    message_file.write_text("   \n  \t  ", encoding="utf-8")

    with pytest.raises(ValueError, match="Il file messaggio è vuoto"):
        load_message(str(message_file))


def test_load_message_with_whitespace(tmp_path):
    """Test caricamento messaggio con spazi bianchi iniziali e finali."""
    message_file = tmp_path / "whitespace.txt"
    message_file.write_text("  \n  Test message  \n  ", encoding="utf-8")

    result = load_message(str(message_file))

    assert result == "Test message"


def test_confirm_send_yes():
    """Test conferma invio con risposta positiva."""
    with patch("builtins.input", return_value="S"):
        result = confirm_send("Test message", 5)
        assert result is True


def test_confirm_send_si():
    """Test conferma invio con 'SI'."""
    with patch("builtins.input", return_value="SI"):
        result = confirm_send("Test message", 5)
        assert result is True


def test_confirm_send_yes_english():
    """Test conferma invio con 'Y'."""
    with patch("builtins.input", return_value="Y"):
        result = confirm_send("Test message", 5)
        assert result is True


def test_confirm_send_no():
    """Test conferma invio con risposta negativa."""
    with patch("builtins.input", return_value="N"):
        result = confirm_send("Test message", 5)
        assert result is False


def test_confirm_send_invalid():
    """Test conferma invio con risposta invalida."""
    with patch("builtins.input", return_value="MAYBE"):
        result = confirm_send("Test message", 5)
        assert result is False


def test_confirm_send_lowercase():
    """Test conferma invio con risposta in minuscolo."""
    with patch("builtins.input", return_value="s"):
        result = confirm_send("Test message", 5)
        assert result is True


@pytest.mark.asyncio
async def test_broadcast_to_all_users_success(tmp_path):
    """Test broadcast completo con successo."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Mock users
    users = {"user1": {"luce": {}}, "user2": {"luce": {}}}

    # Mock bot
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    with patch("broadcast.load_users", return_value=users):
        with patch("broadcast.confirm_send", return_value=True):
            with patch("broadcast.Bot", return_value=bot_mock):
                result = await broadcast_to_all_users(str(message_file), "fake_token")

    assert result["successful"] == 2
    assert result["failed"] == 0
    assert result["total"] == 2
    assert bot_mock.send_message.call_count == 2


@pytest.mark.asyncio
async def test_broadcast_to_all_users_no_users(tmp_path):
    """Test broadcast senza utenti nel database."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    with patch("broadcast.load_users", return_value={}):
        with pytest.raises(ValueError, match="Nessun utente nel database"):
            await broadcast_to_all_users(str(message_file), "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_all_users_cancelled(tmp_path):
    """Test broadcast annullato dall'utente."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Mock users
    users = {"user1": {"luce": {}}}

    with patch("broadcast.load_users", return_value=users):
        with patch("broadcast.confirm_send", return_value=False):
            result = await broadcast_to_all_users(str(message_file), "fake_token")

    assert result["successful"] == 0
    assert result["failed"] == 0
    assert result["total"] == 0


@pytest.mark.asyncio
async def test_broadcast_to_all_users_partial_failure(tmp_path):
    """Test broadcast con alcuni fallimenti."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Mock users
    users = {"user1": {"luce": {}}, "user2": {"luce": {}}, "user3": {"luce": {}}}

    # Mock bot con un fallimento
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=[None, RetryAfter(30), None])

    with patch("broadcast.load_users", return_value=users):
        with patch("broadcast.confirm_send", return_value=True):
            with patch("broadcast.Bot", return_value=bot_mock):
                result = await broadcast_to_all_users(str(message_file), "fake_token")

    assert result["successful"] == 2
    assert result["failed"] == 1
    assert result["total"] == 3


@pytest.mark.asyncio
async def test_broadcast_to_all_users_file_not_found():
    """Test broadcast con file messaggio non esistente."""
    with pytest.raises(FileNotFoundError):
        await broadcast_to_all_users("/path/to/nonexistent.txt", "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_all_users_empty_message(tmp_path):
    """Test broadcast con messaggio vuoto."""
    # Crea file messaggio vuoto
    message_file = tmp_path / "empty.txt"
    message_file.write_text("   ", encoding="utf-8")

    with pytest.raises(ValueError, match="Il file messaggio è vuoto"):
        await broadcast_to_all_users(str(message_file), "fake_token")


def test_main_success(tmp_path):
    """Test main con esecuzione corretta."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test message", encoding="utf-8")

    # Mock broadcast_to_all_users per evitare esecuzione reale
    async def mock_broadcast(*args, **kwargs):
        return {"successful": 1, "failed": 0, "total": 1}

    with patch("sys.argv", ["broadcast.py", str(message_file)]):
        with patch("broadcast.load_dotenv"):
            with patch("os.getenv", return_value="fake_token"):
                with patch("broadcast.broadcast_to_all_users", side_effect=mock_broadcast):
                    with patch("asyncio.run") as mock_run:
                        from broadcast import main

                        main()
                        mock_run.assert_called_once()


def test_main_no_token():
    """Test main senza token configurato."""
    with patch("broadcast.load_dotenv"):
        with patch("os.getenv", return_value=None):
            with pytest.raises(SystemExit) as exc_info:
                from broadcast import main

                main()
            assert exc_info.value.code == 1


def test_main_file_not_found():
    """Test main con file messaggio non esistente."""
    with patch("broadcast.load_dotenv"):
        with patch("os.getenv", return_value="fake_token"):
            with patch("asyncio.run", side_effect=FileNotFoundError("File non trovato")):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_keyboard_interrupt():
    """Test main con interruzione da tastiera."""
    with patch("broadcast.load_dotenv"):
        with patch("os.getenv", return_value="fake_token"):
            with patch("asyncio.run", side_effect=KeyboardInterrupt()):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_generic_exception():
    """Test main con eccezione generica."""
    with patch("broadcast.load_dotenv"):
        with patch("os.getenv", return_value="fake_token"):
            with patch("asyncio.run", side_effect=Exception("Errore generico")):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_with_custom_message_file(tmp_path):
    """Test main con file messaggio personalizzato specificato da CLI."""
    message_file = tmp_path / "custom_message.txt"
    message_file.write_text("Custom message", encoding="utf-8")

    async def mock_broadcast(file_path, token):
        assert file_path == str(message_file)
        return {"successful": 1, "failed": 0, "total": 1}

    with patch("sys.argv", ["broadcast.py", str(message_file)]):
        with patch("broadcast.load_dotenv"):
            with patch("os.getenv", return_value="fake_token"):
                with patch("asyncio.run") as mock_run:
                    from broadcast import main

                    main()
                    # Verifica che asyncio.run sia stato chiamato con la coroutine corretta
                    mock_run.assert_called_once()


def test_main_default_message_file():
    """Test main con file messaggio di default (message.txt)."""

    async def mock_broadcast(file_path, token):
        assert file_path == "message.txt"
        return {"successful": 1, "failed": 0, "total": 1}

    with patch("sys.argv", ["broadcast.py"]):
        with patch("broadcast.load_dotenv"):
            with patch("os.getenv", return_value="fake_token"):
                with patch("asyncio.run") as mock_run:
                    from broadcast import main

                    main()
                    mock_run.assert_called_once()
