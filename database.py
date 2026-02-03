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
DB_FILE = DATA_DIR / "octotracker.db"

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
    pending_rates TEXT,
    last_feedback_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    feedback_type TEXT NOT NULL,
    rating INTEGER,
    comment TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_feedback_user ON feedback(user_id);
CREATE INDEX IF NOT EXISTS idx_feedback_created ON feedback(created_at DESC);

CREATE TABLE IF NOT EXISTS rate_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    data_fonte TEXT NOT NULL,
    servizio TEXT NOT NULL,
    tipo TEXT NOT NULL,
    fascia TEXT NOT NULL,
    energia REAL NOT NULL,
    commercializzazione REAL,
    cod_offerta TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(data_fonte, servizio, tipo, fascia)
);

CREATE INDEX IF NOT EXISTS idx_rate_history_data ON rate_history(data_fonte);
CREATE INDEX IF NOT EXISTS idx_rate_history_servizio ON rate_history(servizio, tipo, fascia);
"""


@contextmanager
def get_connection():
    """Context manager per connessione DB con gestione errori"""
    conn = None
    try:
        DATA_DIR.mkdir(exist_ok=True)
        conn = sqlite3.connect(DB_FILE, timeout=30.0)  # Timeout aumentato per concurrent writes
        conn.row_factory = sqlite3.Row  # Accesso per nome colonna
        conn.execute("PRAGMA foreign_keys = ON")  # Abilita foreign key constraints
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


def _column_exists(conn: sqlite3.Connection, table: str, column: str) -> bool:
    """Controlla se una colonna esiste in una tabella"""
    cursor = conn.execute(f"PRAGMA table_info({table})")
    columns = [row[1] for row in cursor.fetchall()]
    return column in columns


def _migrate_feedback_schema() -> None:
    """Migrazione per aggiungere supporto feedback a database esistenti"""
    try:
        with get_connection() as conn:
            # Controlla se la colonna last_feedback_at esiste già
            if not _column_exists(conn, "users", "last_feedback_at"):
                logger.info("🔄 Aggiunta colonna last_feedback_at alla tabella users")
                conn.execute("ALTER TABLE users ADD COLUMN last_feedback_at TIMESTAMP")
                logger.info("✅ Migration feedback completata")
    except sqlite3.Error as e:
        logger.error(f"❌ Errore migration feedback: {e}")
        raise


def _has_cascade_delete(conn: sqlite3.Connection, table: str) -> bool:
    """Verifica se la tabella ha ON DELETE CASCADE nella FOREIGN KEY"""
    cursor = conn.execute(f"SELECT sql FROM sqlite_master WHERE type='table' AND name='{table}'")
    row = cursor.fetchone()
    if row:
        table_sql = row[0]
        return "ON DELETE CASCADE" in table_sql
    return False


def _migrate_feedback_cascade() -> None:
    """Migrazione per aggiungere ON DELETE CASCADE alla tabella feedback"""
    try:
        with get_connection() as conn:
            # Controlla se la tabella feedback esiste
            cursor = conn.execute(
                "SELECT name FROM sqlite_master WHERE type='table' AND name='feedback'"
            )
            if not cursor.fetchone():
                # Tabella non esiste ancora, verrà creata con lo schema corretto
                return

            # Controlla se ha già ON DELETE CASCADE
            if _has_cascade_delete(conn, "feedback"):
                logger.debug("Tabella feedback ha già ON DELETE CASCADE")
                return

            logger.info("🔄 Migrazione tabella feedback per aggiungere ON DELETE CASCADE")

            # 1. Rinomina la vecchia tabella
            conn.execute("ALTER TABLE feedback RENAME TO feedback_old")

            # 2. Crea la nuova tabella con ON DELETE CASCADE
            conn.execute(
                """
                CREATE TABLE feedback (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    user_id TEXT NOT NULL,
                    feedback_type TEXT NOT NULL,
                    rating INTEGER,
                    comment TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    FOREIGN KEY (user_id) REFERENCES users(user_id) ON DELETE CASCADE
                )
            """
            )

            # 3. Copia solo i feedback validi (dove user_id esiste ancora in users)
            conn.execute(
                """
                INSERT INTO feedback (id, user_id, feedback_type, rating, comment, created_at)
                SELECT f.id, f.user_id, f.feedback_type, f.rating, f.comment, f.created_at
                FROM feedback_old f
                WHERE EXISTS (SELECT 1 FROM users u WHERE u.user_id = f.user_id)
            """
            )

            # 4. Ricrea gli indici
            conn.execute("CREATE INDEX idx_feedback_user ON feedback(user_id)")
            conn.execute("CREATE INDEX idx_feedback_created ON feedback(created_at DESC)")

            # 5. Verifica quanti feedback sono stati copiati vs eliminati
            cursor_old = conn.execute("SELECT COUNT(*) as count FROM feedback_old")
            old_count = cursor_old.fetchone()["count"]
            cursor_new = conn.execute("SELECT COUNT(*) as count FROM feedback")
            new_count = cursor_new.fetchone()["count"]

            # 6. Elimina la vecchia tabella
            conn.execute("DROP TABLE feedback_old")

            orphaned = old_count - new_count
            if orphaned > 0:
                logger.info(
                    f"✅ Migration feedback CASCADE completata: "
                    f"{new_count} feedback mantenuti, {orphaned} feedback orfani rimossi"
                )
            else:
                logger.info(
                    f"✅ Migration feedback CASCADE completata: {new_count} feedback migrati"
                )

    except sqlite3.Error as e:
        logger.error(f"❌ Errore migration feedback cascade: {e}")
        raise


def _migrate_pending_rates() -> None:
    """Migrazione per aggiungere colonna pending_rates a database esistenti"""
    try:
        with get_connection() as conn:
            if not _column_exists(conn, "users", "pending_rates"):
                logger.info("🔄 Aggiunta colonna pending_rates alla tabella users")
                conn.execute("ALTER TABLE users ADD COLUMN pending_rates TEXT")
                logger.info("✅ Migration pending_rates completata")
    except sqlite3.Error as e:
        logger.error(f"❌ Errore migration pending_rates: {e}")
        raise


def init_db() -> None:
    """Inizializza database e crea tabelle"""
    try:
        with get_connection() as conn:
            conn.executescript(SCHEMA)
        # Applica migration per database esistenti
        _migrate_feedback_schema()
        _migrate_feedback_cascade()
        _migrate_pending_rates()
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


def _validate_luce_data(luce: dict[str, Any]) -> None:
    """Valida i dati della fornitura luce"""
    if luce["tipo"] not in VALID_TYPES:
        raise ValueError(f"luce.tipo non valido: '{luce['tipo']}'. Valori ammessi: {VALID_TYPES}")
    if luce["fascia"] not in VALID_FASCE_LUCE:
        raise ValueError(
            f"luce.fascia non valida: '{luce['fascia']}'. Valori ammessi: {VALID_FASCE_LUCE}"
        )


def _validate_gas_data(gas: dict[str, Any]) -> None:
    """Valida i dati della fornitura gas"""
    if gas["tipo"] not in VALID_TYPES:
        raise ValueError(f"gas.tipo non valido: '{gas['tipo']}'. Valori ammessi: {VALID_TYPES}")
    if gas["fascia"] not in VALID_FASCE_GAS:
        raise ValueError(
            f"gas.fascia non valida: '{gas['fascia']}'. Valori ammessi: {VALID_FASCE_GAS}"
        )


def _extract_gas_fields(gas: dict[str, Any] | None) -> tuple[str | None, ...]:
    """Estrae i campi gas se presenti, None altrimenti"""
    if gas is None:
        return (None, None, None, None, None)

    return (
        gas["tipo"],
        gas["fascia"],
        gas["energia"],
        gas["commercializzazione"],
        gas.get("consumo_annuo"),
    )


def save_user(user_id: str, user_data: dict[str, Any]) -> bool:
    """
    Salva o aggiorna un utente nel database
    Usa UPSERT per gestire sia insert che update
    """
    try:
        # Estrai e valida dati luce (obbligatori)
        luce = user_data["luce"]
        _validate_luce_data(luce)

        # Estrai consumi luce (opzionali)
        luce_consumo_f1 = luce.get("consumo_f1")
        luce_consumo_f2 = luce.get("consumo_f2")
        luce_consumo_f3 = luce.get("consumo_f3")

        # Estrai e valida dati gas (opzionali)
        gas = user_data.get("gas")
        if gas is not None:
            _validate_gas_data(gas)

        gas_tipo, gas_fascia, gas_energia, gas_comm, gas_consumo = _extract_gas_fields(gas)

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


# ========== FUNZIONI PENDING RATES ==========


def save_pending_rates(user_id: str, pending_rates: dict[str, Any]) -> bool:
    """
    Salva le tariffe in attesa di conferma per un utente

    Args:
        user_id: ID utente Telegram
        pending_rates: Dict con le nuove tariffe da applicare

    Returns:
        True se salvato con successo, False altrimenti
    """
    try:
        pending_json = json.dumps(pending_rates)
        with get_connection() as conn:
            conn.execute(
                "UPDATE users SET pending_rates = ?, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?",
                (pending_json, user_id),
            )
        logger.debug(f"Pending rates salvate per utente {user_id}")
        return True
    except sqlite3.Error as e:
        logger.error(f"❌ Errore salvataggio pending_rates per {user_id}: {e}")
        return False


def load_pending_rates(user_id: str) -> dict[str, Any] | None:
    """
    Carica le tariffe in attesa di conferma per un utente

    Returns:
        Dict con le tariffe pendenti o None se non presenti
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT pending_rates FROM users WHERE user_id = ?", (user_id,))
            row = cursor.fetchone()
            if row and row["pending_rates"]:
                return json.loads(row["pending_rates"])
            return None
    except sqlite3.Error as e:
        logger.error(f"❌ Errore caricamento pending_rates per {user_id}: {e}")
        return None


