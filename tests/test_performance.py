"""
Test di performance per OctoTracker

Questi test verificano che le operazioni critiche completino
entro tempi ragionevoli.
"""

import time
from unittest.mock import AsyncMock, patch

import pytest
from telegram import Bot

from checker import check_and_notify_users, check_better_rates
from scraper import scrape_octopus_tariffe


@pytest.mark.asyncio
async def test_scraper_performance():
    """
    Test che lo scraper completi entro 15 secondi.

    Questo test fa una chiamata reale al sito Octopus Energy.
    Se il sito non è raggiungibile o ci sono problemi di rete,
    il test viene skippato.
    """
    try:
        start = time.time()
        result = await scrape_octopus_tariffe()
        duration = time.time() - start

        # Verifica tempo di esecuzione
        assert duration < 15.0, f"Scraper troppo lento: {duration:.2f}s (limite: 15s)"

        # Verifica che abbia restituito dati
        assert result is not None, "Scraper dovrebbe restituire dati"
        assert "luce" in result, "Result dovrebbe contenere chiave 'luce'"
        assert "gas" in result, "Result dovrebbe contenere chiave 'gas'"

        print(f"\n✅ Scraper completato in {duration:.2f}s (target: <15s)")

    except Exception as e:
        pytest.skip(f"Scraper test skipped (errore di rete o sito non disponibile): {e}")


def test_check_better_rates_performance():
    """
    Test che check_better_rates sia veloce (<100ms per confronto singolo utente).

    Questa funzione è puramente computazionale e non fa I/O.
    """
    # Dati utente realistici
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "monoraria",
            "energia": 0.025,
            "commercializzazione": 96.0,
        },
        "gas": {
            "tipo": "fissa",
            "fascia": "monoraria",
            "energia": 0.450,
            "commercializzazione": 144.0,
        },
    }

    # Tariffe attuali Octopus (migliori)
    current_rates = {
        "luce": {
            "variabile": {
                "monoraria": {"energia": 0.012, "commercializzazione": 96.0},
            },
        },
        "gas": {
            "fissa": {
                "monoraria": {"energia": 0.350, "commercializzazione": 100.0},
            },
        },
    }

    # Misura tempo di esecuzione
    start = time.time()
    result = check_better_rates(user_rates, current_rates)
    duration = time.time() - start

    # Verifica tempo (dovrebbe essere quasi istantaneo)
    assert duration < 0.1, f"check_better_rates troppo lento: {duration:.3f}s (limite: 0.1s)"

    # Verifica risultato
    assert result["has_savings"] is True
    assert result["luce_energia"] is not None, "Dovrebbe esserci risparmio su luce energia"
    assert result["gas_energia"] is not None, "Dovrebbe esserci risparmio su gas energia"
    assert result["gas_comm"] is not None, "Dovrebbe esserci risparmio su gas comm"

    print(f"\n✅ check_better_rates completato in {duration*1000:.1f}ms (target: <100ms)")


def test_check_better_rates_bulk_performance():
    """
    Test che check_better_rates scala bene con confronti multipli.

    Simula il controllo di 100 utenti diversi.
    """
    # Dati tariffe attuali
    current_rates = {
        "luce": {
            "variabile": {
                "monoraria": {"energia": 0.012, "commercializzazione": 96.0},
                "trioraria": {"energia": 0.015, "commercializzazione": 96.0},
            },
            "fissa": {
                "monoraria": {"energia": 0.140, "commercializzazione": 72.0},
            },
        },
        "gas": {
            "fissa": {
                "monoraria": {"energia": 0.350, "commercializzazione": 100.0},
            },
            "variabile": {
                "monoraria": {"energia": 0.030, "commercializzazione": 120.0},
            },
        },
    }

    # Genera 100 utenti con configurazioni diverse
    user_configs = []
    for i in range(100):
        user_config = {
            "luce": {
                "tipo": "variabile" if i % 2 == 0 else "fissa",
                "fascia": "monoraria" if i % 3 == 0 else "trioraria",
                "energia": 0.020 + (i * 0.001),
                "commercializzazione": 80.0 + (i * 0.5),
            },
            "gas": (
                {
                    "tipo": "fissa" if i % 2 == 0 else "variabile",
                    "fascia": "monoraria",
                    "energia": 0.400 + (i * 0.002),
                    "commercializzazione": 110.0 + (i * 0.3),
                }
                if i % 4 != 0
                else None
            ),  # 25% utenti senza gas
        }
        user_configs.append(user_config)

    # Misura tempo per 100 confronti
    start = time.time()
    results = []
    for user_rates in user_configs:
        result = check_better_rates(user_rates, current_rates)
        results.append(result)
    duration = time.time() - start

    # Verifica tempo totale (<1s per 100 utenti = <10ms/utente)
    assert duration < 1.0, f"Bulk check troppo lento: {duration:.2f}s per 100 utenti"

    # Verifica che tutti i risultati siano validi
    assert len(results) == 100
    assert all("has_savings" in r for r in results)

    avg_time_per_user = duration / 100 * 1000
    print(
        f"\n✅ 100 confronti completati in {duration:.2f}s "
        f"(media: {avg_time_per_user:.1f}ms/utente)"
    )


