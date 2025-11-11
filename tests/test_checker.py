#!/usr/bin/env python3
"""
Unit tests per checker.py
Verifica logica confronto tariffe con vari scenari
"""
import sys
from pathlib import Path

# Aggiungi parent directory al path per import
sys.path.insert(0, str(Path(__file__).parent.parent))

from checker import (
    _build_current_octopus_rates,
    _check_utility_rates,
    _compare_rate_field,
    _should_notify_user,
    check_better_rates,
)


def test_complete_match_no_savings():
    """Tariffe utente = tariffe Octopus → nessun risparmio"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.145, "commercializzazione": 72.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}}},
    }

    savings = check_better_rates(user_rates, current_rates)

    assert savings["has_savings"] is False
    assert savings["luce_energia"] is None
    assert savings["luce_comm"] is None
    assert savings["gas_energia"] is None
    assert savings["gas_comm"] is None


def test_luce_energy_savings():
    """Energia luce migliorata → risparmio"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}},
        "gas": {},
    }

    savings = check_better_rates(user_rates, current_rates)

    assert savings["has_savings"] is True
    assert savings["luce_energia"] is not None
    assert savings["luce_energia"]["attuale"] == 0.145
    assert savings["luce_energia"]["nuova"] == 0.130
    assert abs(savings["luce_energia"]["risparmio"] - 0.015) < 0.0001


def test_mixed_luce_better_worse():
    """Energia luce migliorata, commercializzazione peggiorata → mixed"""
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.010,
            "commercializzazione": 60.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {"variabile": {"monoraria": {"energia": 0.0088, "commercializzazione": 72.0}}},
        "gas": {},
    }

    savings = check_better_rates(user_rates, current_rates)

    assert savings["has_savings"] is True
    assert savings["luce_energia"] is not None
    assert savings["luce_comm_worse"] is True
    assert savings["is_mixed"] is True


def test_no_cross_type_comparison_fissa_vs_variabile():
    """Utente ha fissa, current_rates solo variabile → nessun confronto"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {
            "fissa": {},  # Vuoto
            "variabile": {"monoraria": {"energia": 0.0088, "commercializzazione": 72.0}},
        },
        "gas": {},
    }

    savings = check_better_rates(user_rates, current_rates)

    # Non deve confrontare fissa con variabile
    assert savings["has_savings"] is False
    assert savings["luce_energia"] is None


def test_no_cross_fascia_comparison_mono_vs_tri():
    """Utente ha monoraria, current_rates solo trioraria → nessun confronto"""
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.010,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {
            "variabile": {
                "monoraria": {},  # Vuoto
                "trioraria": {"energia": 0.0088, "commercializzazione": 72.0},
            }
        },
        "gas": {},
    }

    savings = check_better_rates(user_rates, current_rates)

    # Non deve confrontare monoraria con trioraria
    assert savings["has_savings"] is False
    assert savings["luce_energia"] is None


def test_user_with_gas_partial_current_rates():
    """Utente ha gas, current_rates ha solo luce → confronta solo luce"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}},
        "gas": {"fissa": {}, "variabile": {}},  # Gas non disponibile
    }

    savings = check_better_rates(user_rates, current_rates)

    # Deve trovare risparmio luce
    assert savings["has_savings"] is True
    assert savings["luce_energia"] is not None

    # Gas non confrontato (non disponibile)
    assert savings["gas_energia"] is None
    assert savings["gas_comm"] is None


def test_user_without_gas():
    """Utente senza gas → confronta solo luce"""
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.010,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {"variabile": {"trioraria": {"energia": 0.0088, "commercializzazione": 60.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}}},
    }

    savings = check_better_rates(user_rates, current_rates)

    # Deve trovare risparmio luce
    assert savings["has_savings"] is True
    assert savings["luce_energia"] is not None
    assert savings["luce_comm"] is not None

    # Gas non deve essere confrontato
    assert savings["gas_energia"] is None
    assert savings["gas_comm"] is None


