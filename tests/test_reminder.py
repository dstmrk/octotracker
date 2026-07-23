"""
Test per reminder.py - Reminder scadenza offerte a prezzo fisso
"""

from datetime import date
from unittest.mock import AsyncMock, MagicMock

import pytest

import reminder
from database import get_scadenza_reminded_map, save_user
from reminder import (
    _format_days_left,
    _get_expiring_services,
    _prepare_reminder,
    check_and_send_reminders,
    format_reminder_message,
)

TODAY = date(2025, 1, 1)


def _fissa_user(scadenza=None, gas=None):
    data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }
    if scadenza is not None:
        data["luce"]["scadenza"] = scadenza
    if gas is not None:
        data["gas"] = gas
    return data


# ========== _get_expiring_services ==========


def test_expiring_luce_within_window():
    user = _fissa_user(scadenza="2025-01-20")  # 19 giorni
    result = _get_expiring_services(user, {}, TODAY)
    assert result == [("luce", "2025-01-20", 19)]


def test_expiring_luce_exactly_30_days():
    user = _fissa_user(scadenza="2025-01-31")  # 30 giorni → incluso
    result = _get_expiring_services(user, {}, TODAY)
    assert result == [("luce", "2025-01-31", 30)]


def test_not_expiring_beyond_window():
    user = _fissa_user(scadenza="2025-03-01")  # ~59 giorni
    assert _get_expiring_services(user, {}, TODAY) == []


def test_not_expiring_already_passed():
    user = _fissa_user(scadenza="2024-12-01")  # già scaduta
    assert _get_expiring_services(user, {}, TODAY) == []


def test_not_expiring_variabile():
    user = _fissa_user(scadenza="2025-01-20")
    user["luce"]["tipo"] = "variabile"
    assert _get_expiring_services(user, {}, TODAY) == []


def test_not_expiring_without_scadenza():
    user = _fissa_user(scadenza=None)
    assert _get_expiring_services(user, {}, TODAY) == []


def test_not_expiring_with_malformed_scadenza():
    """Una scadenza non parsabile viene ignorata senza errori"""
    user = _fissa_user(scadenza="data-non-valida")
    assert _get_expiring_services(user, {}, TODAY) == []


def test_not_expiring_already_reminded():
    user = _fissa_user(scadenza="2025-01-20")
    reminded = {"luce": "2025-01-20"}
    assert _get_expiring_services(user, reminded, TODAY) == []


def test_reminded_different_scadenza_still_fires():
    """Se la scadenza è cambiata rispetto a quella già notificata, si notifica di nuovo"""
    user = _fissa_user(scadenza="2025-01-20")
    reminded = {"luce": "2024-06-01"}  # vecchia scadenza
    assert _get_expiring_services(user, reminded, TODAY) == [("luce", "2025-01-20", 19)]


def test_expiring_gas():
    gas = {
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.456,
        "commercializzazione": 84.0,
        "scadenza": "2025-01-15",
    }
    user = _fissa_user(scadenza=None, gas=gas)
    result = _get_expiring_services(user, {}, TODAY)
    assert result == [("gas", "2025-01-15", 14)]


def test_expiring_both_luce_and_gas():
    gas = {
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.456,
        "commercializzazione": 84.0,
        "scadenza": "2025-01-15",
    }
    user = _fissa_user(scadenza="2025-01-20", gas=gas)
    result = _get_expiring_services(user, {}, TODAY)
    assert ("luce", "2025-01-20", 19) in result
    assert ("gas", "2025-01-15", 14) in result


def test_gas_variabile_not_expiring():
    gas = {
        "tipo": "variabile",
        "fascia": "monoraria",
        "energia": 0.08,
        "commercializzazione": 84.0,
        "scadenza": "2025-01-15",
    }
    user = _fissa_user(scadenza=None, gas=gas)
    assert _get_expiring_services(user, {}, TODAY) == []


# ========== _format_days_left ==========


def test_format_days_left_today():
    assert _format_days_left(0) == "oggi"


def test_format_days_left_one():
    assert _format_days_left(1) == "tra 1 giorno"


def test_format_days_left_many():
    assert _format_days_left(15) == "tra 15 giorni"


# ========== format_reminder_message ==========