def clear_pending_rates(user_id: str) -> bool:
    """
    Rimuove le tariffe in attesa per un utente

    Returns:
        True se rimosso con successo, False altrimenti
    """
    try:
        with get_connection() as conn:
            conn.execute(
                "UPDATE users SET pending_rates = NULL, updated_at = CURRENT_TIMESTAMP WHERE user_id = ?",
                (user_id,),
            )
        logger.debug(f"Pending rates rimosse per utente {user_id}")
        return True
    except sqlite3.Error as e:
        logger.error(f"❌ Errore rimozione pending_rates per {user_id}: {e}")
        return False


# ========== FUNZIONI FEEDBACK ==========


def save_feedback(
    user_id: str, feedback_type: str, rating: int | None = None, comment: str | None = None
) -> bool:
    """
    Salva un nuovo feedback nel database

    Args:
        user_id: ID utente Telegram
        feedback_type: Tipo di feedback ('command', 'notification_rating', 'periodic_survey')
        rating: Rating numerico (1-5 per survey, 1/0 per thumbs, None se non applicabile)
        comment: Commento testuale opzionale

    Returns:
        True se salvato con successo, False altrimenti
    """
    try:
        with get_connection() as conn:
            conn.execute(
                """
                INSERT INTO feedback (user_id, feedback_type, rating, comment)
                VALUES (?, ?, ?, ?)
            """,
                (user_id, feedback_type, rating, comment),
            )
            # Aggiorna timestamp ultimo feedback
            conn.execute(
                "UPDATE users SET last_feedback_at = CURRENT_TIMESTAMP WHERE user_id = ?",
                (user_id,),
            )

        logger.info(f"💬 Feedback salvato per utente {user_id} (tipo: {feedback_type})")
        return True

    except sqlite3.Error as e:
        logger.error(f"❌ Errore salvataggio feedback: {e}")
        return False


