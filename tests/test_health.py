#!/usr/bin/env python3
"""
Test per health_handler.py
Verifica endpoint /health e check di sistema
"""
import json
from datetime import datetime, timedelta
from unittest.mock import MagicMock, Mock, mock_open, patch

from health_handler import (
    HealthHandler,
    _check_database,
    _check_tariffe,
    _check_tasks,
    check_system_health,
)


class TestHealthHandler:
    """Test per HealthHandler Tornado request handler"""

    def test_get_healthy(self):
        """Test GET /health con sistema healthy"""
        # Setup handler mock
        handler = HealthHandler(
            application=MagicMock(),
            request=MagicMock(),
            application_data={"scraper_task": MagicMock(), "checker_task": MagicMock()},
        )
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()

        # Mock check_system_health per ritornare healthy
        with patch("health_handler.check_system_health") as mock_check:
            mock_check.return_value = {
                "status": "healthy",
                "timestamp": "2025-11-11T10:00:00",
                "checks": {},
            }

            handler.get()

            # Verifica status code 200
            handler.set_status.assert_called_once_with(200)

            # Verifica Content-Type JSON
            handler.set_header.assert_called_once_with("Content-Type", "application/json")

            # Verifica response body
            handler.write.assert_called_once()
            written_data = handler.write.call_args[0][0]
            response = json.loads(written_data)
            assert response["status"] == "healthy"

    def test_get_degraded(self):
        """Test GET /health con sistema degraded"""
        handler = HealthHandler(application=MagicMock(), request=MagicMock(), application_data={})
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()

        with patch("health_handler.check_system_health") as mock_check:
            mock_check.return_value = {
                "status": "degraded",
                "timestamp": "2025-11-11T10:00:00",
                "checks": {},
            }

            handler.get()

            # Degraded ritorna 200 (non critico)
            handler.set_status.assert_called_once_with(200)

    def test_get_unhealthy(self):
        """Test GET /health con sistema unhealthy"""
        handler = HealthHandler(application=MagicMock(), request=MagicMock(), application_data={})
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()

        with patch("health_handler.check_system_health") as mock_check:
            mock_check.return_value = {
                "status": "unhealthy",
                "timestamp": "2025-11-11T10:00:00",
                "checks": {},
            }

            handler.get()

            # Unhealthy ritorna 503 Service Unavailable
            handler.set_status.assert_called_once_with(503)

    def test_get_exception(self):
        """Test GET /health con exception"""
        handler = HealthHandler(application=MagicMock(), request=MagicMock(), application_data={})
        handler.set_status = Mock()
        handler.set_header = Mock()
        handler.write = Mock()

        with patch("health_handler.check_system_health") as mock_check:
            mock_check.side_effect = Exception("Test error")

            handler.get()

            # Exception ritorna 503
            handler.set_status.assert_called_once_with(503)

            # Verifica error response
            written_data = handler.write.call_args[0][0]
            response = json.loads(written_data)
            assert response["status"] == "unhealthy"
            assert "error" in response


class TestCheckSystemHealth:
    """Test per check_system_health()"""

    def test_all_healthy(self):
        """Test con tutti i check ok"""
        with (
            patch("health_handler._check_database") as mock_db,
            patch("health_handler._check_tariffe") as mock_tar,
            patch("health_handler._check_tasks") as mock_tasks,
        ):
            mock_db.return_value = {"status": "ok"}
            mock_tar.return_value = {"status": "ok"}
            mock_tasks.return_value = {"status": "ok"}

            result = check_system_health({})

            assert result["status"] == "healthy"
            assert "timestamp" in result
            assert "checks" in result
            assert result["checks"]["database"]["status"] == "ok"
            assert result["checks"]["tariffe"]["status"] == "ok"
            assert result["checks"]["bot"]["status"] == "ok"

    def test_degraded_tariffe_warning(self):
        """Test con tariffe warning -> degraded"""
        with (
            patch("health_handler._check_database") as mock_db,
            patch("health_handler._check_tariffe") as mock_tar,
            patch("health_handler._check_tasks") as mock_tasks,
        ):
            mock_db.return_value = {"status": "ok"}
            mock_tar.return_value = {"status": "warning", "message": "Tariffe outdated"}
            mock_tasks.return_value = {"status": "ok"}

            result = check_system_health({})

            assert result["status"] == "degraded"

    def test_unhealthy_database_error(self):
        """Test con database error -> unhealthy"""
        with (
            patch("health_handler._check_database") as mock_db,
            patch("health_handler._check_tariffe") as mock_tar,
            patch("health_handler._check_tasks") as mock_tasks,
        ):
            mock_db.return_value = {"status": "error", "error": "DB not found"}
            mock_tar.return_value = {"status": "ok"}
            mock_tasks.return_value = {"status": "ok"}

            result = check_system_health({})

            assert result["status"] == "unhealthy"

    def test_unhealthy_tariffe_error(self):
        """Test con tariffe error -> unhealthy"""
        with (
            patch("health_handler._check_database") as mock_db,
            patch("health_handler._check_tariffe") as mock_tar,
            patch("health_handler._check_tasks") as mock_tasks,
        ):
            mock_db.return_value = {"status": "ok"}
            mock_tar.return_value = {"status": "error", "error": "Invalid JSON"}
            mock_tasks.return_value = {"status": "ok"}

            result = check_system_health({})

            assert result["status"] == "unhealthy"

    def test_unhealthy_tasks_error(self):
        """Test con tasks error -> unhealthy"""
        with (
            patch("health_handler._check_database") as mock_db,
            patch("health_handler._check_tariffe") as mock_tar,
            patch("health_handler._check_tasks") as mock_tasks,
        ):
            mock_db.return_value = {"status": "ok"}
            mock_tar.return_value = {"status": "ok"}
            mock_tasks.return_value = {"status": "error", "error": "Tasks missing"}

            result = check_system_health({})

            assert result["status"] == "unhealthy"