def test_empty_current_rates():
    """current_rates completamente vuoto → nessun confronto"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {"luce": {"fissa": {}, "variabile": {}}, "gas": {"fissa": {}, "variabile": {}}}

    savings = check_better_rates(user_rates, current_rates)

    # Nessun risparmio trovato (niente da confrontare)
    assert savings["has_savings"] is False
    assert savings["luce_energia"] is None
    assert savings["luce_comm"] is None


def test_both_luce_and_gas_savings():
    """Risparmio sia su luce che su gas"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.10,
            "commercializzazione": 90.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 60.0}}},
        "gas": {"variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 84.0}}},
    }

    savings = check_better_rates(user_rates, current_rates)

    # Risparmio su tutto
    assert savings["has_savings"] is True
    assert savings["luce_energia"] is not None
    assert savings["luce_comm"] is not None
    assert savings["gas_energia"] is not None
    assert savings["gas_comm"] is not None


def test_tipo_and_fascia_in_savings():
    """Verifica che savings contenga tipo e fascia corretti"""
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.010,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        },
    }

    current_rates = {
        "luce": {"variabile": {"trioraria": {"energia": 0.0088, "commercializzazione": 72.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.456, "commercializzazione": 84.0}}},
    }

    savings = check_better_rates(user_rates, current_rates)

    # Verifica tipo e fascia memorizzati correttamente
    assert savings["luce_tipo"] == "variabile"
    assert savings["luce_fascia"] == "trioraria"
    assert savings["gas_tipo"] == "fissa"
    assert savings["gas_fascia"] == "monoraria"


def test_user_with_gas_no_notifications():
    """Utente con gas, nessuna notifica precedente"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        },
    }

    # Verifica struttura base
    assert "luce" in user_data
    assert "gas" in user_data
    assert user_data["gas"] is not None

    # Verifica campi luce
    assert set(user_data["luce"].keys()) == {"tipo", "fascia", "energia", "commercializzazione"}

    # Verifica campi gas
    assert set(user_data["gas"].keys()) == {"tipo", "fascia", "energia", "commercializzazione"}


def test_user_without_gas_no_notifications():
    """Utente senza gas, nessuna notifica precedente"""
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.0088,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    # Verifica struttura
    assert "luce" in user_data
    assert "gas" in user_data
    assert user_data["gas"] is None


def test_user_with_last_notified_rates():
    """Utente con notifiche precedenti"""
    user_data = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.010,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.10,
            "commercializzazione": 84.0,
        },
        "last_notified_rates": {
            "luce": {"energia": 0.0088, "commercializzazione": 72.0},
            "gas": {"energia": 0.08, "commercializzazione": 84.0},
        },
    }

    # Verifica struttura last_notified_rates
    assert "last_notified_rates" in user_data
    assert "luce" in user_data["last_notified_rates"]
    assert "gas" in user_data["last_notified_rates"]

    # Verifica che last_notified non contenga tipo/fascia (ridondanti)
    assert "tipo" not in user_data["last_notified_rates"]["luce"]
    assert "fascia" not in user_data["last_notified_rates"]["luce"]

    # Verifica campi corretti
    assert set(user_data["last_notified_rates"]["luce"].keys()) == {
        "energia",
        "commercializzazione",
    }


def test_user_without_gas_with_last_notified():
    """Utente senza gas ma con notifiche precedenti (solo luce)"""
    user_data = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
        "last_notified_rates": {"luce": {"energia": 0.130, "commercializzazione": 60.0}},
    }

    # last_notified_rates può avere solo luce
    assert "last_notified_rates" in user_data
    assert "luce" in user_data["last_notified_rates"]
    assert "gas" not in user_data["last_notified_rates"]


# ========== TEST HELPER FUNCTIONS ==========


def test_compare_rate_field_improvement():
    """_compare_rate_field: nuova tariffa migliore"""
    saving, is_worse = _compare_rate_field(0.145, 0.130)

    assert saving is not None
    assert saving["attuale"] == 0.145
    assert saving["nuova"] == 0.130
    assert abs(saving["risparmio"] - 0.015) < 0.0001
    assert is_worse is False


def test_compare_rate_field_worsening():
    """_compare_rate_field: nuova tariffa peggiore"""
    saving, is_worse = _compare_rate_field(0.130, 0.145)

    assert saving is None
    assert is_worse is True


def test_compare_rate_field_no_change():
    """_compare_rate_field: nessun cambiamento"""
    saving, is_worse = _compare_rate_field(0.145, 0.145)

    assert saving is None
    assert is_worse is False


def test_compare_rate_field_none_value():
    """_compare_rate_field: current_value è None"""
    saving, is_worse = _compare_rate_field(0.145, None)

    assert saving is None
    assert is_worse is False


def test_check_utility_rates_with_savings():
    """_check_utility_rates: trova risparmi su energia e commercializzazione"""
    user_utility = {
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.145,
        "commercializzazione": 72.0,
    }

    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.130,
                    "commercializzazione": 60.0,
                }
            }
        }
    }

    result = _check_utility_rates(user_utility, current_rates, "luce")

    assert result["has_savings"] is True
    assert result["energia_saving"] is not None
    assert result["comm_saving"] is not None
    assert result["energia_worse"] is False
    assert result["comm_worse"] is False


def test_check_utility_rates_no_rate_available():
    """_check_utility_rates: tariffa non disponibile"""
    user_utility = {
        "tipo": "fissa",
        "fascia": "monoraria",
        "energia": 0.145,
        "commercializzazione": 72.0,
    }

    current_rates = {"luce": {"fissa": {}}}  # Nessuna monoraria

    result = _check_utility_rates(user_utility, current_rates, "luce")

    assert result["has_savings"] is False
    assert result["energia_saving"] is None
    assert result["comm_saving"] is None


def test_build_current_octopus_rates_with_luce_only():
    """_build_current_octopus_rates: solo luce"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 60.0}}},
        "gas": {},
    }

    result = _build_current_octopus_rates(user_rates, current_rates)

    assert "luce" in result
    assert result["luce"]["energia"] == 0.130
    assert result["luce"]["commercializzazione"] == 60.0
    assert "gas" not in result


