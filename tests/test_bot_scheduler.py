"""
Test per bot.py - Scheduler (run_scraper, run_checker) e error_handler

Copre i rami di gestione eccezioni introdotti/modificati per l'uso di
logging.exception() al posto di logging.error().
"""

import asyncio
from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram.error import NetworkError, TelegramError, TimedOut

import bot


@pytest.mark.asyncio
async def test_run_scraper_connection_error(monkeypatch, caplog):
    """run_scraper logga con logging.exception() su ConnectionError"""
    monkeypatch.setattr(
        bot, "fetch_octopus_tariffe", AsyncMock(side_effect=ConnectionError("no network"))
    )

    with caplog.at_level("ERROR"):
        await bot.run_scraper()

    assert "Errore di connessione scraper" in caplog.text


@pytest.mark.asyncio
async def test_run_scraper_os_error(monkeypatch, caplog):
    """run_scraper logga con logging.exception() su OSError"""
    monkeypatch.setattr(bot, "fetch_octopus_tariffe", AsyncMock(side_effect=OSError("disk full")))

    with caplog.at_level("ERROR"):
        await bot.run_scraper()

    assert "Errore I/O scraper" in caplog.text


@pytest.mark.asyncio
async def test_run_scraper_generic_exception(monkeypatch, caplog):
    """run_scraper logga con logging.exception() su errore generico"""
    monkeypatch.setattr(bot, "fetch_octopus_tariffe", AsyncMock(side_effect=ValueError("boom")))

    with caplog.at_level("ERROR"):
        await bot.run_scraper()

    assert "Errore inatteso scraper" in caplog.text


@pytest.mark.asyncio
async def test_run_checker_telegram_error(monkeypatch, caplog):
    """run_checker logga con logging.exception() su TelegramError"""
    monkeypatch.setattr(
        bot,
        "check_and_notify_users",
        AsyncMock(side_effect=TelegramError("telegram down")),
    )

    with caplog.at_level("ERROR"):
        await bot.run_checker("fake-token")

    assert "Errore Telegram checker" in caplog.text


@pytest.mark.asyncio
async def test_run_checker_network_error(monkeypatch, caplog):
    """run_checker logga con logging.exception() su NetworkError"""
    monkeypatch.setattr(
        bot,
        "check_and_notify_users",
        AsyncMock(side_effect=NetworkError("network down")),
    )

    with caplog.at_level("ERROR"):
        await bot.run_checker("fake-token")

    assert "Errore di rete checker" in caplog.text


@pytest.mark.asyncio
async def test_run_checker_os_error(monkeypatch, caplog):
    """run_checker logga con logging.exception() su OSError"""
    monkeypatch.setattr(bot, "check_and_notify_users", AsyncMock(side_effect=OSError("disk full")))

    with caplog.at_level("ERROR"):
        await bot.run_checker("fake-token")

    assert "Errore I/O checker" in caplog.text


@pytest.mark.asyncio
async def test_run_checker_generic_exception(monkeypatch, caplog):
    """run_checker logga con logging.exception() su errore generico"""
    monkeypatch.setattr(bot, "check_and_notify_users", AsyncMock(side_effect=ValueError("boom")))

    with caplog.at_level("ERROR"):
        await bot.run_checker("fake-token")

    assert "Errore inatteso checker" in caplog.text


@pytest.mark.asyncio
async def test_error_handler_admin_alert_failure(monkeypatch, caplog):
    """error_handler logga con logging.exception() se l'invio alert admin fallisce"""
    monkeypatch.setattr(bot, "ADMIN_USER_ID", "12345")

    context = MagicMock()
    context.error = RuntimeError("errore inatteso non gestito")
    context.application.bot.send_message = AsyncMock(side_effect=TimedOut("timeout"))

    with caplog.at_level("ERROR"):
        await bot.error_handler(update=None, context=context)

    assert "Errore invio alert admin" in caplog.text


@pytest.mark.asyncio
async def test_scraper_daily_task_logs_unhandled_error(monkeypatch, caplog):
    """scraper_daily_task logga con logging.exception() se run_scraper propaga un errore"""
    monkeypatch.setattr(bot, "calculate_seconds_until_next_run", lambda hour: 0)
    monkeypatch.setattr(bot.asyncio, "sleep", AsyncMock())
    monkeypatch.setattr(
        bot,
        "run_scraper",
        AsyncMock(side_effect=[ValueError("boom"), asyncio.CancelledError()]),
    )

    with caplog.at_level("ERROR"), pytest.raises(asyncio.CancelledError):
        await bot.scraper_daily_task()

    assert "Errore non gestito in scraper_daily_task" in caplog.text


@pytest.mark.asyncio
async def test_checker_daily_task_logs_unhandled_error(monkeypatch, caplog):
    """checker_daily_task logga con logging.exception() se run_checker propaga un errore"""
    monkeypatch.setattr(bot, "calculate_seconds_until_next_run", lambda hour: 0)
    monkeypatch.setattr(bot.asyncio, "sleep", AsyncMock())
    monkeypatch.setattr(
        bot,
        "run_checker",
        AsyncMock(side_effect=[ValueError("boom"), asyncio.CancelledError()]),
    )

    with caplog.at_level("ERROR"), pytest.raises(asyncio.CancelledError):
        await bot.checker_daily_task("fake-token")

    assert "Errore non gestito in checker_daily_task" in caplog.text
