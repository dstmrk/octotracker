#!/usr/bin/env python3
"""
Test per run_health_server in bot.py
Verifica avvio del health server su porta separata
"""
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
        # Mock create_task per ritornare mock tasks
        mock_tasks = {
            "scraper": MagicMock(),
            "checker": MagicMock(),
            "health": MagicMock(),
        }

        def create_task_side_effect(coro):
            # Identifica quale coroutine è stata passata
            if "scraper" in str(coro):
                return mock_tasks["scraper"]
            elif "checker" in str(coro):
                return mock_tasks["checker"]
            else:
                return mock_tasks["health"]

        mock_create_task.side_effect = create_task_side_effect

        # Run post_init
        await post_init(mock_app)

        # Verifica che tutti e 3 i task siano stati creati
        assert mock_create_task.call_count == 3

        # Verifica che i task siano stati salvati in bot_data
        assert "scraper_task" in mock_app.bot_data
        assert "checker_task" in mock_app.bot_data
        assert "health_task" in mock_app.bot_data
