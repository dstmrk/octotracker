"""
Test di integrazione E2E per OctoTracker

Questi test verificano flussi completi dell'applicazione,
incluse interazioni reali con il database SQLite.
"""

import tempfile
from pathlib import Path

import pytest

import database
from database import (
    get_user_count,
    init_db,
    load_user,
    load_users,
    remove_user,
    save_user,
    user_exists,
)


@pytest.fixture
def temp_db():
    """
    Fixture che crea un database temporaneo isolato per ogni test.

    Questo permette di testare interazioni reali con SQLite
    senza interferire con il database di produzione.
    """
    with tempfile.TemporaryDirectory() as tmpdir:
        # Salva i path originali
        original_data_dir = database.DATA_DIR
        original_db_file = database.DB_FILE

        # Patch con path temporanei
        database.DATA_DIR = Path(tmpdir)
        database.DB_FILE = Path(tmpdir) / "test_users.db"

        # Inizializza database temporaneo
        init_db()

        # Fornisce il database al test
        yield database.DB_FILE

        # Cleanup: ripristina path originali
        database.DATA_DIR = original_data_dir
        database.DB_FILE = original_db_file


def test_user_registration_flow(temp_db):
    """
    Test flusso completo registrazione utente: save → load → verify
    """
    user_id = "123456789"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    # Step 1: Salva nuovo utente
    result = save_user(user_id, user_data)
    assert result is True, "save_user dovrebbe restituire True per nuovo utente"

    # Step 2: Verifica esistenza
    assert user_exists(user_id) is True, "user_exists dovrebbe restituire True"

    # Step 3: Carica utente e verifica dati
    loaded = load_user(user_id)
    assert loaded is not None, "load_user dovrebbe restituire dati"
    assert loaded["luce"]["tipo"] == "fissa"
    assert loaded["luce"]["fascia"] == "monoraria"
    assert loaded["luce"]["energia"] == 0.145
    assert loaded["luce"]["commercializzazione"] == 72.0
    assert "gas" not in loaded, "gas non dovrebbe essere presente quando è None"

    # Step 4: Verifica conteggio utenti
    assert get_user_count() == 1, "Dovrebbe esserci 1 utente"


def test_user_registration_with_gas_flow(temp_db):
    """
    Test flusso registrazione utente con luce E gas
    """
    user_id = "987654321"
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.012,
            "commercializzazione": 96.0,
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.025,
            "commercializzazione": 120.0,
        },
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica luce
    assert loaded["luce"]["tipo"] == "variabile"
    assert loaded["luce"]["fascia"] == "trioraria"
    assert loaded["luce"]["energia"] == 0.012

    # Verifica gas
    assert loaded["gas"] is not None
    assert loaded["gas"]["tipo"] == "variabile"
    assert loaded["gas"]["energia"] == 0.025
    assert loaded["gas"]["commercializzazione"] == 120.0


def test_user_update_flow(temp_db):
    """
    Test flusso aggiornamento dati utente esistente
    """
    user_id = "111222333"

    # Step 1: Registrazione iniziale (solo luce)
    initial_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.150,
            "commercializzazione": 60.0,
        },
        "gas": None,
    }
    assert save_user(user_id, initial_data) is True

    # Step 2: Aggiornamento tariffe (aggiungo gas)
    updated_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.010,
            "commercializzazione": 96.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.350,
            "commercializzazione": 100.0,
        },
    }
    assert save_user(user_id, updated_data) is True

    # Step 3: Verifica che i dati siano aggiornati
    loaded = load_user(user_id)
    assert loaded["luce"]["tipo"] == "variabile"
    assert loaded["luce"]["energia"] == 0.010
    assert loaded["gas"] is not None
    assert loaded["gas"]["energia"] == 0.350

    # Step 4: Verifica che ci sia ancora solo 1 utente (non duplicato)
    assert get_user_count() == 1


def test_user_removal_flow(temp_db):
    """
    Test flusso completo rimozione utente: save → remove → verify deletion
    """
    user_id = "444555666"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.140,
            "commercializzazione": 80.0,
        },
        "gas": None,
    }

    # Step 1: Salva utente
    assert save_user(user_id, user_data) is True
    assert user_exists(user_id) is True

    # Step 2: Rimuovi utente
    result = remove_user(user_id)
    assert result is True, "remove_user dovrebbe restituire True"

    # Step 3: Verifica rimozione
    assert user_exists(user_id) is False, "Utente non dovrebbe più esistere"
    assert load_user(user_id) is None, "load_user dovrebbe restituire None"
    assert get_user_count() == 0, "Non dovrebbero esserci utenti"

    # Step 4: Tentativo rimozione utente inesistente
    result2 = remove_user(user_id)
    assert result2 is False, "Rimozione utente inesistente dovrebbe restituire False"


