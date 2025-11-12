#!/usr/bin/env python3
"""
Unit tests per checker.py
Verifica logica confronto tariffe con vari scenari
"""
import sys
from pathlib import Path

import pytest

# Aggiungi parent directory al path per import
sys.path.insert(0, str(Path(__file__).parent.parent))

from checker import (
    _build_current_octopus_rates,
    _calculate_utility_savings,
    _check_utility_rates,
    _compare_rate_field,
    _format_footer,
    _should_notify_user,
    check_and_notify_users,
    check_better_rates,
    send_notification,
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


# ========== TESTS FOR FORMATTING FUNCTIONS ==========


def test_format_number_integer():
    """format_number con numero intero"""
    from checker import format_number

    result = format_number(72.0, max_decimals=2)
    assert result == "72"


def test_format_number_with_decimals():
    """format_number con decimali"""
    from checker import format_number

    result = format_number(0.1078, max_decimals=4)
    assert result == "0,1078"


def test_format_number_trailing_zeros():
    """format_number rimuove zeri trailing oltre il secondo decimale"""
    from checker import format_number

    result = format_number(0.1000, max_decimals=4)
    assert result == "0,10"


def test_format_number_two_decimals_min():
    """format_number mantiene almeno 2 decimali"""
    from checker import format_number

    result = format_number(0.5, max_decimals=4)
    assert result == "0,50"


def test_load_json_success():
    """load_json carica file JSON valido"""
    from pathlib import Path
    from unittest.mock import mock_open, patch

    from checker import load_json

    test_data = '{"luce": {"fissa": {"monoraria": {"energia": 0.1078}}}}'

    with patch("builtins.open", mock_open(read_data=test_data)):
        with patch.object(Path, "exists", return_value=True):
            result = load_json(Path("/tmp/test.json"))

    assert result is not None
    assert "luce" in result


def test_load_json_empty_file():
    """load_json con file vuoto"""
    from pathlib import Path
    from unittest.mock import mock_open, patch

    from checker import load_json

    with patch("builtins.open", mock_open(read_data="")):
        with patch.object(Path, "exists", return_value=True):
            result = load_json(Path("/tmp/test.json"))

    assert result is None


def test_load_json_file_not_found():
    """load_json con file non trovato"""
    from pathlib import Path

    from checker import load_json

    result = load_json(Path("/tmp/nonexistent.json"))

    assert result is None


def test_load_json_invalid_json():
    """load_json con JSON non valido"""
    from pathlib import Path
    from unittest.mock import mock_open, patch

    from checker import load_json

    with patch("builtins.open", mock_open(read_data="invalid json")):
        with patch.object(Path, "exists", return_value=True):
            result = load_json(Path("/tmp/test.json"))

    assert result is None


def test_format_header_mixed():
    """_format_header con caso mixed"""
    from checker import _format_header

    result = _format_header(is_mixed=True)

    assert "⚖️" in result
    assert "Aggiornamento tariffe" in result


def test_format_header_savings():
    """_format_header con risparmi"""
    from checker import _format_header

    result = _format_header(is_mixed=False)

    assert "⚡️" in result
    assert "Buone notizie" in result


def test_format_luce_section_with_savings():
    """_format_luce_section con risparmi"""
    from checker import _format_luce_section

    savings = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": {"attuale": 0.145, "nuova": 0.130, "risparmio": 0.015},
        "luce_comm": None,
        "luce_energia_worse": False,
        "luce_comm_worse": False,
    }

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
        }
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}}
    }

    result = _format_luce_section(savings, user_rates, current_rates)

    assert "💡" in result
    assert "Luce" in result
    assert "0,145" in result


def test_format_luce_section_no_savings():
    """_format_luce_section senza risparmi"""
    from checker import _format_luce_section

    savings = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": None,
        "luce_comm": None,
    }

    user_rates = {"luce": {"tipo": "fissa", "fascia": "monoraria"}}
    current_rates = {}

    result = _format_luce_section(savings, user_rates, current_rates)

    assert result == ""