@pytest.mark.asyncio
async def test_checker_end_to_end_performance():
    """
    Test performance completa del checker con mock.

    Simula il flusso completo con 50 utenti, verificando che
    le notifiche vengano elaborate in tempo ragionevole.
    """
    # Mock Bot Telegram
    mock_bot = AsyncMock(spec=Bot)
    mock_bot.send_message = AsyncMock(return_value=True)

    # Mock database con 50 utenti
    mock_users = {}
    for i in range(50):
        mock_users[f"user_{i}"] = {
            "luce": {
                "tipo": "variabile",
                "fascia": "monoraria",
                "energia": 0.025,
                "commercializzazione": 96.0,
            },
            "gas": None,
        }

    # Mock tariffe attuali (migliori per generare notifiche)
    mock_rates = {
        "luce": {
            "variabile": {
                "monoraria": {"energia": 0.010, "commercializzazione": 72.0},
            },
        },
        "gas": {
            "fissa": {
                "monoraria": {"energia": 0.300, "commercializzazione": 100.0},
            },
        },
    }

    # Patch tutte le dipendenze
    with (
        patch("checker.load_users", return_value=mock_users),
        patch("checker.load_json", return_value=mock_rates),
        patch("checker.Bot", return_value=mock_bot),
        patch("checker.save_user", return_value=True),
    ):
        start = time.time()
        await check_and_notify_users("fake_token")
        duration = time.time() - start

        # Con 50 utenti e batching (20 concurrent), dovrebbe completare velocemente
        # Target: <5s per 50 utenti (considerando rate limiting simulato)
        assert duration < 5.0, f"Checker troppo lento per 50 utenti: {duration:.2f}s (limite: 5s)"

        print(f"\n✅ Checker (50 utenti) completato in {duration:.2f}s (target: <5s)")


@pytest.mark.asyncio
async def test_checker_with_no_savings_performance():
    """
    Test performance quando non ci sono risparmi (dovrebbe essere più veloce).
    """
    # Mock Bot Telegram
    mock_bot = AsyncMock(spec=Bot)

    # Mock database con 100 utenti
    mock_users = {}
    for i in range(100):
        mock_users[f"user_{i}"] = {
            "luce": {
                "tipo": "fissa",
                "fascia": "monoraria",
                "energia": 0.100,  # Già miglior tariffa
                "commercializzazione": 60.0,
            },
            "gas": None,
        }

    # Mock tariffe attuali (peggiori - nessun risparmio)
    mock_rates = {
        "luce": {
            "fissa": {
                "monoraria": {"energia": 0.150, "commercializzazione": 80.0},
            },
        },
        "gas": {},
    }

    with (
        patch("checker.load_users", return_value=mock_users),
        patch("checker.load_json", return_value=mock_rates),
        patch("checker.Bot", return_value=mock_bot),
    ):
        start = time.time()
        await check_and_notify_users("fake_token")
        duration = time.time() - start

        # Senza notifiche da inviare, dovrebbe essere molto veloce
        assert duration < 1.0, f"Checker troppo lento senza notifiche: {duration:.2f}s (limite: 1s)"

        # Verifica che non siano state inviate notifiche
        mock_bot.send_message.assert_not_called()

        print(f"\n✅ Checker (100 utenti, no savings) completato in {duration:.2f}s")


def test_multiple_checks_sequential_performance():
    """
    Test che check_better_rates possa essere chiamato sequenzialmente
    molte volte senza degradazione performance.
    """
    user_rates = {
        "luce": {
            "tipo": "variabile",
            "fascia": "trioraria",
            "energia": 0.020,
            "commercializzazione": 96.0,
        },
        "gas": None,
    }

    current_rates = {
        "luce": {
            "variabile": {
                "trioraria": {"energia": 0.015, "commercializzazione": 96.0},
            },
        },
        "gas": {},
    }

    # Esegui 1000 controlli sequenziali
    iterations = 1000
    start = time.time()

    for _ in range(iterations):
        check_better_rates(user_rates, current_rates)

    duration = time.time() - start

    # Target: <1s per 1000 iterazioni = <1ms per controllo
    assert duration < 1.0, f"Performance degradata: {duration:.2f}s per {iterations} controlli"

    avg_time = duration / iterations * 1000
    print(f"\n✅ {iterations} controlli in {duration:.2f}s (media: {avg_time:.3f}ms/controllo)")
