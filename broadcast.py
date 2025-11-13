"""
Script per inviare messaggi broadcast a tutti gli utenti del bot.

Uso:
    python broadcast.py [message_file]

    Se non specificato, usa 'message.txt' come file sorgente.

Il messaggio viene inviato massimo 10 alla volta con rate limiting
per evitare problemi con l'API di Telegram.
"""

import asyncio
import logging
import os
import sys
from pathlib import Path
from time import time

from dotenv import load_dotenv
from telegram import Bot
from telegram.error import NetworkError, RetryAfter, TelegramError, TimedOut

from database import load_users

logger = logging.getLogger(__name__)


async def send_broadcast_message(bot: Bot, user_id: str, message: str) -> bool:
    """
    Invia un messaggio broadcast a un singolo utente.

    Args:
        bot: Bot Telegram
        user_id: ID dell'utente destinatario
        message: Testo del messaggio da inviare

    Returns:
        True se l'invio ha successo, False altrimenti
    """
    try:
        await bot.send_message(chat_id=user_id, text=message, parse_mode="HTML")
        return True
    except RetryAfter as e:
        logger.warning(f"⏱️  Rate limit per utente {user_id}: riprova dopo {e.retry_after}s")
        return False
    except (TimedOut, NetworkError) as e:
        logger.error(f"❌ Errore di rete per utente {user_id}: {e}")
        return False
    except TelegramError as e:
        logger.error(f"❌ Errore Telegram per utente {user_id}: {e}")
        return False


async def send_broadcasts_parallel(
    bot: Bot, users: dict[str, dict], message: str
) -> tuple[int, int]:
    """
    Invia messaggi broadcast in parallelo con rate limiting.

    Utilizza un semaforo per limitare a massimo 10 invii simultanei,
    come implementato in checker.py.

    Args:
        bot: Bot Telegram
        users: Dizionario {user_id: user_data}
        message: Messaggio da inviare

    Returns:
        Tupla (messaggi_inviati, messaggi_falliti)
    """
    semaphore = asyncio.Semaphore(10)

    async def send_with_limit(user_id: str) -> bool:
        async with semaphore:
            success = await send_broadcast_message(bot, user_id, message)
            if success:
                logger.info(f"  ✅ Messaggio inviato a {user_id}")
            else:
                logger.warning(f"  ❌ Invio fallito per {user_id}")
            return success

    logger.info(f"📨 Invio {len(users)} messaggi in parallelo (max 10 simultanei)...")

    tasks = [send_with_limit(user_id) for user_id in users.keys()]
    results = await asyncio.gather(*tasks, return_exceptions=True)

    successful = sum(1 for r in results if r is True)
    failed = len(results) - successful

    return successful, failed


def load_message(message_file: str) -> str:
    """
    Carica il messaggio da un file.

    Args:
        message_file: Path del file contenente il messaggio

    Returns:
        Contenuto del messaggio

    Raises:
        FileNotFoundError: Se il file non esiste
        ValueError: Se il file è vuoto
    """
    message_path = Path(message_file)
    if not message_path.exists():
        raise FileNotFoundError(f"File messaggio non trovato: {message_file}")

    message = message_path.read_text(encoding="utf-8").strip()
    if not message:
        raise ValueError("Il file messaggio è vuoto")

    return message


def confirm_send(message: str, user_count: int) -> bool:
    """
    Mostra un'anteprima del messaggio e chiede conferma all'utente.

    Args:
        message: Il messaggio da inviare
        user_count: Numero di utenti che riceveranno il messaggio

    Returns:
        True se l'utente conferma, False altrimenti
    """
    print("\n" + "=" * 70)
    print("📢 BROADCAST MESSAGE PREVIEW")
    print("=" * 70)
    print(f"\n{message}\n")
    print("=" * 70)
    print(f"\n⚠️  Stai per inviare questo messaggio a {user_count} utenti.")

    confirmation = input("\nSei sicuro? (S/N): ").strip().upper()

    return confirmation in ["S", "SI", "SÌ", "Y", "YES"]


async def broadcast_to_all_users(message_file: str, bot_token: str) -> dict[str, int]:
    """
    Carica il messaggio, conferma con l'utente e invia broadcast a tutti gli utenti.

    Args:
        message_file: Path del file contenente il messaggio
        bot_token: Token del bot Telegram

    Returns:
        Dizionario con statistiche di invio: {"successful": int, "failed": int, "total": int}

    Raises:
        FileNotFoundError: Se il file messaggio non esiste
        ValueError: Se il file messaggio è vuoto o non ci sono utenti
    """
    # Carica il messaggio
    message = load_message(message_file)

    # Carica gli utenti
    users = load_users()
    user_count = len(users)

    if user_count == 0:
        raise ValueError("Nessun utente nel database")

    # Mostra preview e chiedi conferma
    if not confirm_send(message, user_count):
        logger.info("❌ Invio annullato dall'utente")
        return {"successful": 0, "failed": 0, "total": 0}

    # Inizia l'invio
    logger.info(f"\n🚀 Inizio invio broadcast a {user_count} utenti...")
    start_time = time()

    bot = Bot(token=bot_token)
    successful, failed = await send_broadcasts_parallel(bot, users, message)

    duration = time() - start_time

    # Riepilogo
    print("\n" + "=" * 70)
    print("📊 RIEPILOGO INVIO")
    print("=" * 70)
    print(f"  ✅ Inviati con successo: {successful}/{user_count}")
    print(f"  ❌ Falliti: {failed}/{user_count}")
    print(f"  ⏱️  Tempo totale: {duration:.2f}s")
    print("=" * 70 + "\n")

    logger.info(
        f"✅ Broadcast completato in {duration:.2f}s - "
        f"Successi: {successful}/{user_count}, Falliti: {failed}/{user_count}"
    )

    return {"successful": successful, "failed": failed, "total": user_count}


def main():
    """Entry point per lo script broadcast."""
    # Setup logging
    logging.basicConfig(
        level=logging.INFO, format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    )

    # Carica variabili d'ambiente
    load_dotenv()
    bot_token = os.getenv("TELEGRAM_BOT_TOKEN")

    if not bot_token:
        logger.error("❌ TELEGRAM_BOT_TOKEN non configurato nel file .env")
        sys.exit(1)

    # File messaggio (default: message.txt nella directory corrente)
    message_file = sys.argv[1] if len(sys.argv) > 1 else "message.txt"

    # Esegui broadcast
    try:
        asyncio.run(broadcast_to_all_users(message_file, bot_token))
    except FileNotFoundError as e:
        logger.error(f"❌ {e}")
        sys.exit(1)
    except ValueError as e:
        logger.error(f"❌ {e}")
        sys.exit(1)
    except KeyboardInterrupt:
        logger.info("\n❌ Invio interrotto dall'utente")
        sys.exit(1)
    except Exception as e:
        logger.error(f"❌ Errore durante l'invio broadcast: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()