def get_last_feedback_time(user_id: str) -> str | None:
    """
    Ottiene timestamp dell'ultimo feedback dell'utente

    Returns:
        Timestamp ISO string o None se mai dato feedback
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute(
                "SELECT last_feedback_at FROM users WHERE user_id = ?", (user_id,)
            )
            row = cursor.fetchone()
            return row["last_feedback_at"] if row else None

    except sqlite3.Error as e:
        logger.error(f"❌ Errore recupero last_feedback_time: {e}")
        return None


def get_recent_feedbacks(limit: int = 10) -> list[dict[str, Any]]:
    """
    Ottiene gli ultimi N feedback (per admin)

    Args:
        limit: Numero massimo di feedback da recuperare

    Returns:
        Lista di dizionari con i feedback
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute(
                """
                SELECT
                    id, user_id, feedback_type, rating, comment,
                    datetime(created_at, 'localtime') as created_at
                FROM feedback
                ORDER BY created_at DESC, id DESC
                LIMIT ?
            """,
                (limit,),
            )
            rows = cursor.fetchall()

            feedbacks = []
            for row in rows:
                feedbacks.append(
                    {
                        "id": row["id"],
                        "user_id": row["user_id"],
                        "feedback_type": row["feedback_type"],
                        "rating": row["rating"],
                        "comment": row["comment"],
                        "created_at": row["created_at"],
                    }
                )

            return feedbacks

    except sqlite3.Error as e:
        logger.error(f"❌ Errore recupero feedback recenti: {e}")
        return []