def test_format_gas_section_with_savings():
    """_format_gas_section con risparmi"""
    from checker import _format_gas_section

    savings = {
        "gas_tipo": "fissa",
        "gas_fascia": "monoraria",
        "gas_energia": {"attuale": 0.456, "nuova": 0.400, "risparmio": 0.056},
        "gas_comm": None,
        "gas_energia_worse": False,
        "gas_comm_worse": False,
    }

    user_rates = {
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
        }
    }

    current_rates = {
        "gas": {"fissa": {"monoraria": {"energia": 0.400, "commercializzazione": 84.0}}}
    }

    result = _format_gas_section(savings, user_rates, current_rates)

    assert "🔥" in result
    assert "Gas" in result
    assert "0,456" in result


def test_format_gas_section_no_gas():
    """_format_gas_section quando utente non ha gas"""
    from checker import _format_gas_section

    savings = {"gas_tipo": None, "gas_fascia": None, "gas_energia": None, "gas_comm": None}

    user_rates = {"gas": None}
    current_rates = {}

    result = _format_gas_section(savings, user_rates, current_rates)

    assert result == ""


def test_format_footer_mixed():
    """_format_footer con caso mixed"""
    from checker import _format_footer

    result = _format_footer(
        luce_is_mixed=True,
        gas_is_mixed=False,
        luce_estimated_savings=None,
        gas_estimated_savings=None,
        show_luce=True,
        show_gas=False,
    )

    assert "📊" in result
    assert "convenienza dipende" in result


def test_format_footer_savings():
    """_format_footer con risparmi"""
    from checker import _format_footer

    result = _format_footer(
        luce_is_mixed=False,
        gas_is_mixed=False,
        luce_estimated_savings=None,
        gas_estimated_savings=None,
        show_luce=True,
        show_gas=True,
    )

    assert "🔧" in result
    assert "/update" in result


def test_format_notification():
    """format_notification costruisce messaggio completo"""
    from checker import format_notification

    savings = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": {"attuale": 0.145, "nuova": 0.130, "risparmio": 0.015},
        "luce_comm": None,
        "gas_tipo": None,
        "gas_fascia": None,
        "gas_energia": None,
        "gas_comm": None,
        "luce_energia_worse": False,
        "luce_comm_worse": False,
        "gas_energia_worse": False,
        "gas_comm_worse": False,
        "is_mixed": False,
        "luce_is_mixed": False,
        "gas_is_mixed": False,
    }

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
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}}
    }

    result = format_notification(savings, user_rates, current_rates)

    assert "⚡️" in result
    assert "💡" in result
    assert "ko-fi.com" in result


# ========== TESTS FOR ASYNC FUNCTIONS ==========


@pytest.mark.asyncio
async def test_send_notification_success():
    """send_notification invia messaggio con successo"""
    from unittest.mock import AsyncMock, MagicMock

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock()

    result = await send_notification(bot_mock, "123456", "Test message")

    assert result is True
    bot_mock.send_message.assert_called_once_with(
        chat_id="123456", text="Test message", parse_mode="HTML"
    )


@pytest.mark.asyncio
async def test_send_notification_retry_after():
    """send_notification con rate limit (RetryAfter)"""
    from unittest.mock import AsyncMock, MagicMock

    from telegram.error import RetryAfter

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=RetryAfter(10))

    result = await send_notification(bot_mock, "123456", "Test message")

    assert result is False


@pytest.mark.asyncio
async def test_send_notification_timeout():
    """send_notification con timeout"""
    from unittest.mock import AsyncMock, MagicMock

    from telegram.error import TimedOut

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TimedOut())

    result = await send_notification(bot_mock, "123456", "Test message")

    assert result is False


@pytest.mark.asyncio
async def test_send_notification_network_error():
    """send_notification con errore di rete"""
    from unittest.mock import AsyncMock, MagicMock

    from telegram.error import NetworkError

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=NetworkError("Network error"))

    result = await send_notification(bot_mock, "123456", "Test message")

    assert result is False


@pytest.mark.asyncio
async def test_send_notification_telegram_error():
    """send_notification con errore generico Telegram"""
    from unittest.mock import AsyncMock, MagicMock

    from telegram.error import TelegramError

    bot_mock = MagicMock()
    bot_mock.send_message = AsyncMock(side_effect=TelegramError("Generic error"))

    result = await send_notification(bot_mock, "123456", "Test message")

    assert result is False


