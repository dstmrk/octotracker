#!/usr/bin/env python3
"""
Gestione database SQLite per gli utenti di OctoTracker
"""
import json
import logging
import sqlite3
from contextlib import contextmanager
from pathlib import Path
from typing import Any

# Setup logger
logger = logging.getLogger(__name__)

# File database
DATA_DIR = Path(__file__).parent / "data"
DB_FILE = DATA_DIR / "users.db"

# Valori validi per validazione
VALID_TYPES = {"fissa", "variabile"}
VALID_FASCE_LUCE = {"monoraria", "bioraria", "trioraria"}
VALID_FASCE_GAS = {"monoraria"}

# Schema SQL
SCHEMA = """
CREATE TABLE IF NOT EXISTS users (
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
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
"""


@contextmanager
def get_connection():
    """Context manager per connessione DB con gestione errori"""
    conn = None
    try:
        DATA_DIR.mkdir(exist_ok=True)
        conn = sqlite3.connect(DB_FILE, timeout=30.0)  # Timeout aumentato per concurrent writes
        conn.row_factory = sqlite3.Row  # Accesso per nome colonna
        yield conn
        conn.commit()
    except sqlite3.Error as e:
        if conn:
            conn.rollback()
        logger.error(f"❌ Errore database: {e}")
        raise
    finally:
        if conn:
            conn.close()


def init_db() -> None:
    """Inizializza database e crea tabelle"""
    try:
        with get_connection() as conn:
            conn.executescript(SCHEMA)
        logger.info("✅ Database inizializzato")
    except sqlite3.Error as e:
        logger.error(f"❌ Errore inizializzazione database: {e}")
        raise


def _row_to_dict(row: sqlite3.Row) -> dict[str, Any]:
    """Converte Row SQLite in formato dict compatibile con JSON attuale"""
    user_data = {
        "luce": {
            "tipo": row["luce_tipo"],
            "fascia": row["luce_fascia"],
            "energia": row["luce_energia"],
            "commercializzazione": row["luce_commercializzazione"],
        }
    }

    # Aggiungi consumi luce se presenti
    if row["luce_consumo_f1"] is not None:
        user_data["luce"]["consumo_f1"] = row["luce_consumo_f1"]
    if row["luce_consumo_f2"] is not None:
        user_data["luce"]["consumo_f2"] = row["luce_consumo_f2"]
    if row["luce_consumo_f3"] is not None:
        user_data["luce"]["consumo_f3"] = row["luce_consumo_f3"]

    # Aggiungi gas solo se presente
    if row["gas_tipo"]:
        user_data["gas"] = {
            "tipo": row["gas_tipo"],
            "fascia": row["gas_fascia"],
            "energia": row["gas_energia"],
            "commercializzazione": row["gas_commercializzazione"],
        }
        # Aggiungi consumo gas se presente
        if row["gas_consumo_annuo"] is not None:
            user_data["gas"]["consumo_annuo"] = row["gas_consumo_annuo"]

    # Aggiungi last_notified_rates se presente
    if row["last_notified_rates"]:
        user_data["last_notified_rates"] = json.loads(row["last_notified_rates"])

    return user_data


def load_users() -> dict[str, Any]:
    """
    Carica tutti gli utenti dal database
    Ritorna dizionario nel formato: {user_id: user_data}
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT * FROM users")
            rows = cursor.fetchall()

            users = {}
            for row in rows:
                user_id = row["user_id"]
                users[user_id] = _row_to_dict(row)

            logger.debug(f"Caricati {len(users)} utenti dal database")
            return users

    except sqlite3.Error as e:
        logger.error(f"❌ Errore caricamento utenti: {e}")
        return {}


def load_user(user_id: str) -> dict[str, Any] | None:
    """
    Carica un singolo utente dal database
    Ritorna None se l'utente non esiste
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT * FROM users WHERE user_id = ?", (user_id,))
            row = cursor.fetchone()

            if row:
                return _row_to_dict(row)
            return None

    except sqlite3.Error as e:
        logger.error(f"❌ Errore caricamento utente {user_id}: {e}")
        return None


