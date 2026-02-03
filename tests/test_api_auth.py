#!/usr/bin/env python3
"""
Test per api/auth.py - Validazione initData Telegram WebApp

Testa la verifica crittografica dei dati inviati da Telegram Mini Apps.
"""

import hashlib
import hmac
import json
import time
from urllib.parse import urlencode

import pytest

from api.auth import (
    TelegramAuthError,
    parse_init_data,
    validate_init_data,
    verify_telegram_auth,
)

# Bot token di test (non reale)
TEST_BOT_TOKEN = "1234567890:ABCdefGHIjklMNOpqrsTUVwxyz123456789"


def generate_test_init_data(
    user_id: int = 123456789,
    first_name: str = "Mario",
    last_name: str = "Rossi",
    username: str = "mariorossi",
    auth_date: int | None = None,
    bot_token: str = TEST_BOT_TOKEN,
) -> str:
    """
    Genera initData valido per i test.

    Simula esattamente come Telegram genera e firma initData.
    """
    if auth_date is None:
        auth_date = int(time.time())

    # User data come JSON
    user_data = {
        "id": user_id,
        "first_name": first_name,
        "last_name": last_name,
        "username": username,
        "language_code": "it",
    }

    # Parametri da firmare (in ordine alfabetico per la firma)
    params = {
        "auth_date": str(auth_date),
        "user": json.dumps(user_data, separators=(",", ":")),
    }

    # Calcola data_check_string (chiavi ordinate alfabeticamente)
    data_check_string = "\n".join(f"{k}={v}" for k, v in sorted(params.items()))

    # Calcola hash come fa Telegram
    secret_key = hmac.new(b"WebAppData", bot_token.encode(), hashlib.sha256).digest()
    calculated_hash = hmac.new(secret_key, data_check_string.encode(), hashlib.sha256).hexdigest()

    # Aggiungi hash ai parametri
    params["hash"] = calculated_hash

    # Ritorna come query string (formato initData)
    return urlencode(params)


class TestParseInitData:
    """Test per parse_init_data()"""

    def test_parse_valid_init_data(self):
        """Test parsing di initData valido"""
        init_data = generate_test_init_data()
        result = parse_init_data(init_data)

        assert "auth_date" in result
        assert "user" in result
        assert "hash" in result

    def test_parse_extracts_user_json(self):
        """Test che user viene parsato come JSON"""
        init_data = generate_test_init_data(user_id=999, first_name="Test", username="testuser")
        result = parse_init_data(init_data)

        # user dovrebbe essere un dict, non una stringa
        user = result["user"]
        assert isinstance(user, dict)
        assert user["id"] == 999
        assert user["first_name"] == "Test"
        assert user["username"] == "testuser"

    def test_parse_empty_string(self):
        """Test parsing stringa vuota"""
        result = parse_init_data("")
        assert result == {}

    def test_parse_invalid_format(self):
        """Test parsing formato non valido"""
        result = parse_init_data("not_valid_query_string")
        # Dovrebbe ritornare dict vuoto o parziale, non crashare
        assert isinstance(result, dict)

    def test_parse_preserves_hash(self):
        """Test che hash viene preservato"""
        init_data = generate_test_init_data()
        result = parse_init_data(init_data)

        assert "hash" in result
        assert len(result["hash"]) == 64  # SHA256 hex