class TestCheckDatabase:
    """Test per _check_database()"""

    def test_database_ok(self):
        """Test con database accessibile e funzionante"""
        with (
            patch("health_handler.Path") as mock_path,
            patch("database.get_user_count") as mock_count,
        ):
            # Mock file exists
            mock_db_path = MagicMock()
            mock_db_path.exists.return_value = True
            mock_db_path.is_file.return_value = True
            mock_db_path.__str__.return_value = "data/octotracker.db"
            mock_path.return_value = mock_db_path

            # Mock count
            mock_count.return_value = 42

            result = _check_database()

            assert result["status"] == "ok"
            assert result["users_count"] == 42

    def test_database_not_found(self):
        """Test con database non esistente"""
        with patch("health_handler.Path") as mock_path:
            mock_db_path = MagicMock()
            mock_db_path.exists.return_value = False
            mock_db_path.__str__.return_value = "data/octotracker.db"
            mock_path.return_value = mock_db_path

            result = _check_database()

            assert result["status"] == "error"
            assert "not found" in result["error"].lower()

    def test_database_not_file(self):
        """Test con path database che è una directory"""
        with patch("health_handler.Path") as mock_path:
            mock_db_path = MagicMock()
            mock_db_path.exists.return_value = True
            mock_db_path.is_file.return_value = False
            mock_db_path.__str__.return_value = "data/octotracker.db"
            mock_path.return_value = mock_db_path

            result = _check_database()

            assert result["status"] == "error"

    def test_database_exception(self):
        """Test con exception durante query database"""
        with (
            patch("health_handler.Path") as mock_path,
            patch("database.get_user_count") as mock_count,
        ):
            mock_db_path = MagicMock()
            mock_db_path.exists.return_value = True
            mock_db_path.is_file.return_value = True
            mock_db_path.__str__.return_value = "data/octotracker.db"
            mock_path.return_value = mock_db_path

            # Simula errore query
            mock_count.side_effect = Exception("Database locked")

            result = _check_database()

            assert result["status"] == "error"
            assert "locked" in result["error"].lower()