def test_multiple_users_flow(temp_db):
    """
    Test gestione multipli utenti: save, load_users, count
    """
    # Registra 3 utenti
    users = {
        "user1": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.140,
                "commercializzazione": 70.0,
            },
            "gas": None,
        },
        "user2": {
            "luce": {
                "tipo": "variabile",
                "fascia": "trioraria",
                "energia": 0.015,
                "commercializzazione": 90.0,
            },
            "gas": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.300,
                "commercializzazione": 110.0,
            },
        },
        "user3": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.130,
                "commercializzazione": 65.0,
            },
            "gas": None,
        },
    }

    # Salva tutti gli utenti
    for user_id, user_data in users.items():
        assert save_user(user_id, user_data) is True

    # Verifica conteggio
    assert get_user_count() == 3, "Dovrebbero esserci 3 utenti"

    # Carica tutti gli utenti
    loaded_users = load_users()
    assert len(loaded_users) == 3, "load_users dovrebbe restituire 3 utenti"
    assert "user1" in loaded_users
    assert "user2" in loaded_users
    assert "user3" in loaded_users

    # Verifica dati specifici
    assert loaded_users["user1"]["luce"]["energia"] == 0.140
    assert loaded_users["user2"]["gas"]["energia"] == 0.300
    assert loaded_users["user3"]["luce"]["commercializzazione"] == 65.0


def test_validation_invalid_tipo(temp_db):
    """
    Test validazione: tipo non valido dovrebbe fallire
    """
    user_id = "999"
    user_data = {
        "luce": {
            "tipo": "invalido",  # Tipo non valido!
            "fascia": "monoraria",
            "energia": 0.140,
            "commercializzazione": 70.0,
        },
        "gas": None,
    }

    # Dovrebbe restituire False (validazione fallita)
    result = save_user(user_id, user_data)
    assert result is False, "save_user dovrebbe fallire con tipo invalido"

    # Utente non dovrebbe essere salvato
    assert user_exists(user_id) is False


def test_validation_invalid_fascia_luce(temp_db):
    """
    Test validazione: fascia luce non valida dovrebbe fallire
    """
    user_id = "888"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "quadrioraria",  # Fascia non valida!
            "energia": 0.140,
            "commercializzazione": 70.0,
        },
        "gas": None,
    }

    result = save_user(user_id, user_data)
    assert result is False, "save_user dovrebbe fallire con fascia invalida"
    assert user_exists(user_id) is False


def test_validation_invalid_fascia_gas(temp_db):
    """
    Test validazione: fascia gas non valida dovrebbe fallire
    """
    user_id = "777"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.140,
            "commercializzazione": 70.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "bioraria",  # Gas non supporta bioraria!
            "energia": 0.300,
            "commercializzazione": 100.0,
        },
    }

    result = save_user(user_id, user_data)
    assert result is False, "save_user dovrebbe fallire con fascia gas invalida"
    assert user_exists(user_id) is False


def test_validation_missing_field(temp_db):
    """
    Test validazione: campo mancante dovrebbe fallire
    """
    user_id = "666"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            # Manca 'energia'!
            "commercializzazione": 70.0,
        },
        "gas": None,
    }

    result = save_user(user_id, user_data)
    assert result is False, "save_user dovrebbe fallire con campo mancante"
    assert user_exists(user_id) is False


def test_last_notified_rates_persistence(temp_db):
    """
    Test persistenza campo last_notified_rates (usato dal checker)
    """
    user_id = "555444333"
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.010,
            "commercializzazione": 96.0,
        },
        "gas": None,
        "last_notified_rates": {
            "luce_energia": 0.120,
            "luce_comm": 72.0,
            "timestamp": "2024-01-15T10:00:00",
        },
    }

    # Salva con last_notified_rates
    assert save_user(user_id, user_data) is True

    # Carica e verifica persistenza
    loaded = load_user(user_id)
    assert loaded["last_notified_rates"] is not None
    assert loaded["last_notified_rates"]["luce_energia"] == 0.120
    assert loaded["last_notified_rates"]["timestamp"] == "2024-01-15T10:00:00"


def test_database_isolation(temp_db):
    """
    Test che il database temporaneo sia isolato dal database di produzione
    """
    # Verifica che il database sia vuoto all'inizio
    assert get_user_count() == 0, "Database temporaneo dovrebbe iniziare vuoto"

    # Salva un utente
    save_user(
        "test_isolation",
        {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.150,
                "commercializzazione": 80.0,
            },
            "gas": None,
        },
    )

    # Verifica che sia nel database temporaneo
    assert get_user_count() == 1

    # Verifica che il file DB sia nella directory temporanea
    assert str(database.DB_FILE).startswith("/tmp/") or str(database.DB_FILE).startswith(
        tempfile.gettempdir()
    )


def test_database_connection_error_handling(temp_db):
    """
    Test gestione errori connessione database
    """
    # Salva path originale
    original_db = database.DB_FILE

    # Punta a un path non valido (directory senza permessi)
    database.DB_FILE = Path("/root/invalid/path/db.sqlite")

    # load_users dovrebbe gestire l'errore e restituire dict vuoto
    users = load_users()
    assert users == {}, "load_users dovrebbe restituire {} in caso di errore"

    # Ripristina
    database.DB_FILE = original_db