def save_user(user_id: str, user_data: dict[str, Any]) -> bool:
    """
    Salva o aggiorna un utente nel database
    Usa UPSERT per gestire sia insert che update
    """
    try:
        # Estrai dati luce (obbligatori)
        luce = user_data["luce"]

        # Validazione luce
        if luce["tipo"] not in VALID_TYPES:
            raise ValueError(
                f"luce.tipo non valido: '{luce['tipo']}'. Valori ammessi: {VALID_TYPES}"
            )
        if luce["fascia"] not in VALID_FASCE_LUCE:
            raise ValueError(
                f"luce.fascia non valida: '{luce['fascia']}'. Valori ammessi: {VALID_FASCE_LUCE}"
            )

        # Estrai consumi luce (opzionali)
        luce_consumo_f1 = luce.get("consumo_f1")
        luce_consumo_f2 = luce.get("consumo_f2")
        luce_consumo_f3 = luce.get("consumo_f3")

        # Estrai dati gas (opzionali)
        gas = user_data.get("gas")
        gas_tipo = gas["tipo"] if gas else None
        gas_fascia = gas["fascia"] if gas else None
        gas_energia = gas["energia"] if gas else None
        gas_comm = gas["commercializzazione"] if gas else None
        gas_consumo = gas.get("consumo_annuo") if gas else None

        # Validazione gas (se presente)
        if gas is not None:
            if gas["tipo"] not in VALID_TYPES:
                raise ValueError(
                    f"gas.tipo non valido: '{gas['tipo']}'. Valori ammessi: {VALID_TYPES}"
                )
            if gas["fascia"] not in VALID_FASCE_GAS:
                raise ValueError(
                    f"gas.fascia non valida: '{gas['fascia']}'. Valori ammessi: {VALID_FASCE_GAS}"
                )

        # Serializza last_notified_rates se presente
        last_notified = user_data.get("last_notified_rates")
        last_notified_json = json.dumps(last_notified) if last_notified else None

        with get_connection() as conn:
            conn.execute(
                """
                INSERT INTO users (
                    user_id, luce_tipo, luce_fascia, luce_energia, luce_commercializzazione,
                    gas_tipo, gas_fascia, gas_energia, gas_commercializzazione,
                    luce_consumo_f1, luce_consumo_f2, luce_consumo_f3, gas_consumo_annuo,
                    last_notified_rates, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
                ON CONFLICT(user_id) DO UPDATE SET
                    luce_tipo = excluded.luce_tipo,
                    luce_fascia = excluded.luce_fascia,
                    luce_energia = excluded.luce_energia,
                    luce_commercializzazione = excluded.luce_commercializzazione,
                    gas_tipo = excluded.gas_tipo,
                    gas_fascia = excluded.gas_fascia,
                    gas_energia = excluded.gas_energia,
                    gas_commercializzazione = excluded.gas_commercializzazione,
                    luce_consumo_f1 = excluded.luce_consumo_f1,
                    luce_consumo_f2 = excluded.luce_consumo_f2,
                    luce_consumo_f3 = excluded.luce_consumo_f3,
                    gas_consumo_annuo = excluded.gas_consumo_annuo,
                    last_notified_rates = excluded.last_notified_rates,
                    updated_at = CURRENT_TIMESTAMP
            """,
                (
                    user_id,
                    luce["tipo"],
                    luce["fascia"],
                    luce["energia"],
                    luce["commercializzazione"],
                    gas_tipo,
                    gas_fascia,
                    gas_energia,
                    gas_comm,
                    luce_consumo_f1,
                    luce_consumo_f2,
                    luce_consumo_f3,
                    gas_consumo,
                    last_notified_json,
                ),
            )

        logger.debug(f"Utente {user_id} salvato nel database")
        return True

    except KeyError as e:
        # Bug del codice: campo mancante in user_data
        logger.critical(f"🐛 BUG: Campo mancante in user_data per {user_id}: {e}")
        logger.debug(f"   user_data ricevuto: {user_data}")
        return False
    except ValueError as e:
        # Validazione fallita: tipo/fascia non validi
        logger.error(f"❌ Validazione fallita per {user_id}: {e}")
        logger.debug(f"   user_data ricevuto: {user_data}")
        return False
    except sqlite3.Error as e:
        # Problema database: connessione, lock, corruzione, etc.
        logger.error(f"❌ Errore database salvando {user_id}: {e}")
        return False


def remove_user(user_id: str) -> bool:
    """Rimuove un utente dal database"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("DELETE FROM users WHERE user_id = ?", (user_id,))
            deleted = cursor.rowcount > 0

        if deleted:
            logger.info(f"🗑️  Utente {user_id} rimosso dal database")
        return deleted

    except sqlite3.Error as e:
        logger.error(f"❌ Errore rimozione utente {user_id}: {e}")
        return False


def user_exists(user_id: str) -> bool:
    """Controlla se un utente esiste nel database"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT 1 FROM users WHERE user_id = ? LIMIT 1", (user_id,))
            return cursor.fetchone() is not None

    except sqlite3.Error as e:
        logger.error(f"❌ Errore controllo esistenza utente {user_id}: {e}")
        return False


def get_user_count() -> int:
    """Ritorna il numero totale di utenti"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT COUNT(*) as count FROM users")
            row = cursor.fetchone()
            return row["count"] if row else 0

    except sqlite3.Error as e:
        logger.error(f"❌ Errore conteggio utenti: {e}")
        return 0


if __name__ == "__main__":
    # Test inizializzazione
    init_db()
    print(f"✅ Database creato: {DB_FILE}")
