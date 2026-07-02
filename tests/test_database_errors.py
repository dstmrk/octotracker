"""
Test per i rami di gestione errori sqlite3.Error in database.py

Copre i percorsi di eccezione che usano logging.exception() per
loggare errori database su init_db, load_user, save_user, remove_user,
user_exists e get_user_count.
"""

import sqlite3
from unittest.mock import patch

import pytest

from database import (
    get_user_count,
    init_db,
    load_user,
    remove_user,
    save_user,
    user_exists,
)

VALID_USER_DATA = {
    "luce": {
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.12,
        "commercializzazione": 72.0,
    },
    "gas": None,
}


class TestInitDbErrors:
    def test_init_db_sqlite_error_propagates(self):
        """init_db logga con logging.exception() e rilancia su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("schema error")

            with pytest.raises(sqlite3.Error):
                init_db()


class TestLoadUserErrors:
    def test_load_user_sqlite_error_returns_none(self):
        """load_user logga con logging.exception() e ritorna None su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = load_user("123")

            assert result is None


class TestSaveUserErrors:
    def test_save_user_sqlite_error_returns_false(self):
        """save_user logga con logging.exception() e ritorna False su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = save_user("123", VALID_USER_DATA)

            assert result is False


class TestRemoveUserErrors:
    def test_remove_user_sqlite_error_returns_false(self):
        """remove_user logga con logging.exception() e ritorna False su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = remove_user("123")

            assert result is False


class TestUserExistsErrors:
    def test_user_exists_sqlite_error_returns_false(self):
        """user_exists logga con logging.exception() e ritorna False su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = user_exists("123")

            assert result is False


class TestGetUserCountErrors:
    def test_get_user_count_sqlite_error_returns_zero(self):
        """get_user_count logga con logging.exception() e ritorna 0 su sqlite3.Error"""
        with patch("database.get_connection") as mock_conn:
            mock_conn.side_effect = sqlite3.Error("DB error")

            result = get_user_count()

            assert result == 0
