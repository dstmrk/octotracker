#!/usr/bin/env python3
"""
Health check endpoint per monitoring OctoTracker
Verifica stato database, tariffe e sistema
"""
import json
import logging
from datetime import datetime
from pathlib import Path
from typing import Any

from tornado.web import RequestHandler

logger = logging.getLogger(__name__)


class HealthHandler(RequestHandler):
    """
    Tornado handler per endpoint /health

    Ritorna JSON con stato del sistema:
    - 200 OK se tutto healthy
    - 200 OK con warning se degraded (es: tariffe vecchie)
    - 503 Service Unavailable se unhealthy (es: database inaccessibile)
    """

    def initialize(self, application_data: dict[str, Any]) -> None:
        """
        Inizializza handler con riferimento ai dati dell'applicazione.

        Args:
            application_data: bot_data dell'Application telegram
        """
        self.application_data = application_data

    def get(self) -> None:
        """Handler GET per /health endpoint"""
        try:
            health_data = check_system_health(self.application_data)

            # Status code HTTP in base allo stato
            if health_data["status"] == "unhealthy":
                self.set_status(503)  # Service Unavailable
            else:
                self.set_status(200)  # OK (anche per degraded)

            self.set_header("Content-Type", "application/json")
            self.write(json.dumps(health_data, indent=2))

        except Exception as e:
            logger.error(f"❌ Errore health check: {e}", exc_info=True)
            self.set_status(503)
            self.set_header("Content-Type", "application/json")
            self.write(
                json.dumps(
                    {
                        "status": "unhealthy",
                        "error": str(e),
                        "timestamp": datetime.now().isoformat(),
                    }
                )
            )


def check_system_health(application_data: dict[str, Any]) -> dict[str, Any]:
    """
    Verifica salute del sistema OctoTracker.

    Controlla:
    1. Database accessibile e funzionante
    2. File tariffe presente e aggiornato
    3. Task scheduler attivi

    Args:
        application_data: bot_data dell'Application telegram con task references

    Returns:
        Dizionario con stato generale e dettagli dei check
    """
    checks: dict[str, Any] = {}
    overall_status = "healthy"

    # Check 1: Database
    db_check = _check_database()
    checks["database"] = db_check
    if db_check["status"] == "error":
        overall_status = "unhealthy"

    # Check 2: Tariffe
    tariffe_check = _check_tariffe()
    checks["tariffe"] = tariffe_check
    if tariffe_check["status"] == "error":
        overall_status = "unhealthy"
    elif tariffe_check["status"] == "warning" and overall_status == "healthy":
        overall_status = "degraded"

    # Check 3: Bot tasks
    tasks_check = _check_tasks(application_data)
    checks["bot"] = tasks_check
    if tasks_check["status"] == "error":
        overall_status = "unhealthy"

    return {
        "status": overall_status,
        "timestamp": datetime.now().isoformat(),
        "checks": checks,
    }


def _check_database() -> dict[str, Any]:
    """
    Verifica accessibilità e stato del database SQLite.

    Returns:
        Dizionario con status e dettagli del database
    """
    # Import locale per evitare circular dependency
    from database import get_user_count

    db_path = Path("data/users.db")

    try:
        # Verifica file esiste
        if not db_path.exists() or not db_path.is_file():
            return {
                "status": "error",
                "path": str(db_path),
                "accessible": False,
                "error": "Database file not found",
            }

        # Verifica database funzionante (query di test)
        users_count = get_user_count()

        return {
            "status": "ok",
            "path": str(db_path),
            "accessible": True,
            "users_count": users_count,
        }

    except Exception as e:
        logger.error(f"❌ Errore check database: {e}")
        return {
            "status": "error",
            "path": str(db_path),
            "accessible": False,
            "error": str(e),
        }


def _check_tariffe() -> dict[str, Any]:
    """
    Verifica presenza e aggiornamento del file tariffe.

    Returns:
        Dizionario con status e dettagli del file tariffe
    """
    tariffe_path = Path("data/current_rates.json")

    try:
        # Verifica file esiste
        if not tariffe_path.exists():
            return {
                "status": "warning",
                "path": str(tariffe_path),
                "accessible": False,
                "message": "Tariffe file not found (will be created on first scrape)",
            }

        # Leggi e valida contenuto
        with open(tariffe_path) as f:
            data = json.load(f)
            last_update_str = data.get("data_aggiornamento")

        if not last_update_str:
            return {
                "status": "warning",
                "path": str(tariffe_path),
                "accessible": True,
                "message": "Missing data_aggiornamento field",
            }

        # Verifica che le tariffe non siano troppo vecchie (>3 giorni)
        last_update = datetime.strptime(last_update_str, "%Y-%m-%d")
        days_old = (datetime.now() - last_update).days

        if days_old > 3:
            return {
                "status": "warning",
                "path": str(tariffe_path),
                "accessible": True,
                "last_update": last_update_str,
                "days_old": days_old,
                "message": f"Tariffe outdated ({days_old} days old)",
            }

        return {
            "status": "ok",
            "path": str(tariffe_path),
            "accessible": True,
            "last_update": last_update_str,
            "days_old": days_old,
        }

    except json.JSONDecodeError as e:
        logger.error(f"❌ Errore parsing tariffe JSON: {e}")
        return {
            "status": "error",
            "path": str(tariffe_path),
            "accessible": True,
            "error": f"Invalid JSON: {str(e)}",
        }
    except Exception as e:
        logger.error(f"❌ Errore check tariffe: {e}")
        return {
            "status": "error",
            "path": str(tariffe_path),
            "accessible": False,
            "error": str(e),
        }


def _check_tasks(application_data: dict[str, Any]) -> dict[str, Any]:
    """
    Verifica che i task scheduler siano attivi.

    Args:
        application_data: bot_data con reference ai task asyncio

    Returns:
        Dizionario con status dei task
    """
    try:
        scraper_task = application_data.get("scraper_task")
        checker_task = application_data.get("checker_task")

        # Verifica task esistono
        if scraper_task is None or checker_task is None:
            return {
                "status": "error",
                "webhook_configured": True,
                "scheduled_tasks": {
                    "scraper": "missing",
                    "checker": "missing",
                },
                "error": "Scheduler tasks not initialized",
            }

        # Verifica task non sono cancellati o in errore
        scraper_status = "running" if not scraper_task.done() else "stopped"
        checker_status = "running" if not checker_task.done() else "stopped"

        # Se un task è done(), potrebbe essere crashato
        if scraper_task.done() or checker_task.done():
            return {
                "status": "error",
                "webhook_configured": True,
                "scheduled_tasks": {
                    "scraper": scraper_status,
                    "checker": checker_status,
                },
                "error": "One or more scheduler tasks have stopped",
            }

        return {
            "status": "ok",
            "webhook_configured": True,
            "scheduled_tasks": {
                "scraper": scraper_status,
                "checker": checker_status,
            },
        }

    except Exception as e:
        logger.error(f"❌ Errore check tasks: {e}")
        return {
            "status": "error",
            "webhook_configured": True,
            "error": str(e),
        }