def get_feedback_count() -> int:
    """Ritorna il numero totale di feedback ricevuti"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT COUNT(*) as count FROM feedback")
            row = cursor.fetchone()
            return row["count"] if row else 0

    except sqlite3.Error as e:
        logger.error(f"❌ Errore conteggio feedback: {e}")
        return 0


# ========== FUNZIONI STORICO TARIFFE ==========


def save_rate(
    data_fonte: str,
    servizio: str,
    tipo: str,
    fascia: str,
    energia: float,
    commercializzazione: float | None = None,
    cod_offerta: str | None = None,
) -> bool:
    """
    Salva una tariffa nello storico. Ignora duplicati (stessa data/servizio/tipo/fascia).

    Returns:
        True se inserita, False se già presente o errore
    """
    try:
        with get_connection() as conn:
            conn.execute(
                """
                INSERT OR IGNORE INTO rate_history
                    (data_fonte, servizio, tipo, fascia, energia, commercializzazione, cod_offerta)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                (data_fonte, servizio, tipo, fascia, energia, commercializzazione, cod_offerta),
            )
            return True
    except sqlite3.Error as e:
        logger.error(f"❌ Errore salvataggio tariffa storico: {e}")
        return False


def save_rates_batch(data_fonte: str, rates: list[dict[str, Any]]) -> int:
    """
    Salva un batch di tariffe per una data in modo atomico (transazione).
    Ignora duplicati (già presenti nel DB).

    Args:
        data_fonte: Data fonte in formato YYYY-MM-DD
        rates: Lista di dict con chiavi: servizio, tipo, fascia, energia, commercializzazione, cod_offerta

    Returns:
        Numero di tariffe inserite (>= 0)
        -1 in caso di errore critico (nessuna tariffa salvata)
    """
    if not rates:
        return 0

    inserted = 0
    conn = None
    try:
        conn = sqlite3.connect(DB_FILE, timeout=30.0)
        conn.row_factory = sqlite3.Row
        conn.execute("BEGIN TRANSACTION")

        for rate in rates:
            conn.execute(
                """
                INSERT OR IGNORE INTO rate_history
                    (data_fonte, servizio, tipo, fascia, energia,
                     commercializzazione, cod_offerta)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    data_fonte,
                    rate["servizio"],
                    rate["tipo"],
                    rate["fascia"],
                    rate["energia"],
                    rate.get("commercializzazione"),
                    rate.get("cod_offerta"),
                ),
            )
            if conn.execute("SELECT changes()").fetchone()[0] > 0:
                inserted += 1

        conn.commit()
        logger.debug(f"Salvate {inserted} tariffe per {data_fonte}")
        return inserted

    except sqlite3.Error as e:
        logger.error(f"❌ Errore salvataggio batch tariffe: {e}")
        if conn:
            try:
                conn.rollback()
                logger.debug("Rollback transazione completato")
            except sqlite3.Error:
                pass
        return -1
    finally:
        if conn:
            conn.close()


def get_current_rates() -> dict[str, Any] | None:
    """
    Legge le tariffe più recenti dal DB e le restituisce nella struttura nested.

    Returns:
        Dict con struttura:
        {
            "luce": {"fissa": {"monoraria": {...}}, "variabile": {...}},
            "gas": {"fissa": {"monoraria": {...}}, "variabile": {...}},
        }
        oppure None se non ci sono tariffe
    """
    try:
        with get_connection() as conn:
            cursor = conn.execute(
                """
                SELECT servizio, tipo, fascia, energia, commercializzazione, cod_offerta
                FROM rate_history
                WHERE data_fonte = (SELECT MAX(data_fonte) FROM rate_history)
                """
            )
            rows = cursor.fetchall()

        if not rows:
            return None

        result: dict[str, Any] = {
            "luce": {"fissa": {}, "variabile": {}},
            "gas": {"fissa": {}, "variabile": {}},
        }

        for row in rows:
            servizio = row["servizio"]
            tipo = row["tipo"]
            fascia = row["fascia"]

            rate_data: dict[str, Any] = {"energia": row["energia"]}
            if row["commercializzazione"] is not None:
                rate_data["commercializzazione"] = row["commercializzazione"]
            if row["cod_offerta"] is not None:
                rate_data["cod_offerta"] = row["cod_offerta"]

            if servizio in result and tipo in result[servizio]:
                result[servizio][tipo][fascia] = rate_data

        return result

    except sqlite3.Error as e:
        logger.error(f"❌ Errore lettura tariffe correnti: {e}")
        return None


def get_latest_rate_date() -> str | None:
    """Ritorna la data più recente presente nello storico tariffe (formato YYYY-MM-DD)"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT MAX(data_fonte) as max_date FROM rate_history")
            row = cursor.fetchone()
            return row["max_date"] if row else None
    except sqlite3.Error as e:
        logger.error(f"❌ Errore lettura data tariffe: {e}")
        return None