@pytest.mark.asyncio
async def test_check_and_notify_users_no_users():
    """check_and_notify_users senza utenti registrati"""
    from unittest.mock import patch

    with patch("checker.load_users", return_value={}):
        with patch("checker.load_json", return_value={"luce": {}, "gas": {}}):
            # Non dovrebbe generare errori
            await check_and_notify_users("fake_token")


@pytest.mark.asyncio
async def test_check_and_notify_users_no_rates():
    """check_and_notify_users senza tariffe disponibili"""
    from unittest.mock import patch

    users = {"123": {"luce": {"tipo": "fissa", "fascia": "monoraria"}}}

    with patch("checker.load_users", return_value=users):
        with patch("checker.load_json", return_value=None):
            # Non dovrebbe generare errori
            await check_and_notify_users("fake_token")


@pytest.mark.asyncio
async def test_check_and_notify_users_with_savings():
    """check_and_notify_users trova risparmi e invia notifica"""
    from unittest.mock import AsyncMock, MagicMock, patch

    users = {
        "123": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,
                "commercializzazione": 72.0,
            },
            "gas": None,
        }
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}},
        "gas": {},
    }

    with patch("checker.load_users", return_value=users):
        with patch("checker.load_json", return_value=current_rates):
            with patch("checker.Bot") as mock_bot_class:
                mock_bot = MagicMock()
                mock_bot.send_message = AsyncMock()
                mock_bot_class.return_value = mock_bot

                with patch("checker.save_user") as mock_save:
                    await check_and_notify_users("fake_token")

                    # Verifica che il messaggio sia stato inviato
                    mock_bot.send_message.assert_called_once()
                    # Verifica che l'utente sia stato aggiornato
                    mock_save.assert_called_once()


@pytest.mark.asyncio
async def test_check_and_notify_users_already_notified():
    """check_and_notify_users salta notifica se già inviata"""
    from unittest.mock import AsyncMock, MagicMock, patch

    users = {
        "123": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,
                "commercializzazione": 72.0,
            },
            "gas": None,
            "last_notified_rates": {"luce": {"energia": 0.130, "commercializzazione": 72.0}},
        }
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 72.0}}},
        "gas": {},
    }

    with patch("checker.load_users", return_value=users):
        with patch("checker.load_json", return_value=current_rates):
            with patch("checker.Bot") as mock_bot_class:
                mock_bot = MagicMock()
                mock_bot.send_message = AsyncMock()
                mock_bot_class.return_value = mock_bot

                await check_and_notify_users("fake_token")

                # Verifica che nessun messaggio sia stato inviato
                mock_bot.send_message.assert_not_called()


def test_format_luce_section_worse():
    """_format_luce_section con peggioramento"""
    from checker import _format_luce_section

    savings = {
        "luce_tipo": "fissa",
        "luce_fascia": "monoraria",
        "luce_energia": None,
        "luce_comm": {"attuale": 60.0, "nuova": 72.0, "risparmio": -12.0},
        "luce_energia_worse": True,
        "luce_comm_worse": False,
    }

    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 60.0,
        }
    }

    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.160, "commercializzazione": 72.0}}}
    }

    result = _format_luce_section(savings, user_rates, current_rates)

    # Dovrebbe contenere markup di sottolineatura per peggioramento
    assert "<u>" in result or result == ""


def test_load_json_permission_error():
    """load_json con errore di permesso"""
    from pathlib import Path
    from unittest.mock import mock_open, patch

    from checker import load_json

    with patch("builtins.open", mock_open()) as mock_file:
        mock_file.side_effect = PermissionError("Permission denied")
        with patch.object(Path, "exists", return_value=True):
            result = load_json(Path("/tmp/test.json"))

    assert result is None


def test_load_json_os_error():
    """load_json con errore OS generico"""
    from pathlib import Path
    from unittest.mock import mock_open, patch

    from checker import load_json

    with patch("builtins.open", mock_open()) as mock_file:
        mock_file.side_effect = OSError("OS error")
        with patch.object(Path, "exists", return_value=True):
            result = load_json(Path("/tmp/test.json"))

    assert result is None


# ========== TEST CONSUMPTION-BASED SAVINGS CALCULATION ==========