def test_build_current_octopus_rates_with_luce_and_gas():
    """_build_current_octopus_rates: luce e gas"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        },
        "gas": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.10,
            "commercializzazione": 84.0,
        },
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 60.0}}},
        "gas": {"variabile": {"monoraria": {"energia": 0.08, "commercializzazione": 78.0}}},
    }

    result = _build_current_octopus_rates(user_rates, current_rates)

    assert "luce" in result
    assert "gas" in result
    assert result["luce"]["energia"] == 0.130
    assert result["gas"]["energia"] == 0.08


def test_should_notify_user_first_notification():
    """_should_notify_user: prima notifica (no last_notified_rates)"""
    user_rates = {
        "luce": {"tipo": "fissa", "fascia": "monoraria"},
        "gas": None,
    }

    current_octopus = {"luce": {"energia": 0.130, "commercializzazione": 60.0}}

    should_notify = _should_notify_user(user_rates, current_octopus)

    assert should_notify is True


def test_should_notify_user_rates_changed():
    """_should_notify_user: tariffe cambiate rispetto all'ultima notifica"""
    user_rates = {
        "luce": {"tipo": "fissa", "fascia": "monoraria"},
        "gas": None,
        "last_notified_rates": {"luce": {"energia": 0.135, "commercializzazione": 65.0}},
    }

    current_octopus = {"luce": {"energia": 0.130, "commercializzazione": 60.0}}

    should_notify = _should_notify_user(user_rates, current_octopus)

    assert should_notify is True


def test_should_notify_user_already_notified():
    """_should_notify_user: già notificato con stesse tariffe"""
    user_rates = {
        "luce": {"tipo": "fissa", "fascia": "monoraria"},
        "gas": None,
        "last_notified_rates": {"luce": {"energia": 0.130, "commercializzazione": 60.0}},
    }

    current_octopus = {"luce": {"energia": 0.130, "commercializzazione": 60.0}}

    should_notify = _should_notify_user(user_rates, current_octopus)

    assert should_notify is False