def test_user_consumption_monoraria(temp_db):
    """
    Test salvataggio e caricamento consumi luce monoraria
    """
    user_id = "consumption_mono_001"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
        },
        "gas": None,
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica tariffe
    assert loaded["luce"]["tipo"] == "fissa"
    assert loaded["luce"]["fascia"] == "monoraria"

    # Verifica consumi
    assert loaded["luce"]["consumo_f1"] == 2700.0
    assert "consumo_f2" not in loaded["luce"]
    assert "consumo_f3" not in loaded["luce"]


def test_user_consumption_bioraria(temp_db):
    """
    Test salvataggio e caricamento consumi luce bioraria
    """
    user_id = "consumption_bi_002"
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "bioraria",
            "energia": 0.015,
            "commercializzazione": 80.0,
            "consumo_f1": 1200.0,
            "consumo_f2": 1500.0,
        },
        "gas": None,
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica tariffe
    assert loaded["luce"]["tipo"] == "variabile"
    assert loaded["luce"]["fascia"] == "bioraria"

    # Verifica consumi
    assert loaded["luce"]["consumo_f1"] == 1200.0
    assert loaded["luce"]["consumo_f2"] == 1500.0
    assert "consumo_f3" not in loaded["luce"]


def test_user_consumption_trioraria(temp_db):
    """
    Test salvataggio e caricamento consumi luce trioraria
    """
    user_id = "consumption_tri_003"
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.012,
            "commercializzazione": 96.0,
            "consumo_f1": 900.0,
            "consumo_f2": 900.0,
            "consumo_f3": 900.0,
        },
        "gas": None,
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica tariffe
    assert loaded["luce"]["tipo"] == "variabile"
    assert loaded["luce"]["fascia"] == "trioraria"

    # Verifica consumi
    assert loaded["luce"]["consumo_f1"] == 900.0
    assert loaded["luce"]["consumo_f2"] == 900.0
    assert loaded["luce"]["consumo_f3"] == 900.0


def test_user_consumption_gas(temp_db):
    """
    Test salvataggio e caricamento consumo gas
    """
    user_id = "consumption_gas_004"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.140,
            "commercializzazione": 70.0,
            "consumo_f1": 2500.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.350,
            "commercializzazione": 120.0,
            "consumo_annuo": 1200.0,
        },
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica consumi luce
    assert loaded["luce"]["consumo_f1"] == 2500.0

    # Verifica consumo gas
    assert loaded["gas"] is not None
    assert loaded["gas"]["consumo_annuo"] == 1200.0


def test_backward_compatibility_no_consumption(temp_db):
    """
    Test retrocompatibilità: utenti senza consumi (NULL nel DB)
    Simula utenti creati prima dell'introduzione dei consumi
    """
    user_id = "old_user_005"
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            # Nessun campo consumo
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.025,
            "commercializzazione": 100.0,
            # Nessun campo consumo
        },
    }

    # Salva e carica
    assert save_user(user_id, user_data) is True
    loaded = load_user(user_id)

    # Verifica che le tariffe siano salvate correttamente
    assert loaded["luce"]["tipo"] == "fissa"
    assert loaded["gas"]["tipo"] == "variabile"

    # Verifica che i campi consumo NON siano presenti (NULL nel DB)
    assert "consumo_f1" not in loaded["luce"]
    assert "consumo_f2" not in loaded["luce"]
    assert "consumo_f3" not in loaded["luce"]
    assert "consumo_annuo" not in loaded["gas"]


def test_update_add_consumption(temp_db):
    """
    Test aggiunta consumi a utente esistente senza consumi
    Simula utente vecchio che aggiorna i dati aggiungendo i consumi
    """
    user_id = "update_consumption_006"

    # Step 1: Registrazione iniziale SENZA consumi (utente vecchio)
    initial_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.150,
            "commercializzazione": 60.0,
        },
        "gas": None,
    }
    assert save_user(user_id, initial_data) is True

    # Verifica che non ci siano consumi
    loaded = load_user(user_id)
    assert "consumo_f1" not in loaded["luce"]

    # Step 2: Aggiornamento CON consumi
    updated_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.150,
            "commercializzazione": 60.0,
            "consumo_f1": 3000.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.380,
            "commercializzazione": 110.0,
            "consumo_annuo": 1400.0,
        },
    }
    assert save_user(user_id, updated_data) is True

    # Step 3: Verifica che i consumi siano stati aggiunti
    loaded = load_user(user_id)
    assert loaded["luce"]["consumo_f1"] == 3000.0
    assert loaded["gas"]["consumo_annuo"] == 1400.0

    # Step 4: Verifica che ci sia ancora solo 1 utente (update, non insert)
    assert get_user_count() == 1
