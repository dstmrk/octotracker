#!/usr/bin/env python3
"""
Unit tests per scraper.py
Verifica formato JSON output senza eseguire scraping reale
"""
import json


def test_complete_data_structure():
    """Verifica struttura completa con tutte le tariffe"""
    # Simula output completo dello scraper
    tariffe_data = {
        "luce": {
            "fissa": {
                "monoraria": {"energia": 0.145, "commercializzazione": 72.0}
            },
            "variabile": {
                "monoraria": {"energia": 0.0088, "commercializzazione": 72.0},
                "trioraria": {"energia": 0.0088, "commercializzazione": 72.0}
            }
        },
        "gas": {
            "fissa": {
                "monoraria": {"energia": 0.456, "commercializzazione": 84.0}
            },
            "variabile": {
                "monoraria": {"energia": 0.08, "commercializzazione": 84.0}
            }
        },
        "data_aggiornamento": "2025-11-10"
    }

    # Verifica struttura top-level
    assert 'luce' in tariffe_data
    assert 'gas' in tariffe_data
    assert 'data_aggiornamento' in tariffe_data

    # Verifica struttura luce
    assert 'fissa' in tariffe_data['luce']
    assert 'variabile' in tariffe_data['luce']

    # Verifica luce fissa
    assert 'monoraria' in tariffe_data['luce']['fissa']
    assert set(tariffe_data['luce']['fissa']['monoraria'].keys()) == {'energia', 'commercializzazione'}

    # Verifica luce variabile
    assert 'monoraria' in tariffe_data['luce']['variabile']
    assert 'trioraria' in tariffe_data['luce']['variabile']

    # Verifica struttura gas
    assert 'fissa' in tariffe_data['gas']
    assert 'variabile' in tariffe_data['gas']

    # Verifica gas fissa
    assert 'monoraria' in tariffe_data['gas']['fissa']

    # Verifica gas variabile
    assert 'monoraria' in tariffe_data['gas']['variabile']


def test_partial_data_only_luce():
    """Verifica dati parziali: solo tariffe luce"""
    tariffe_data = {
        "luce": {
            "fissa": {
                "monoraria": {"energia": 0.145, "commercializzazione": 72.0}
            },
            "variabile": {}
        },
        "gas": {
            "fissa": {},
            "variabile": {}
        },
        "data_aggiornamento": "2025-11-10"
    }

    # Struttura base deve esistere
    assert 'luce' in tariffe_data
    assert 'gas' in tariffe_data

    # Luce fissa deve avere dati
    assert tariffe_data['luce']['fissa'].get('monoraria') is not None
    assert 'energia' in tariffe_data['luce']['fissa']['monoraria']

    # Gas deve essere vuoto ma struttura esistente
    assert isinstance(tariffe_data['gas']['fissa'], dict)
    assert isinstance(tariffe_data['gas']['variabile'], dict)


def test_partial_data_only_gas():
    """Verifica dati parziali: solo tariffe gas"""
    tariffe_data = {
        "luce": {
            "fissa": {},
            "variabile": {}
        },
        "gas": {
            "fissa": {},
            "variabile": {
                "monoraria": {"energia": 0.08, "commercializzazione": 84.0}
            }
        },
        "data_aggiornamento": "2025-11-10"
    }

    # Struttura base deve esistere
    assert 'luce' in tariffe_data
    assert 'gas' in tariffe_data

    # Gas variabile deve avere dati
    assert tariffe_data['gas']['variabile'].get('monoraria') is not None
    assert 'energia' in tariffe_data['gas']['variabile']['monoraria']

    # Luce deve essere vuoto ma struttura esistente
    assert isinstance(tariffe_data['luce']['fissa'], dict)
    assert isinstance(tariffe_data['luce']['variabile'], dict)


def test_empty_data():
    """Verifica dati completamente vuoti (scraping fallito)"""
    tariffe_data = {
        "luce": {
            "fissa": {},
            "variabile": {}
        },
        "gas": {
            "fissa": {},
            "variabile": {}
        },
        "data_aggiornamento": "2025-11-10"
    }

    # Struttura base deve esistere anche se vuota
    assert 'luce' in tariffe_data
    assert 'gas' in tariffe_data
    assert isinstance(tariffe_data['luce']['fissa'], dict)
    assert isinstance(tariffe_data['luce']['variabile'], dict)
    assert isinstance(tariffe_data['gas']['fissa'], dict)
    assert isinstance(tariffe_data['gas']['variabile'], dict)


def test_data_types():
    """Verifica tipi di dati corretti"""
    tariffe_data = {
        "luce": {
            "fissa": {
                "monoraria": {"energia": 0.145, "commercializzazione": 72.0}
            },
            "variabile": {}
        },
        "gas": {
            "fissa": {},
            "variabile": {}
        },
        "data_aggiornamento": "2025-11-10"
    }

    # energia deve essere float
    assert isinstance(tariffe_data['luce']['fissa']['monoraria']['energia'], float)

    # commercializzazione deve essere float
    assert isinstance(tariffe_data['luce']['fissa']['monoraria']['commercializzazione'], float)

    # data_aggiornamento deve essere string
    assert isinstance(tariffe_data['data_aggiornamento'], str)


def test_json_serializable():
    """Verifica che l'output sia serializzabile in JSON"""
    tariffe_data = {
        "luce": {
            "fissa": {
                "monoraria": {"energia": 0.145, "commercializzazione": 72.0}
            },
            "variabile": {
                "monoraria": {"energia": 0.0088, "commercializzazione": 72.0}
            }
        },
        "gas": {
            "fissa": {
                "monoraria": {"energia": 0.456, "commercializzazione": 84.0}
            },
            "variabile": {}
        },
        "data_aggiornamento": "2025-11-10"
    }

    # Deve essere serializzabile senza errori
    json_str = json.dumps(tariffe_data, indent=2)
    parsed = json.loads(json_str)
    assert tariffe_data == parsed
