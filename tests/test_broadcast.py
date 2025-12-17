"""Test per il modulo broadcast."""

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut

from broadcast import (
    broadcast_to_users,
    confirm_send,
    load_message,
    load_users_from_file,
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

    user_ids = ["user1", "user2", "user3"]
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, user_ids, message)

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

    user_ids = ["user1", "user2", "user3", "user4", "user5"]
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, user_ids, message)

    assert successful == 3
    assert failed == 2
    assert bot_mock.send_message.call_count == 5


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_all_failures():
    """Test invio parallelo con tutti fallimenti."""
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TelegramError("Error"))

    user_ids = ["user1", "user2"]
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, user_ids, message)

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
    user_ids = [f"user{i}" for i in range(25)]
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, user_ids, message)

    assert successful == 25
    assert failed == 0


@pytest.mark.asyncio
async def test_send_broadcasts_parallel_custom_batch_size():
    """Test invio parallelo con batch size personalizzato."""
    bot_mock = MagicMock()

    # Crea un counter per tracciare le chiamate simultanee
    concurrent_calls = []
    max_concurrent = 0

    async def mock_send_message(*args, **kwargs):
        nonlocal max_concurrent
        concurrent_calls.append(1)
        max_concurrent = max(max_concurrent, len(concurrent_calls))
        await asyncio.sleep(0.05)
        concurrent_calls.pop()

    bot_mock.send_message = mock_send_message

    user_ids = [f"user{i}" for i in range(15)]
    message = "Test broadcast"

    successful, failed = await send_broadcasts_parallel(bot_mock, user_ids, message, batch_size=5)

    assert successful == 15
    assert failed == 0
    assert max_concurrent <= 5


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


def test_load_users_from_file_success(tmp_path):
    """Test caricamento utenti da file con successo."""
    users_file = tmp_path / "users.txt"
    users_file.write_text("123456\n789012\n345678", encoding="utf-8")

    result = load_users_from_file(str(users_file))

    assert result == ["123456", "789012", "345678"]


def test_load_users_from_file_with_comments(tmp_path):
    """Test caricamento utenti ignorando commenti."""
    users_file = tmp_path / "users.txt"
    users_file.write_text("# Utenti test\n123456\n# Altro commento\n789012", encoding="utf-8")

    result = load_users_from_file(str(users_file))

    assert result == ["123456", "789012"]


def test_load_users_from_file_with_empty_lines(tmp_path):
    """Test caricamento utenti ignorando righe vuote."""
    users_file = tmp_path / "users.txt"
    users_file.write_text("123456\n\n  \n789012\n", encoding="utf-8")

    result = load_users_from_file(str(users_file))

    assert result == ["123456", "789012"]


def test_load_users_from_file_not_found():
    """Test caricamento utenti con file non esistente."""
    with pytest.raises(FileNotFoundError, match="File utenti non trovato"):
        load_users_from_file("/path/to/nonexistent/users.txt")


def test_load_users_from_file_empty(tmp_path):
    """Test caricamento utenti da file vuoto."""
    users_file = tmp_path / "empty.txt"
    users_file.write_text("   \n# solo commenti\n  ", encoding="utf-8")

    with pytest.raises(ValueError, match="non contiene user_id validi"):
        load_users_from_file(str(users_file))


def test_confirm_send_yes():
    """Test conferma invio con risposta positiva."""
    with patch("builtins.input", return_value="S"):
        result = confirm_send("Test message", 5, 10)
        assert result is True


def test_confirm_send_si():
    """Test conferma invio con 'SI'."""
    with patch("builtins.input", return_value="SI"):
        result = confirm_send("Test message", 5, 10)
        assert result is True


def test_confirm_send_yes_english():
    """Test conferma invio con 'Y'."""
    with patch("builtins.input", return_value="Y"):
        result = confirm_send("Test message", 5, 10)
        assert result is True


def test_confirm_send_no():
    """Test conferma invio con risposta negativa."""
    with patch("builtins.input", return_value="N"):
        result = confirm_send("Test message", 5, 10)
        assert result is False


def test_confirm_send_invalid():
    """Test conferma invio con risposta invalida."""
    with patch("builtins.input", return_value="MAYBE"):
        result = confirm_send("Test message", 5, 10)
        assert result is False


def test_confirm_send_lowercase():
    """Test conferma invio con risposta in minuscolo."""
    with patch("builtins.input", return_value="s"):
        result = confirm_send("Test message", 5, 10)
        assert result is True