def test_calculate_estimated_savings_monoraria_luce_only():
    """Test calcolo risparmio con consumi luce monoraria"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,  # kWh/anno
        },
        "gas": None,
    }
    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.130,  # Risparmio 0.015 €/kWh
                    "commercializzazione": 65.0,  # Risparmio 7 €/anno
                }
            }
        }
    }

    risparmio = _calculate_utility_savings("luce", user_rates, current_rates)

    # Risparmio = (0.145 - 0.130) * 2700 + (72 - 65) = 40.5 + 7 = 47.5
    assert risparmio is not None
    assert risparmio == pytest.approx(47.5, abs=0.1)


def test_calculate_estimated_savings_trioraria():
    """Test calcolo risparmio con consumi luce trioraria"""
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.025,
            "commercializzazione": 72.0,
            "consumo_f1": 900.0,
            "consumo_f2": 850.0,
            "consumo_f3": 950.0,
        },
        "gas": None,
    }
    current_rates = {
        "luce": {
            "variabile": {
                "trioraria": {
                    "energia": 0.020,  # Risparmio 0.005 €/kWh
                    "commercializzazione": 85.0,  # Aumento 13 €/anno
                }
            }
        }
    }

    risparmio = _calculate_utility_savings("luce", user_rates, current_rates)

    # Risparmio energia = (0.025 - 0.020) * (900 + 850 + 950) = 0.005 * 2700 = 13.5
    # Aumento comm = 72 - 85 = -13
    # Totale = 13.5 - 13 = 0.5
    assert risparmio is not None
    assert risparmio == pytest.approx(0.5, abs=0.1)


def test_calculate_estimated_savings_with_gas():
    """Test calcolo risparmio con luce e gas (separato per utility)"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.145,
            "commercializzazione": 72.0,
            "consumo_f1": 2700.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.456,
            "commercializzazione": 84.0,
            "consumo_annuo": 1200.0,
        },
    }
    current_rates = {
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}},
        "gas": {"fissa": {"monoraria": {"energia": 0.420, "commercializzazione": 80.0}}},
    }

    # Calcola separatamente per luce e gas
    risparmio_luce = _calculate_utility_savings("luce", user_rates, current_rates)
    risparmio_gas = _calculate_utility_savings("gas", user_rates, current_rates)

    # Luce: (0.145-0.130)*2700 + (72-65) = 40.5 + 7 = 47.5
    assert risparmio_luce is not None
    assert risparmio_luce == pytest.approx(47.5, abs=0.1)

    # Gas: (0.456-0.420)*1200 + (84-80) = 43.2 + 4 = 47.2
    assert risparmio_gas is not None
    assert risparmio_gas == pytest.approx(47.2, abs=0.1)


def test_calculate_estimated_savings_negative():
    """Test calcolo con risparmio negativo (aumento costo)"""
    user_rates = {
        "luce": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.130,
            "commercializzazione": 65.0,
            "consumo_f1": 2700.0,
        },
        "gas": None,
    }
    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.145,  # Peggioramento
                    "commercializzazione": 72.0,  # Peggioramento
                }
            }
        }
    }

    risparmio = _calculate_utility_savings("luce", user_rates, current_rates)

    # Risparmio = (0.130-0.145)*2700 + (65-72) = -40.5 - 7 = -47.5
    assert risparmio is not None
    assert risparmio == pytest.approx(-47.5, abs=0.1)


def test_calculate_estimated_savings_no_consumption():
    """Test calcolo senza consumi → None"""
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
        "luce": {"fissa": {"monoraria": {"energia": 0.130, "commercializzazione": 65.0}}}
    }

    risparmio = _calculate_utility_savings("luce", user_rates, current_rates)

    assert risparmio is None


# ========== TEST FOOTER WITH CONSUMPTION ==========


def test_format_footer_mixed_without_consumption():
    """Test footer MIXED senza consumi"""
    footer = _format_footer(
        luce_is_mixed=True,
        gas_is_mixed=False,
        luce_estimated_savings=None,
        gas_estimated_savings=None,
        show_luce=True,
        show_gas=False,
    )

    assert "📊 In questi casi la convenienza dipende dai tuoi consumi" in footer
    assert "Se vuoi una stima più precisa" in footer
    assert "/update" in footer


