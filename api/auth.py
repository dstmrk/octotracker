#!/usr/bin/env python3
"""
Autenticazione Telegram WebApp per OctoTracker Mini App

Implementa la validazione di initData secondo la documentazione ufficiale:
https://core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app

L'initData contiene:
- user: JSON con dati utente (id, first_name, last_name, username, etc.)
- auth_date: timestamp Unix della generazione
- hash: firma HMAC-SHA256 per verificare autenticità

La firma viene calcolata usando il bot token come chiave segreta.
"""

import hashlib
import hmac
import json
import logging
import time
from urllib.parse import parse_qs

logger = logging.getLogger(__name__)


class TelegramAuthError(Exception):
    """Eccezione per errori di autenticazione Telegram"""

    pass


def parse_init_data(init_data: str) -> dict:
    """
    Parsa initData query string in dizionario.

    Args:
        init_data: Query string formato "key1=value1&key2=value2"

    Returns:
        Dict con chiavi e valori, user viene parsato come JSON
    """
    if not init_data:
        return {}

    try:
        # parse_qs ritorna liste per ogni valore
        parsed = parse_qs(init_data, keep_blank_values=True)
        result = {k: v[0] if v else "" for k, v in parsed.items()}

        # Parsa user JSON se presente
        if "user" in result and result["user"]:
            try:
                result["user"] = json.loads(result["user"])
            except json.JSONDecodeError:
                logger.warning("Failed to parse user JSON in initData")

        return result

    except Exception as e:
        logger.warning(f"Failed to parse initData: {e}")
        return {}


def verify_telegram_auth(init_data: str, bot_token: str) -> dict | None:
    """
    Verifica la firma crittografica di initData.

    Implementa l'algoritmo di verifica ufficiale Telegram:
    1. Estrae hash da initData
    2. Ricostruisce data_check_string (chiavi ordinate, senza hash)
    3. Calcola HMAC-SHA256 usando secret derivato dal bot token
    4. Confronta hash calcolato con hash ricevuto

    Args:
        init_data: Query string initData da Telegram
        bot_token: Token del bot Telegram

    Returns:
        Dict con dati parsati se valido, None se firma non valida
    """
    if not init_data or not bot_token:
        return None

    try:
        # Parsa la query string
        parsed = parse_qs(init_data, keep_blank_values=True)
        params = {k: v[0] if v else "" for k, v in parsed.items()}

        # Estrai hash
        received_hash = params.pop("hash", None)
        if not received_hash:
            logger.debug("No hash in initData")
            return None

        # Ricostruisci data_check_string (chiavi ordinate alfabeticamente)
        data_check_string = "\n".join(f"{k}={v}" for k, v in sorted(params.items()))

        # Calcola secret key: HMAC-SHA256("WebAppData", bot_token)
        secret_key = hmac.new(b"WebAppData", bot_token.encode(), hashlib.sha256).digest()

        # Calcola hash atteso
        expected_hash = hmac.new(secret_key, data_check_string.encode(), hashlib.sha256).hexdigest()

        # Confronto sicuro (timing-safe)
        if not hmac.compare_digest(received_hash, expected_hash):
            logger.debug("Hash mismatch in initData verification")
            return None

        # Firma valida, parsa user JSON
        if "user" in params and params["user"]:
            try:
                params["user"] = json.loads(params["user"])
            except json.JSONDecodeError:
                logger.warning("Valid signature but invalid user JSON")
                return None

        return params

    except Exception as e:
        logger.error(f"Error verifying Telegram auth: {e}")
        return None


def validate_init_data(
    init_data: str,
    bot_token: str,
    max_age_seconds: int = 86400,  # 24 ore default
) -> dict:
    """
    Validazione completa di initData con controllo expiry.

    Verifica:
    1. Firma crittografica valida
    2. auth_date non troppo vecchio
    3. Presenza di user con id

    Args:
        init_data: Query string initData da Telegram
        bot_token: Token del bot Telegram
        max_age_seconds: Età massima accettata per auth_date (default 24h)

    Returns:
        Dict con dati validati

    Raises:
        TelegramAuthError: Se validazione fallisce
    """
    # Verifica firma
    auth_data = verify_telegram_auth(init_data, bot_token)
    if auth_data is None:
        raise TelegramAuthError("Invalid Telegram authentication signature")

    # Verifica auth_date non scaduto
    try:
        auth_date = int(auth_data.get("auth_date", 0))
        current_time = int(time.time())

        if current_time - auth_date > max_age_seconds:
            raise TelegramAuthError(
                f"Authentication expired (auth_date: {auth_date}, "
                f"age: {current_time - auth_date}s, max: {max_age_seconds}s)"
            )
    except (ValueError, TypeError) as e:
        raise TelegramAuthError(f"Invalid auth_date format: {e}") from e

    # Verifica presenza user
    user = auth_data.get("user")
    if not user or not isinstance(user, dict):
        raise TelegramAuthError("Missing user data in authentication")

    if "id" not in user:
        raise TelegramAuthError("Missing user id in authentication")

    logger.debug(f"Telegram auth validated for user {user.get('id')}")
    return auth_data
