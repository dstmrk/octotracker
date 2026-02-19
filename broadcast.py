"""
Script per inviare messaggi broadcast a utenti specifici.

Uso:
    python broadcast.py [message_file] [users_file]

    Se non specificati:
    - message_file: 'message.txt'
    - users_file: 'users.txt'

Il messaggio viene inviato a batch (default 10, configurabile via BROADCAST_BATCH_SIZE
nel file .env) con rate limiting per evitare problemi con l'API di Telegram.

Prima dell'invio viene mostrata un'anteprima del messaggio e il numero di destinatari.
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

logger = logging.getLogger(__name__)

# Configurazione batch size (default 10)
DEFAULT_BATCH_SIZE = 10


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
    bot: Bot, user_ids: list[str], message: str, batch_size: int = DEFAULT_BATCH_SIZE
) -> tuple[int, int]:
    """
    Invia messaggi broadcast in parallelo con rate limiting.

    Utilizza un semaforo per limitare gli invii simultanei.

    Args:
        bot: Bot Telegram
        user_ids: Lista di user_id destinatari
        message: Messaggio da inviare
        batch_size: Numero massimo di invii simultanei (default da .env o 10)

    Returns:
        Tupla (messaggi_inviati, messaggi_falliti)
    """
    semaphore = asyncio.Semaphore(batch_size)

    async def send_with_limit(position: int, user_id: str) -> bool:
        async with semaphore:
            success = await send_broadcast_message(bot, user_id, message)
            if success:
                logger.info(f"  ✅ Messaggio inviato al destinatario #{position}")
            else:
                logger.warning(f"  ❌ Invio fallito per il destinatario #{position}")
            return success

    logger.info(f"📨 Invio {len(user_ids)} messaggi in parallelo (max {batch_size} simultanei)...")

    tasks = [send_with_limit(index, user_id) for index, user_id in enumerate(user_ids, start=1)]
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


def load_users_from_file(users_file: str) -> list[str]:
    """
    Carica la lista di user_id da un file.

    Il file deve contenere un user_id Telegram per riga.
    Righe vuote e commenti (iniziano con #) vengono ignorati.

    Args:
        users_file: Path del file contenente gli user_id

    Returns:
        Lista di user_id

    Raises:
        FileNotFoundError: Se il file non esiste
        ValueError: Se il file è vuoto o non contiene user_id validi
    """
    users_path = Path(users_file)
    if not users_path.exists():
        raise FileNotFoundError(f"File utenti non trovato: {users_file}")

    lines = users_path.read_text(encoding="utf-8").strip().split("\n")

    # Filtra righe vuote e commenti
    user_ids = []
    for line in lines:
        line = line.strip()
        if line and not line.startswith("#"):
            user_ids.append(line)

    if not user_ids:
        raise ValueError(f"Il file {users_file} non contiene user_id validi")

    return user_ids


def confirm_send(message: str, user_count: int, batch_size: int) -> bool:
    """
    Mostra un'anteprima del messaggio e chiede conferma all'utente.

    Args:
        message: Il messaggio da inviare
        user_count: Numero di utenti che riceveranno il messaggio
        batch_size: Numero di invii simultanei configurato

    Returns:
        True se l'utente conferma, False altrimenti
    """
    print("\n" + "=" * 70)
    print("📢 BROADCAST MESSAGE PREVIEW")
    print("=" * 70)
    print(f"\n{message}\n")
    print("=" * 70)
    print("\n📊 Configurazione invio:")
    print(f"   • Destinatari: {user_count} utenti")
    print(f"   • Batch size: {batch_size} messaggi simultanei")
    print(f"\n⚠️  Stai per inviare questo messaggio a {user_count} utenti.")

    confirmation = input("\nSei sicuro? (S/N): ").strip().upper()

    return confirmation in ["S", "SI", "SÌ", "Y", "YES"]


async def broadcast_to_users(
    message_file: str, users_file: str, bot_token: str, batch_size: int = DEFAULT_BATCH_SIZE
) -> dict[str, int]:
    """
    Carica messaggio e utenti da file, conferma con l'utente e invia broadcast.

    Args:
        message_file: Path del file contenente il messaggio
        users_file: Path del file contenente gli user_id (uno per riga)
        bot_token: Token del bot Telegram
        batch_size: Numero massimo di invii simultanei

    Returns:
        Dizionario con statistiche di invio: {"successful": int, "failed": int, "total": int}

    Raises:
        FileNotFoundError: Se il file messaggio o utenti non esiste
        ValueError: Se i file sono vuoti o non validi
    """
    # Carica il messaggio
    message = load_message(message_file)

    # Carica gli utenti dal file
    user_ids = load_users_from_file(users_file)
    user_count = len(user_ids)

    # Mostra preview e chiedi conferma
    if not confirm_send(message, user_count, batch_size):
        logger.info("❌ Invio annullato dall'utente")
        return {"successful": 0, "failed": 0, "total": 0}

    # Inizia l'invio
    logger.info(f"\n🚀 Inizio invio broadcast a {user_count} utenti...")
    start_time = time()

    bot = Bot(token=bot_token)
    successful, failed = await send_broadcasts_parallel(bot, user_ids, message, batch_size)

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

    # Batch size da .env (default: 10)
    batch_size = int(os.getenv("BROADCAST_BATCH_SIZE", DEFAULT_BATCH_SIZE))

    # File messaggio (default: message.txt)
    message_file = sys.argv[1] if len(sys.argv) > 1 else "message.txt"

    # File utenti (default: users.txt)
    users_file = sys.argv[2] if len(sys.argv) > 2 else "users.txt"

    # Esegui broadcast
    try:
        asyncio.run(broadcast_to_users(message_file, users_file, bot_token, batch_size))
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
