"""
Test per handlers/__init__.py - safe_answer_callback
"""

from unittest.mock import AsyncMock

import pytest
from telegram.error import BadRequest

from handlers import safe_answer_callback


@pytest.mark.asyncio
async def test_safe_answer_callback_success():
    """query.answer() va a buon fine"""
    query = AsyncMock()
    await safe_answer_callback(query)
    query.answer.assert_awaited_once()


@pytest.mark.asyncio
async def test_safe_answer_callback_query_too_old():
    """query.answer() fallisce con 'Query is too old' - errore ignorato"""
    query = AsyncMock()
    query.answer.side_effect = BadRequest(
        "Query is too old and response timeout expired or query id is invalid"
    )
    # Non deve sollevare eccezione
    await safe_answer_callback(query)
    query.answer.assert_awaited_once()


@pytest.mark.asyncio
async def test_safe_answer_callback_query_id_invalid():
    """query.answer() fallisce con 'query id is invalid' - errore ignorato"""
    query = AsyncMock()
    query.answer.side_effect = BadRequest("Query id is invalid")
    await safe_answer_callback(query)
    query.answer.assert_awaited_once()


@pytest.mark.asyncio
async def test_safe_answer_callback_other_bad_request_reraises():
    """query.answer() fallisce con altro BadRequest - errore rilanciato"""
    query = AsyncMock()
    query.answer.side_effect = BadRequest("Some other error")
    with pytest.raises(BadRequest, match="Some other error"):
        await safe_answer_callback(query)