def test_message_contains_core_info():
    user = _fissa_user(scadenza="2025-01-20")
    expiring = [("luce", "2025-01-20", 19)]
    msg = format_reminder_message(expiring, user, {})

    assert "sta per scadere" in msg
    assert "20/01/2025" in msg
    assert "tra 19 giorni" in msg
    assert "La tua tariffa" in msg
    assert "variabile del momento" in msg


def test_message_includes_octopus_comparison():
    user = _fissa_user(scadenza="2025-01-20")
    expiring = [("luce", "2025-01-20", 19)]
    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.130,
                    "commercializzazione": 60.0,
                    "cod_offerta": "OCTO123",
                }
            }
        }
    }
    msg = format_reminder_message(expiring, user, current_rates)
    assert "Offerta fissa Octopus oggi" in msg
    assert "OCTO123" in msg


def test_message_without_octopus_rate():
    user = _fissa_user(scadenza="2025-01-20")
    expiring = [("luce", "2025-01-20", 19)]
    msg = format_reminder_message(expiring, user, {})
    assert "Offerta fissa Octopus oggi" not in msg


# ========== _prepare_reminder ==========


def test_prepare_reminder_none_when_nothing_expiring():
    user = _fissa_user(scadenza="2025-06-01")  # troppo lontana
    result = _prepare_reminder("1", user, {}, {}, TODAY)
    assert result is None


def test_prepare_reminder_returns_message():
    user = _fissa_user(scadenza="2025-01-20")
    result = _prepare_reminder("1", user, {}, {}, TODAY)
    assert result is not None
    message, expiring = result
    assert "sta per scadere" in message
    assert expiring == [("luce", "2025-01-20", 19)]


# ========== check_and_send_reminders (integrazione) ==========


@pytest.mark.asyncio
async def test_check_and_send_no_users(monkeypatch, caplog):
    monkeypatch.setattr(reminder, "Bot", MagicMock())
    with caplog.at_level("WARNING"):
        await check_and_send_reminders("fake-token", today=TODAY)
    assert "Nessun utente registrato" in caplog.text


@pytest.mark.asyncio
async def test_check_and_send_marks_reminded(monkeypatch):
    save_user("1", _fissa_user(scadenza="2025-01-20"))

    mock_send = AsyncMock(return_value=True)
    monkeypatch.setattr(reminder, "send_notification", mock_send)
    monkeypatch.setattr(reminder, "Bot", MagicMock())

    await check_and_send_reminders("fake-token", today=TODAY)

    # È stato inviato un messaggio
    mock_send.assert_awaited_once()
    # Ed è stato marcato come notificato
    reminded = get_scadenza_reminded_map()
    assert reminded["1"]["luce"] == "2025-01-20"


@pytest.mark.asyncio
async def test_check_and_send_not_marked_on_failure(monkeypatch):
    save_user("1", _fissa_user(scadenza="2025-01-20"))

    mock_send = AsyncMock(return_value=False)  # invio fallito
    monkeypatch.setattr(reminder, "send_notification", mock_send)
    monkeypatch.setattr(reminder, "Bot", MagicMock())

    await check_and_send_reminders("fake-token", today=TODAY)

    reminded = get_scadenza_reminded_map()
    assert reminded["1"]["luce"] is None


@pytest.mark.asyncio
async def test_check_and_send_skips_when_not_expiring(monkeypatch):
    save_user("1", _fissa_user(scadenza="2025-06-01"))  # troppo lontana

    mock_send = AsyncMock(return_value=True)
    monkeypatch.setattr(reminder, "send_notification", mock_send)
    monkeypatch.setattr(reminder, "Bot", MagicMock())

    await check_and_send_reminders("fake-token", today=TODAY)

    mock_send.assert_not_awaited()


@pytest.mark.asyncio
async def test_check_and_send_not_resent_if_already_reminded(monkeypatch):
    save_user("1", _fissa_user(scadenza="2025-01-20"))

    mock_send = AsyncMock(return_value=True)
    monkeypatch.setattr(reminder, "send_notification", mock_send)
    monkeypatch.setattr(reminder, "Bot", MagicMock())

    # Primo giro: invia
    await check_and_send_reminders("fake-token", today=TODAY)
    assert mock_send.await_count == 1

    # Secondo giro (giorno dopo): non deve reinviare
    mock_send.reset_mock()
    await check_and_send_reminders("fake-token", today=date(2025, 1, 2))
    mock_send.assert_not_awaited()


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