class TestVerifyTelegramAuth:
    """Test per verify_telegram_auth()"""

    def test_verify_valid_signature(self):
        """Test verifica firma valida"""
        init_data = generate_test_init_data()
        result = verify_telegram_auth(init_data, TEST_BOT_TOKEN)

        assert result is not None
        assert "user" in result
        assert result["user"]["id"] == 123456789

    def test_verify_invalid_signature(self):
        """Test verifica firma non valida (token sbagliato)"""
        init_data = generate_test_init_data(bot_token=TEST_BOT_TOKEN)
        # Verifica con token diverso
        result = verify_telegram_auth(init_data, "wrong_bot_token")

        assert result is None

    def test_verify_tampered_data(self):
        """Test verifica dati manomessi"""
        init_data = generate_test_init_data()
        # Manometti i dati cambiando l'auth_date
        tampered = init_data.replace("auth_date=", "auth_date=999")
        result = verify_telegram_auth(tampered, TEST_BOT_TOKEN)

        assert result is None

    def test_verify_missing_hash(self):
        """Test verifica senza hash"""
        # Genera initData e rimuovi hash
        init_data = generate_test_init_data()
        # Rimuovi il parametro hash
        parts = [p for p in init_data.split("&") if not p.startswith("hash=")]
        no_hash = "&".join(parts)

        result = verify_telegram_auth(no_hash, TEST_BOT_TOKEN)
        assert result is None

    def test_verify_empty_init_data(self):
        """Test verifica stringa vuota"""
        result = verify_telegram_auth("", TEST_BOT_TOKEN)
        assert result is None

    def test_verify_returns_parsed_user(self):
        """Test che ritorna user come dict"""
        init_data = generate_test_init_data(user_id=42, first_name="Alice", last_name="Wonder")
        result = verify_telegram_auth(init_data, TEST_BOT_TOKEN)

        assert result is not None
        assert result["user"]["id"] == 42
        assert result["user"]["first_name"] == "Alice"
        assert result["user"]["last_name"] == "Wonder"


class TestValidateInitData:
    """Test per validate_init_data() - validazione completa con expiry"""

    def test_validate_success(self):
        """Test validazione completa con successo"""
        init_data = generate_test_init_data()
        result = validate_init_data(init_data, TEST_BOT_TOKEN)

        assert result is not None
        assert "user" in result

    def test_validate_expired_auth(self):
        """Test validazione con auth_date troppo vecchio"""
        # auth_date di 2 ore fa (default max_age è 1 ora)
        old_auth_date = int(time.time()) - 7200
        init_data = generate_test_init_data(auth_date=old_auth_date)

        with pytest.raises(TelegramAuthError) as exc_info:
            validate_init_data(init_data, TEST_BOT_TOKEN, max_age_seconds=3600)

        assert "expired" in str(exc_info.value).lower()

    def test_validate_custom_max_age(self):
        """Test validazione con max_age personalizzato"""
        # auth_date di 30 minuti fa
        recent_auth = int(time.time()) - 1800
        init_data = generate_test_init_data(auth_date=recent_auth)

        # Con max_age di 1 ora, dovrebbe essere valido
        result = validate_init_data(init_data, TEST_BOT_TOKEN, max_age_seconds=3600)
        assert result is not None

        # Con max_age di 10 minuti, dovrebbe essere scaduto
        with pytest.raises(TelegramAuthError):
            validate_init_data(init_data, TEST_BOT_TOKEN, max_age_seconds=600)

    def test_validate_invalid_signature_raises(self):
        """Test che firma non valida solleva eccezione"""
        init_data = generate_test_init_data()

        with pytest.raises(TelegramAuthError) as exc_info:
            validate_init_data(init_data, "wrong_token")

        assert "invalid" in str(exc_info.value).lower()

    def test_validate_missing_user_raises(self):
        """Test che user mancante solleva eccezione"""
        # Crea initData senza user
        auth_date = int(time.time())
        params = {"auth_date": str(auth_date)}

        # Calcola hash senza user
        data_check_string = f"auth_date={auth_date}"
        secret_key = hmac.new(b"WebAppData", TEST_BOT_TOKEN.encode(), hashlib.sha256).digest()
        calculated_hash = hmac.new(
            secret_key, data_check_string.encode(), hashlib.sha256
        ).hexdigest()
        params["hash"] = calculated_hash

        init_data = urlencode(params)

        with pytest.raises(TelegramAuthError) as exc_info:
            validate_init_data(init_data, TEST_BOT_TOKEN)

        assert "user" in str(exc_info.value).lower()

    def test_validate_extracts_user_id(self):
        """Test che estrae correttamente user_id"""
        init_data = generate_test_init_data(user_id=987654321)
        result = validate_init_data(init_data, TEST_BOT_TOKEN)

        assert result["user"]["id"] == 987654321


class TestTelegramAuthError:
    """Test per TelegramAuthError exception"""

    def test_error_message(self):
        """Test che l'errore contiene il messaggio"""
        error = TelegramAuthError("Test error message")
        assert str(error) == "Test error message"

    def test_error_is_exception(self):
        """Test che è una Exception"""
        error = TelegramAuthError("Test")
        assert isinstance(error, Exception)
