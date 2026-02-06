"""Shared fixtures for OctoTracker tests"""

import os
import sys
import tempfile
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock

import pytest
from telegram import CallbackQuery, Message, Update, User
from telegram.ext import ContextTypes

# Ensure WEBHOOK_SECRET is set before any bot/handler imports
os.environ.setdefault("WEBHOOK_SECRET", "test_secret_token_for_testing_only")

# Ensure project root is in path for imports
sys.path.insert(0, str(Path(__file__).parent.parent))

import database
from database import init_db


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
    """Crea mock CallbackQuery per pulsanti inline (con Update wrapper)"""
    query = MagicMock(spec=CallbackQuery)
    query.answer = AsyncMock()
    query.edit_message_text = AsyncMock()
    query.from_user = MagicMock(spec=User)
    query.from_user.id = 123456789
    query.data = ""

    update = MagicMock(spec=Update)
    update.callback_query = query
    update.effective_user = query.from_user
    update.message = None

    return update


@pytest.fixture
def mock_context():
    """Crea mock ContextTypes.DEFAULT_TYPE"""
    context = MagicMock(spec=ContextTypes.DEFAULT_TYPE)
    context.user_data = {}
    return context