def test_format_footer_mixed_with_consumption():
    """Test footer MIXED con consumi e risparmio positivo"""
    footer = _format_footer(
        luce_is_mixed=True,
        gas_is_mixed=False,
        luce_estimated_savings=47.5,
        gas_estimated_savings=None,
        show_luce=True,
        show_gas=False,
    )

    assert (
        "💰 In base ai tuoi consumi di luce, stimiamo un risparmio di circa 47,50 €/anno" in footer
    )
    assert "📊 In questi casi" not in footer  # Non deve apparire il messaggio generico


def test_format_footer_not_mixed():
    """Test footer NON MIXED (nessuna menzione consumi)"""
    footer = _format_footer(
        luce_is_mixed=False,
        gas_is_mixed=False,
        luce_estimated_savings=None,
        gas_estimated_savings=None,
        show_luce=True,
        show_gas=True,
    )

    assert "📊" not in footer
    assert "💰" not in footer
    assert "consumi" not in footer
    assert "🔧 Se vuoi aggiornare" in footer


# ========== TEST CHECK_AND_NOTIFY SKIP MIXED WITH NEGATIVE SAVINGS ==========


@pytest.mark.asyncio
async def test_check_and_notify_skip_mixed_negative_savings():
    """Test che caso MIXED con risparmio negativo viene skippato"""
    import json
    import tempfile
    from unittest.mock import AsyncMock, patch

    from telegram import Bot

    # User con consumi che porterebbe a risparmio negativo
    users = {
        "123": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.130,  # Tariffa attuale bassa
                "commercializzazione": 65.0,
                "consumo_f1": 2700.0,
            },
            "gas": None,
        }
    }

    # Nuove tariffe peggiori (caso MIXED: una migliora, una peggiora)
    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.125,  # Migliora di 0.005
                    "commercializzazione": 85.0,  # Peggiora di 20
                }
            }
        }
    }

    # Risparmio energia: (0.130-0.125)*2700 = 13.5
    # Aumento comm: 65-85 = -20
    # Totale: 13.5-20 = -6.5 (negativo, deve skippare)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump(current_rates, f)
        rates_file = f.name

    try:
        with patch("checker.load_users", return_value=users):
            with patch("checker.RATES_FILE", Path(rates_file)):
                mock_bot = AsyncMock(spec=Bot)

                await check_and_notify_users("fake_token")

                # Verifica che NON sia stata inviata alcuna notifica
                mock_bot.send_message.assert_not_called()
    finally:
        Path(rates_file).unlink()


@pytest.mark.asyncio
async def test_check_and_notify_send_mixed_positive_savings():
    """Test che caso MIXED con risparmio positivo viene inviato"""
    import json
    import tempfile
    from unittest.mock import AsyncMock, patch

    from telegram import Bot

    # User con consumi che porta a risparmio positivo
    users = {
        "123": {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.145,  # Tariffa attuale alta
                "commercializzazione": 72.0,
                "consumo_f1": 2700.0,
            },
            "gas": None,
        }
    }

    # Nuove tariffe (caso MIXED)
    current_rates = {
        "luce": {
            "fissa": {
                "monoraria": {
                    "energia": 0.130,  # Migliora di 0.015
                    "commercializzazione": 85.0,  # Peggiora di 13
                }
            }
        }
    }

    # Risparmio energia: (0.145-0.130)*2700 = 40.5
    # Aumento comm: 72-85 = -13
    # Totale: 40.5-13 = 27.5 (positivo, deve inviare)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump(current_rates, f)
        rates_file = f.name

    try:
        with patch("checker.load_users", return_value=users):
            with patch("checker.RATES_FILE", Path(rates_file)):
                with patch("checker.Bot") as MockBot:
                    mock_bot_instance = AsyncMock(spec=Bot)
                    MockBot.return_value = mock_bot_instance
                    mock_bot_instance.send_message = AsyncMock()

                    with patch("checker.save_user"):
                        await check_and_notify_users("fake_token")

                        # Verifica che sia stata inviata una notifica
                        mock_bot_instance.send_message.assert_called_once()

                        # Verifica che il messaggio contenga la stima
                        call_args = mock_bot_instance.send_message.call_args
                        message_text = call_args.kwargs["text"]
                        assert "💰 In base ai tuoi consumi di luce" in message_text
                        assert "27,50 €/anno" in message_text
    finally:
        Path(rates_file).unlink()