@pytest.mark.asyncio
async def test_broadcast_to_users_success(tmp_path):
    """Test broadcast completo con successo."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Crea file utenti
    users_file = tmp_path / "users.txt"
    users_file.write_text("user1\nuser2", encoding="utf-8")

    # Mock bot
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    with patch("broadcast.confirm_send", return_value=True):
        with patch("broadcast.Bot", return_value=bot_mock):
            result = await broadcast_to_users(str(message_file), str(users_file), "fake_token")

    assert result["successful"] == 2
    assert result["failed"] == 0
    assert result["total"] == 2
    assert bot_mock.send_message.call_count == 2


@pytest.mark.asyncio
async def test_broadcast_to_users_no_users(tmp_path):
    """Test broadcast con file utenti vuoto."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Crea file utenti vuoto
    users_file = tmp_path / "users.txt"
    users_file.write_text("# solo commenti", encoding="utf-8")

    with pytest.raises(ValueError, match="non contiene user_id validi"):
        await broadcast_to_users(str(message_file), str(users_file), "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_users_cancelled(tmp_path):
    """Test broadcast annullato dall'utente."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Crea file utenti
    users_file = tmp_path / "users.txt"
    users_file.write_text("user1", encoding="utf-8")

    with patch("broadcast.confirm_send", return_value=False):
        result = await broadcast_to_users(str(message_file), str(users_file), "fake_token")

    assert result["successful"] == 0
    assert result["failed"] == 0
    assert result["total"] == 0


@pytest.mark.asyncio
async def test_broadcast_to_users_partial_failure(tmp_path):
    """Test broadcast con alcuni fallimenti."""
    # Crea file messaggio
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test broadcast message", encoding="utf-8")

    # Crea file utenti
    users_file = tmp_path / "users.txt"
    users_file.write_text("user1\nuser2\nuser3", encoding="utf-8")

    # Mock bot con un fallimento
    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=[None, RetryAfter(30), None])

    with patch("broadcast.confirm_send", return_value=True):
        with patch("broadcast.Bot", return_value=bot_mock):
            result = await broadcast_to_users(str(message_file), str(users_file), "fake_token")

    assert result["successful"] == 2
    assert result["failed"] == 1
    assert result["total"] == 3


@pytest.mark.asyncio
async def test_broadcast_to_users_file_not_found():
    """Test broadcast con file messaggio non esistente."""
    with pytest.raises(FileNotFoundError):
        await broadcast_to_users("/path/to/nonexistent.txt", "users.txt", "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_users_users_file_not_found(tmp_path):
    """Test broadcast con file utenti non esistente."""
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test message", encoding="utf-8")

    with pytest.raises(FileNotFoundError, match="File utenti non trovato"):
        await broadcast_to_users(str(message_file), "/path/to/nonexistent.txt", "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_users_empty_message(tmp_path):
    """Test broadcast con messaggio vuoto."""
    # Crea file messaggio vuoto
    message_file = tmp_path / "empty.txt"
    message_file.write_text("   ", encoding="utf-8")

    with pytest.raises(ValueError, match="Il file messaggio è vuoto"):
        await broadcast_to_users(str(message_file), "users.txt", "fake_token")


@pytest.mark.asyncio
async def test_broadcast_to_users_custom_batch_size(tmp_path):
    """Test broadcast con batch size personalizzato."""
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test message", encoding="utf-8")

    users_file = tmp_path / "users.txt"
    users_file.write_text("user1\nuser2", encoding="utf-8")

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    with patch("broadcast.confirm_send", return_value=True):
        with patch("broadcast.Bot", return_value=bot_mock):
            result = await broadcast_to_users(
                str(message_file), str(users_file), "fake_token", batch_size=5
            )

    assert result["successful"] == 2
    assert result["total"] == 2


def test_main_success(tmp_path):
    """Test main con esecuzione corretta."""
    message_file = tmp_path / "message.txt"
    message_file.write_text("Test message", encoding="utf-8")

    users_file = tmp_path / "users.txt"
    users_file.write_text("user1", encoding="utf-8")

    with patch("sys.argv", ["broadcast.py", str(message_file), str(users_file)]):
        with patch("broadcast.load_dotenv"):
            with patch(
                "os.getenv",
                side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
            ):
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
        with patch(
            "os.getenv",
            side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
        ):
            with patch("asyncio.run", side_effect=FileNotFoundError("File non trovato")):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_keyboard_interrupt():
    """Test main con interruzione da tastiera."""
    with patch("broadcast.load_dotenv"):
        with patch(
            "os.getenv",
            side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
        ):
            with patch("asyncio.run", side_effect=KeyboardInterrupt()):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_generic_exception():
    """Test main con eccezione generica."""
    with patch("broadcast.load_dotenv"):
        with patch(
            "os.getenv",
            side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
        ):
            with patch("asyncio.run", side_effect=Exception("Errore generico")):
                with pytest.raises(SystemExit) as exc_info:
                    from broadcast import main

                    main()
                assert exc_info.value.code == 1


def test_main_with_custom_files(tmp_path):
    """Test main con file messaggio e utenti personalizzati da CLI."""
    message_file = tmp_path / "custom_message.txt"
    message_file.write_text("Custom message", encoding="utf-8")

    users_file = tmp_path / "custom_users.txt"
    users_file.write_text("user1", encoding="utf-8")

    with patch("sys.argv", ["broadcast.py", str(message_file), str(users_file)]):
        with patch("broadcast.load_dotenv"):
            with patch(
                "os.getenv",
                side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
            ):
                with patch("asyncio.run") as mock_run:
                    from broadcast import main

                    main()
                    mock_run.assert_called_once()


def test_main_default_files():
    """Test main con file di default (message.txt e users.txt)."""
    with patch("sys.argv", ["broadcast.py"]):
        with patch("broadcast.load_dotenv"):
            with patch(
                "os.getenv",
                side_effect=lambda k, d=None: "fake_token" if k == "TELEGRAM_BOT_TOKEN" else d,
            ):
                with patch("asyncio.run") as mock_run:
                    from broadcast import main

                    main()
                    mock_run.assert_called_once()


def test_main_with_batch_size_from_env():
    """Test main con batch size da variabile d'ambiente."""

    def mock_getenv(key, default=None):
        if key == "TELEGRAM_BOT_TOKEN":
            return "fake_token"
        if key == "BROADCAST_BATCH_SIZE":
            return "5"
        return default

    with patch("sys.argv", ["broadcast.py"]):
        with patch("broadcast.load_dotenv"):
            with patch("os.getenv", side_effect=mock_getenv):
                with patch("asyncio.run") as mock_run:
                    from broadcast import main

                    main()
                    mock_run.assert_called_once()
