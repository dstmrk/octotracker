"""
Test per date_utils.py - Gestione date offerte a prezzo fisso
"""

from datetime import date

from date_utils import (
    FIXED_OFFER_MONTHS,
    add_months,
    compute_expiry_date,
    days_until,
    format_date_display,
    parse_activation_date,
)

# ========== parse_activation_date ==========


def test_parse_activation_date_slash():
    assert parse_activation_date("15/03/2025") == date(2025, 3, 15)


def test_parse_activation_date_dash():
    assert parse_activation_date("15-03-2025") == date(2025, 3, 15)


def test_parse_activation_date_dot():
    assert parse_activation_date("15.03.2025") == date(2025, 3, 15)


def test_parse_activation_date_strips_whitespace():
    assert parse_activation_date("  01/12/2024  ") == date(2024, 12, 1)


def test_parse_activation_date_invalid_format():
    assert parse_activation_date("2025-03-15") is None  # formato ISO non accettato


def test_parse_activation_date_not_a_date():
    assert parse_activation_date("domani") is None


def test_parse_activation_date_empty():
    assert parse_activation_date("") is None


def test_parse_activation_date_impossible_day():
    assert parse_activation_date("31/02/2025") is None


def test_parse_activation_date_year_too_old():
    assert parse_activation_date("15/03/1999") is None


def test_parse_activation_date_year_too_far_future():
    far_future = f"15/03/{date.today().year + 5}"
    assert parse_activation_date(far_future) is None


# ========== add_months ==========


def test_add_months_simple():
    assert add_months(date(2025, 1, 15), 12) == date(2026, 1, 15)


def test_add_months_crossing_year():
    assert add_months(date(2025, 6, 10), 12) == date(2026, 6, 10)


def test_add_months_partial():
    assert add_months(date(2025, 1, 31), 1) == date(2025, 2, 28)  # troncato


def test_add_months_leap_year():
    # 29/02/2024 (bisestile) + 12 mesi → 28/02/2025 (non bisestile)
    assert add_months(date(2024, 2, 29), 12) == date(2025, 2, 28)


def test_add_months_december():
    assert add_months(date(2025, 12, 15), 1) == date(2026, 1, 15)


# ========== compute_expiry_date ==========


def test_compute_expiry_date():
    assert compute_expiry_date(date(2025, 3, 15)) == date(2026, 3, 15)


def test_compute_expiry_date_uses_fixed_months():
    activation = date(2025, 1, 1)
    assert compute_expiry_date(activation) == add_months(activation, FIXED_OFFER_MONTHS)


# ========== format_date_display ==========


def test_format_date_display_from_iso():
    assert format_date_display("2026-03-15") == "15/03/2026"


def test_format_date_display_from_date():
    assert format_date_display(date(2026, 3, 15)) == "15/03/2026"


def test_format_date_display_invalid():
    assert format_date_display("not-a-date") == ""


def test_format_date_display_none():
    assert format_date_display(None) == ""


# ========== days_until ==========


def test_days_until_future():
    today = date(2025, 1, 1)
    assert days_until("2025-01-31", today=today) == 30


def test_days_until_past():
    today = date(2025, 2, 1)
    assert days_until("2025-01-01", today=today) == -31


def test_days_until_same_day():
    today = date(2025, 1, 1)
    assert days_until("2025-01-01", today=today) == 0


def test_days_until_invalid():
    assert days_until("not-a-date") is None