class TestCheckTariffe:
    """Test per _check_tariffe()"""

    def test_tariffe_ok(self):
        """Test con tariffe presenti e recenti"""
        yesterday = (datetime.now() - timedelta(days=1)).strftime("%Y-%m-%d")

        mock_data = json.dumps(
            {
                "luce": {},
                "gas": {},
                "data_aggiornamento": yesterday,
            }
        )

        with (
            patch("health_handler.Path") as mock_path,
            patch("builtins.open", mock_open(read_data=mock_data)),
        ):
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = True
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            result = _check_tariffe()

            assert result["status"] == "ok"
            assert result["last_update"] == yesterday
            assert result["days_old"] == 1

    def test_tariffe_not_found(self):
        """Test con file tariffe non esistente"""
        with patch("health_handler.Path") as mock_path:
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = False
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            result = _check_tariffe()

            assert result["status"] == "warning"
            assert "not found" in result["message"].lower()

    def test_tariffe_outdated(self):
        """Test con tariffe vecchie (>3 giorni)"""
        old_date = (datetime.now() - timedelta(days=5)).strftime("%Y-%m-%d")

        mock_data = json.dumps(
            {
                "luce": {},
                "gas": {},
                "data_aggiornamento": old_date,
            }
        )

        with (
            patch("health_handler.Path") as mock_path,
            patch("builtins.open", mock_open(read_data=mock_data)),
        ):
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = True
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            result = _check_tariffe()

            assert result["status"] == "warning"
            assert result["days_old"] == 5
            assert "outdated" in result["message"].lower()

    def test_tariffe_missing_date(self):
        """Test con file tariffe senza data_aggiornamento"""
        mock_data = json.dumps({"luce": {}, "gas": {}})  # No data_aggiornamento

        with (
            patch("health_handler.Path") as mock_path,
            patch("builtins.open", mock_open(read_data=mock_data)),
        ):
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = True
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            result = _check_tariffe()

            assert result["status"] == "warning"
            assert "missing" in result["message"].lower()

    def test_tariffe_invalid_json(self):
        """Test con JSON malformato"""
        with (
            patch("health_handler.Path") as mock_path,
            patch("builtins.open", mock_open(read_data="invalid json {")),
        ):
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = True
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            result = _check_tariffe()

            assert result["status"] == "error"
            assert "json" in result["error"].lower()

    def test_tariffe_exception(self):
        """Test con exception durante lettura file"""
        with (
            patch("health_handler.Path") as mock_path,
            patch("builtins.open") as mock_file,
        ):
            mock_tariffe_path = MagicMock()
            mock_tariffe_path.exists.return_value = True
            mock_tariffe_path.__str__.return_value = "data/current_rates.json"
            mock_path.return_value = mock_tariffe_path

            # Simula errore I/O
            mock_file.side_effect = OSError("Permission denied")

            result = _check_tariffe()

            assert result["status"] == "error"
            assert "denied" in result["error"].lower()


class TestCheckTasks:
    """Test per _check_tasks()"""

    def test_tasks_ok(self):
        """Test con tasks attivi"""
        # Mock asyncio tasks
        scraper_task = MagicMock()
        scraper_task.done.return_value = False  # Task ancora running
        checker_task = MagicMock()
        checker_task.done.return_value = False

        application_data = {
            "scraper_task": scraper_task,
            "checker_task": checker_task,
        }

        result = _check_tasks(application_data)

        assert result["status"] == "ok"
        assert result["scheduled_tasks"]["scraper"] == "running"
        assert result["scheduled_tasks"]["checker"] == "running"

    def test_tasks_missing(self):
        """Test con tasks non inizializzati"""
        application_data = {}  # Nessun task

        result = _check_tasks(application_data)

        assert result["status"] == "error"
        assert "missing" in result["scheduled_tasks"]["scraper"]
        assert "missing" in result["scheduled_tasks"]["checker"]
        assert "not initialized" in result["error"].lower()

    def test_tasks_stopped_scraper(self):
        """Test con scraper task stopped"""
        scraper_task = MagicMock()
        scraper_task.done.return_value = True  # Task completato/crashato
        checker_task = MagicMock()
        checker_task.done.return_value = False

        application_data = {
            "scraper_task": scraper_task,
            "checker_task": checker_task,
        }

        result = _check_tasks(application_data)

        assert result["status"] == "error"
        assert result["scheduled_tasks"]["scraper"] == "stopped"
        assert result["scheduled_tasks"]["checker"] == "running"
        assert "stopped" in result["error"].lower()

    def test_tasks_stopped_checker(self):
        """Test con checker task stopped"""
        scraper_task = MagicMock()
        scraper_task.done.return_value = False
        checker_task = MagicMock()
        checker_task.done.return_value = True  # Task completato/crashato

        application_data = {
            "scraper_task": scraper_task,
            "checker_task": checker_task,
        }

        result = _check_tasks(application_data)

        assert result["status"] == "error"
        assert result["scheduled_tasks"]["scraper"] == "running"
        assert result["scheduled_tasks"]["checker"] == "stopped"

    def test_tasks_both_stopped(self):
        """Test con entrambi i tasks stopped"""
        scraper_task = MagicMock()
        scraper_task.done.return_value = True
        checker_task = MagicMock()
        checker_task.done.return_value = True

        application_data = {
            "scraper_task": scraper_task,
            "checker_task": checker_task,
        }

        result = _check_tasks(application_data)

        assert result["status"] == "error"
        assert result["scheduled_tasks"]["scraper"] == "stopped"
        assert result["scheduled_tasks"]["checker"] == "stopped"

    def test_tasks_exception(self):
        """Test con exception durante check"""
        # Mock task che solleva exception quando chiamato done()
        scraper_task = MagicMock()
        scraper_task.done.side_effect = Exception("Task error")

        application_data = {
            "scraper_task": scraper_task,
            "checker_task": MagicMock(),
        }

        result = _check_tasks(application_data)

        assert result["status"] == "error"
        assert "error" in result
