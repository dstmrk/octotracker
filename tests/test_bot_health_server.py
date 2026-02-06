#!/usr/bin/env python3
"""
Test per run_health_server e background task in bot.py
"""

import asyncio
import os
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

# Mock WEBHOOK_SECRET prima di importare bot (evita ValueError)
os.environ.setdefault("WEBHOOK_SECRET", "test-secret-token-for-testing")


@pytest.mark.asyncio
async def test_post_init_creates_health_task():
    """Test che post_init crei il task health_server"""
    from bot import post_init

    # Mock application
    mock_app = MagicMock()
    mock_app.bot.token = "test_token_123"
    mock_app.bot_data = {}

    with (
        patch("bot.scraper_daily_task", new_callable=AsyncMock),
        patch("bot.checker_daily_task", new_callable=AsyncMock),
        patch("bot.run_health_server", new_callable=AsyncMock),
        patch("asyncio.create_task") as mock_create_task,
    ):
        # Ogni chiamata a create_task restituisce un mock diverso
        mock_task_1 = MagicMock()
        mock_task_2 = MagicMock()
        mock_task_3 = MagicMock()
        mock_create_task.side_effect = [mock_task_1, mock_task_2, mock_task_3]

        # Run post_init
        await post_init(mock_app)

        # Verifica che tutti e 3 i task siano stati creati
        assert mock_create_task.call_count == 3

        # Verifica che i task siano stati salvati in bot_data
        assert "scraper_task" in mock_app.bot_data
        assert "checker_task" in mock_app.bot_data
        assert "health_task" in mock_app.bot_data

        # Verifica che add_done_callback sia stato chiamato su ogni task
        for task_mock in (mock_task_1, mock_task_2, mock_task_3):
            task_mock.add_done_callback.assert_called_once()


def test_task_done_callback_logs_exception():
    """Test che _task_done_callback logga errori dei task crashati"""
    from bot import _task_done_callback

    mock_task = MagicMock()
    mock_task.get_name.return_value = "test_task"
    mock_task.exception.return_value = RuntimeError("task crashed")

    with patch("bot.logger") as mock_logger:
        _task_done_callback(mock_task)
        mock_logger.critical.assert_called_once()
        assert "test_task" in mock_logger.critical.call_args[0][0]


def test_task_done_callback_handles_cancellation():
    """Test che _task_done_callback gestisce task cancellati"""
    from bot import _task_done_callback

    mock_task = MagicMock()
    mock_task.get_name.return_value = "cancelled_task"
    mock_task.exception.side_effect = asyncio.CancelledError()

    with patch("bot.logger") as mock_logger:
        _task_done_callback(mock_task)
        mock_logger.info.assert_called_once()
        mock_logger.critical.assert_not_called()


def test_task_done_callback_no_exception():
    """Test che _task_done_callback non logga errori se il task termina normalmente"""
    from bot import _task_done_callback

    mock_task = MagicMock()
    mock_task.get_name.return_value = "normal_task"
    mock_task.exception.return_value = None

    with patch("bot.logger") as mock_logger:
        _task_done_callback(mock_task)
        mock_logger.critical.assert_not_called()
