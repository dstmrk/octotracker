"""
Test per la persistenza della scadenza offerte fisse e i reminder in database.py
"""

import sqlite3

import pytest

import database
from database import (
    get_connection,
    get_scadenza_reminded_map,
    init_db,
    load_user,
    mark_scadenza_reminded,
    save_user,
)


def _base_user(scadenza=None, gas_scadenza=None, gas=True):
    """Costruisce una struttura utente con scadenza opzionale"""
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
    if gas:
        data["gas"] = {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        }
        if gas_scadenza is not None:
            data["gas"]["scadenza"] = gas_scadenza
    return data


# ========== PERSISTENZA SCADENZA ==========


def test_save_and_load_luce_scadenza():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    loaded = load_user("1")
    assert loaded["luce"]["scadenza"] == "2026-03-15"


def test_save_and_load_gas_scadenza():
    save_user("1", _base_user(scadenza="2026-03-15", gas_scadenza="2026-05-20"))
    loaded = load_user("1")
    assert loaded["luce"]["scadenza"] == "2026-03-15"
    assert loaded["gas"]["scadenza"] == "2026-05-20"


def test_scadenza_absent_when_not_provided():
    save_user("1", _base_user(gas=False))
    loaded = load_user("1")
    assert "scadenza" not in loaded["luce"]


def test_update_overwrites_scadenza():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    save_user("1", _base_user(scadenza="2027-01-01", gas=False))
    loaded = load_user("1")
    assert loaded["luce"]["scadenza"] == "2027-01-01"


def test_update_can_clear_scadenza():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    # Aggiornamento senza scadenza (es. passaggio a variabile) → azzerata
    save_user("1", _base_user(scadenza=None, gas=False))
    loaded = load_user("1")
    assert "scadenza" not in loaded["luce"]


# ========== REMINDER MAP / MARK ==========


def test_reminded_map_defaults_to_none():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    reminded = get_scadenza_reminded_map()
    assert reminded["1"] == {"luce": None, "gas": None}


def test_mark_scadenza_reminded_luce():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    assert mark_scadenza_reminded("1", "luce", "2026-03-15") is True
    reminded = get_scadenza_reminded_map()
    assert reminded["1"]["luce"] == "2026-03-15"
    assert reminded["1"]["gas"] is None


def test_mark_scadenza_reminded_gas():
    save_user("1", _base_user(scadenza="2026-03-15", gas_scadenza="2026-05-20"))
    mark_scadenza_reminded("1", "gas", "2026-05-20")
    reminded = get_scadenza_reminded_map()
    assert reminded["1"]["gas"] == "2026-05-20"


def test_mark_scadenza_reminded_invalid_service():
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    assert mark_scadenza_reminded("1", "acqua", "2026-03-15") is False


def test_reminded_preserved_across_update():
    """Il flag reminded non deve essere azzerato da un normale save_user"""
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))
    mark_scadenza_reminded("1", "luce", "2026-03-15")

    # L'utente aggiorna i dati mantenendo la stessa scadenza
    save_user("1", _base_user(scadenza="2026-03-15", gas=False))

    reminded = get_scadenza_reminded_map()
    assert reminded["1"]["luce"] == "2026-03-15"


# ========== MIGRAZIONE SCHEMA ==========


def test_migration_adds_columns_to_legacy_db(tmp_path, monkeypatch):
    """init_db aggiunge le colonne scadenza a un DB creato senza di esse"""
    legacy_db = tmp_path / "legacy.db"
    monkeypatch.setattr(database, "DB_FILE", legacy_db)

    # Crea una tabella users "vecchia" senza le nuove colonne
    conn = sqlite3.connect(legacy_db)
    conn.execute(
        """
        CREATE TABLE users (
            user_id TEXT PRIMARY KEY,
            luce_tipo TEXT NOT NULL,
            luce_fascia TEXT NOT NULL,
            luce_energia REAL NOT NULL,
            luce_commercializzazione REAL NOT NULL,
            gas_tipo TEXT,
            gas_fascia TEXT,
            gas_energia REAL,
            gas_commercializzazione REAL,
            luce_consumo_f1 REAL,
            luce_consumo_f2 REAL,
            luce_consumo_f3 REAL,
            gas_consumo_annuo REAL,
            last_notified_rates TEXT,
            pending_rates TEXT,
            last_feedback_at TIMESTAMP,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
        """
    )
    conn.commit()
    conn.close()

    # init_db deve applicare la migrazione senza errori
    init_db()

    with get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = {row["name"] for row in cursor.fetchall()}

    assert "luce_scadenza" in columns
    assert "gas_scadenza" in columns
    assert "luce_scadenza_reminded" in columns
    assert "gas_scadenza_reminded" in columns


def test_migration_is_idempotent(tmp_path, monkeypatch):
    """Chiamare init_db più volte non causa errori (colonne già presenti)"""
    db_path = tmp_path / "idempotent.db"
    monkeypatch.setattr(database, "DB_FILE", db_path)
    init_db()
    init_db()  # Seconda chiamata non deve sollevare eccezioni

    with get_connection() as conn:
        cursor = conn.execute("PRAGMA table_info(users)")
        columns = {row["name"] for row in cursor.fetchall()}
    assert "luce_scadenza" in columns


def test_reminded_map_db_error(monkeypatch):
    """get_scadenza_reminded_map restituisce dict vuoto su errore DB"""

    def boom():
        raise sqlite3.Error("boom")

    monkeypatch.setattr(database, "get_connection", boom)
    assert get_scadenza_reminded_map() == {}


def test_mark_reminded_db_error(monkeypatch):
    """mark_scadenza_reminded restituisce False su errore DB"""

    def boom():
        raise sqlite3.Error("boom")

    monkeypatch.setattr(database, "get_connection", boom)
    assert mark_scadenza_reminded("1", "luce", "2026-03-15") is False


if __name__ == "__main__":
    pytest.main([__file__, "-v"])