def get_rate_history_dates() -> set[str]:
    """Ritorna le date già presenti nello storico (formato YYYY-MM-DD)"""
    try:
        with get_connection() as conn:
            cursor = conn.execute("SELECT DISTINCT data_fonte FROM rate_history")
            return {row["data_fonte"] for row in cursor.fetchall()}
    except sqlite3.Error as e:
        logger.error(f"❌ Errore lettura date storico: {e}")
        return set()


def get_rate_history(
    servizio: str,
    tipo: str,
    fascia: str,
    days: int = 365,
    include_commercializzazione: bool = False,
    include_stats: bool = False,
) -> dict[str, Any]:
    """
    Recupera storico tariffe filtrato per grafico Mini App.

    Args:
        servizio: "luce" o "gas"
        tipo: "fissa" o "variabile"
        fascia: "monoraria", "bioraria", "trioraria"
        days: Numero di giorni da recuperare (default 365)
        include_commercializzazione: Se includere anche i costi commercializzazione
        include_stats: Se calcolare statistiche (min, max, avg)

    Returns:
        Dict con struttura:
        {
            "labels": ["2025-01-01", "2025-01-02", ...],
            "data": [0.115, 0.116, ...],
            "commercializzazione": [72.0, 72.0, ...],  # se richiesto
            "period": {"from": "2025-01-01", "to": "2025-01-30"},
            "stats": {"min": 0.10, "max": 0.12, "avg": 0.11}  # se richiesto
        }
    """
    empty_result: dict[str, Any] = {
        "labels": [],
        "data": [],
        "period": {"from": None, "to": None},
    }

    if include_commercializzazione:
        empty_result["commercializzazione"] = []

    if include_stats:
        empty_result["stats"] = {"min": None, "max": None, "avg": None}

    # Gestisci days invalidi
    if days <= 0:
        return empty_result

    try:
        with get_connection() as conn:
            # Query con filtro temporale
            cursor = conn.execute(
                """
                SELECT data_fonte, energia, commercializzazione
                FROM rate_history
                WHERE servizio = ?
                  AND tipo = ?
                  AND fascia = ?
                  AND data_fonte >= date('now', '-' || ? || ' days')
                ORDER BY data_fonte ASC
                """,
                (servizio, tipo, fascia, days),
            )
            rows = cursor.fetchall()

        if not rows:
            return empty_result

        # Estrai dati
        labels = [row["data_fonte"] for row in rows]
        data = [row["energia"] for row in rows]

        result: dict[str, Any] = {
            "labels": labels,
            "data": data,
            "period": {
                "from": labels[0] if labels else None,
                "to": labels[-1] if labels else None,
            },
        }

        if include_commercializzazione:
            result["commercializzazione"] = [row["commercializzazione"] for row in rows]

        if include_stats and data:
            result["stats"] = {
                "min": round(min(data), 6),
                "max": round(max(data), 6),
                "avg": round(sum(data) / len(data), 6),
            }

        return result

    except sqlite3.Error as e:
        logger.error(f"❌ Errore lettura storico tariffe: {e}")
        return empty_result


if __name__ == "__main__":
    # Test inizializzazione
    init_db()
    print(f"✅ Database creato: {DB_FILE}")
