#!/usr/bin/env python3
"""
Controlla se ci sono tariffe più convenienti e notifica gli utenti
"""
import os
import json
from pathlib import Path
from telegram import Bot
from dotenv import load_dotenv
import asyncio

load_dotenv()

# File dati
DATA_DIR = Path(__file__).parent / "data"
USERS_FILE = DATA_DIR / "users.json"
RATES_FILE = DATA_DIR / "current_rates.json"

def load_json(file_path):
    """Carica file JSON"""
    if file_path.exists():
        with open(file_path, 'r') as f:
            return json.load(f)
    return None

def save_users(users):
    """Salva dati utenti"""
    DATA_DIR.mkdir(exist_ok=True)
    with open(USERS_FILE, 'w') as f:
        json.dump(users, f, indent=2)

def format_number(value, max_decimals=3):
    """
    Formatta numero con logica intelligente per i decimali:
    - Se intero (es. 72.0) → "72" (nessun decimale)
    - Se ha decimali → mostra almeno 2 decimali, rimuovi zeri trailing oltre il secondo
    Usa virgola come separatore decimale (stile italiano)

    Esempi:
    - 72.0 → "72"
    - 72.5 → "72,50"
    - 0.145 → "0,145"
    - 0.140 → "0,14"
    - 0.100 → "0,10"
    """
    # Arrotonda al massimo di decimali
    rounded = round(value, max_decimals)

    # Controlla se è un numero intero
    if rounded == int(rounded):
        return str(int(rounded))

    # Ha decimali: formatta con max decimali e poi sistema
    formatted = f"{rounded:.{max_decimals}f}"

    # Rimuovi zeri trailing
    formatted = formatted.rstrip('0')

    # Assicurati di avere almeno 2 decimali se ci sono decimali
    parts = formatted.split('.')
    if len(parts) > 1 and len(parts[1]) < 2:
        parts[1] = parts[1].ljust(2, '0')
        formatted = '.'.join(parts)

    # Sostituisci punto con virgola (stile italiano)
    return formatted.replace('.', ',')

def check_better_rates(user_rates, current_rates):
    """
    Confronta tariffe utente con tariffe attuali dello stesso tipo
    Ritorna dizionario con risparmi e peggioramenti trovati
    """
    savings = {
        'luce_energia': None,
        'luce_comm': None,
        'gas_energia': None,
        'gas_comm': None,
        'luce_energia_worse': False,
        'luce_comm_worse': False,
        'gas_energia_worse': False,
        'gas_comm_worse': False,
        'has_savings': False,
        'is_mixed': False,
        'luce_tipo': user_rates['luce_tipo'],
        'gas_tipo': user_rates.get('gas_tipo')
    }

    # Determina quale tariffa luce confrontare in base al tipo dell'utente
    luce_tipo = user_rates['luce_tipo']

    # Mappa tipo → chiave nel current_rates
    luce_key = None
    if luce_tipo == "Fissa":
        luce_key = 'luce_fissa'
    elif luce_tipo == "Variabile Monoraria":
        luce_key = 'luce_variabile_mono'
    elif luce_tipo == "Variabile Trioraria":
        luce_key = 'luce_variabile_multi'

    # Controlla luce (solo se troviamo la tariffa dello stesso tipo)
    if luce_key and current_rates.get(luce_key):
        luce_rate = current_rates[luce_key]

        if luce_rate.get('energia') is not None:
            if luce_rate['energia'] < user_rates['luce_energia']:
                savings['luce_energia'] = {
                    'attuale': user_rates['luce_energia'],
                    'nuova': luce_rate['energia'],
                    'risparmio': user_rates['luce_energia'] - luce_rate['energia']
                }
                savings['has_savings'] = True
            elif luce_rate['energia'] > user_rates['luce_energia']:
                savings['luce_energia_worse'] = True

        if luce_rate.get('commercializzazione') is not None:
            if luce_rate['commercializzazione'] < user_rates['luce_comm']:
                savings['luce_comm'] = {
                    'attuale': user_rates['luce_comm'],
                    'nuova': luce_rate['commercializzazione'],
                    'risparmio': user_rates['luce_comm'] - luce_rate['commercializzazione']
                }
                savings['has_savings'] = True
            elif luce_rate['commercializzazione'] > user_rates['luce_comm']:
                savings['luce_comm_worse'] = True

    # Controlla gas (solo se l'utente ha il gas)
    if user_rates.get('gas_energia') is not None:
        gas_tipo = user_rates['gas_tipo']

        # Mappa tipo → chiave nel current_rates
        gas_key = None
        if gas_tipo == "Fissa":
            gas_key = 'gas_fisso'
        elif gas_tipo == "Variabile Monoraria":
            gas_key = 'gas_variabile'

        if gas_key and current_rates.get(gas_key):
            gas_rate = current_rates[gas_key]

            if gas_rate.get('energia') is not None:
                if gas_rate['energia'] < user_rates['gas_energia']:
                    savings['gas_energia'] = {
                        'attuale': user_rates['gas_energia'],
                        'nuova': gas_rate['energia'],
                        'risparmio': user_rates['gas_energia'] - gas_rate['energia']
                    }
                    savings['has_savings'] = True
                elif gas_rate['energia'] > user_rates['gas_energia']:
                    savings['gas_energia_worse'] = True

            if gas_rate.get('commercializzazione') is not None and user_rates.get('gas_comm') is not None:
                if gas_rate['commercializzazione'] < user_rates['gas_comm']:
                    savings['gas_comm'] = {
                        'attuale': user_rates['gas_comm'],
                        'nuova': gas_rate['commercializzazione'],
                        'risparmio': user_rates['gas_comm'] - gas_rate['commercializzazione']
                    }
                    savings['has_savings'] = True
                elif gas_rate['commercializzazione'] > user_rates['gas_comm']:
                    savings['gas_comm_worse'] = True

    # Determina se è un caso "mixed" (una componente migliora, l'altra peggiora)
    # Per luce
    luce_has_improvement = savings['luce_energia'] or savings['luce_comm']
    luce_has_worsening = savings['luce_energia_worse'] or savings['luce_comm_worse']

    # Per gas
    gas_has_improvement = savings['gas_energia'] or savings['gas_comm']
    gas_has_worsening = savings['gas_energia_worse'] or savings['gas_comm_worse']

    # È mixed se almeno una componente (luce o gas) ha sia miglioramenti che peggioramenti
    if (luce_has_improvement and luce_has_worsening) or (gas_has_improvement and gas_has_worsening):
        savings['is_mixed'] = True

    return savings

def format_notification(savings, user_rates, current_rates):
    """Formatta messaggio di notifica"""
    # Header diverso per caso mixed vs tutto migliorato
    if savings['is_mixed']:
        message = "⚖️ <b>Aggiornamento tariffe Octopus Energy</b>\n"
        message += "OctoTracker ha rilevato una variazione nelle tariffe, ma non è detto che sia automaticamente più conveniente: una delle due componenti è migliorata, l'altra è aumentata.\n\n"
    else:
        message = "⚡️ <b>Buone notizie!</b>\n"
        message += "OctoTracker ha trovato una tariffa Octopus Energy più conveniente rispetto a quella che hai attiva.\n\n"

    # Mostra Luce SOLO se c'è almeno un miglioramento
    if savings['luce_energia'] or savings['luce_comm']:
        luce_tipo = savings['luce_tipo']

        # Determina label e unità in base al tipo
        if luce_tipo == "Fissa":
            luce_label = "Prezzo fisso"
        elif luce_tipo == "Variabile Monoraria":
            luce_label = "Spread (PUN +)"
        else:  # Variabile Trioraria
            luce_label = "Spread (PUN +)"

        message += "💡 <b>Luce:</b>\n"

        # Formatta energia con max_decimals=4 per spread (es. 0,0088)
        user_energia = format_number(user_rates['luce_energia'], max_decimals=4)
        user_comm = format_number(user_rates['luce_comm'], max_decimals=2)

        message += f"Tua tariffa: {luce_label} {user_energia} €/kWh, Comm. {user_comm} €/anno\n"

        # Ottieni tariffe nuove da current_rates
        luce_key = None
        if luce_tipo == "Fissa":
            luce_key = 'luce_fissa'
        elif luce_tipo == "Variabile Monoraria":
            luce_key = 'luce_variabile_mono'
        else:  # Variabile Trioraria
            luce_key = 'luce_variabile_multi'

        if luce_key and current_rates.get(luce_key):
            energia_new = current_rates[luce_key]['energia']
            comm_new = current_rates[luce_key]['commercializzazione']

            energia_formatted = format_number(energia_new, max_decimals=4)
            comm_formatted = format_number(comm_new, max_decimals=2)

            if savings['luce_energia']:
                energia_str = f"<b>{energia_formatted} €/kWh</b>"
            elif savings['luce_energia_worse']:
                energia_str = f"<u>{energia_formatted} €/kWh</u>"
            else:
                energia_str = f"{energia_formatted} €/kWh"

            if savings['luce_comm']:
                comm_str = f"<b>{comm_formatted} €/anno</b>"
            elif savings['luce_comm_worse']:
                comm_str = f"<u>{comm_formatted} €/anno</u>"
            else:
                comm_str = f"{comm_formatted} €/anno"

            message += f"Nuova tariffa: {luce_label} {energia_str}, Comm. {comm_str}\n\n"

    # Mostra Gas SOLO se c'è almeno un miglioramento
    if user_rates.get('gas_energia') is not None and (savings['gas_energia'] or savings['gas_comm']):
        gas_tipo = savings['gas_tipo']

        # Determina label in base al tipo
        if gas_tipo == "Fissa":
            gas_label = "Prezzo fisso"
        else:  # Variabile Monoraria
            gas_label = "Spread (PSV +)"

        message += "🔥 <b>Gas:</b>\n"

        # Formatta energia con max_decimals=4 per spread
        user_gas_energia = format_number(user_rates['gas_energia'], max_decimals=4)
        user_gas_comm = format_number(user_rates['gas_comm'], max_decimals=2)

        message += f"Tua tariffa: {gas_label} {user_gas_energia} €/Smc, Comm. {user_gas_comm} €/anno\n"

        # Ottieni tariffe nuove da current_rates
        gas_key = None
        if gas_tipo == "Fissa":
            gas_key = 'gas_fisso'
        else:  # Variabile Monoraria
            gas_key = 'gas_variabile'

        if gas_key and current_rates.get(gas_key):
            energia_new = current_rates[gas_key]['energia']
            comm_new = current_rates[gas_key]['commercializzazione']

            energia_formatted = format_number(energia_new, max_decimals=4)
            comm_formatted = format_number(comm_new, max_decimals=2)

            if savings['gas_energia']:
                energia_str = f"<b>{energia_formatted} €/Smc</b>"
            elif savings['gas_energia_worse']:
                energia_str = f"<u>{energia_formatted} €/Smc</u>"
            else:
                energia_str = f"{energia_formatted} €/Smc"

            if savings['gas_comm']:
                comm_str = f"<b>{comm_formatted} €/anno</b>"
            elif savings['gas_comm_worse']:
                comm_str = f"<u>{comm_formatted} €/anno</u>"
            else:
                comm_str = f"{comm_formatted} €/anno"

            message += f"Nuova tariffa: {gas_label} {energia_str}, Comm. {comm_str}\n\n"

    # Footer diverso per caso mixed
    if savings['is_mixed']:
        message += "📊 In questi casi la convenienza dipende dai tuoi consumi.\n"
        message += "Ti consiglio di fare una verifica in base ai kWh/Smc che usi mediamente ogni anno, puoi trovare i dati nelle tue bollette.\n\n"

    message += "🔧 Se vuoi aggiornare le tariffe che hai registrato, puoi farlo in qualsiasi momento con il comando /update.\n\n"
    message += "🔗 Maggiori info: https://octopusenergy.it/le-nostre-tariffe\n\n"
    message += "☕️ Se pensi che questo bot ti sia utile, puoi offrirmi un caffè su ko-fi.com/dstmrk — grazie di cuore! 💙"

    return message

async def send_notification(bot, user_id, message):
    """Invia notifica Telegram"""
    try:
        await bot.send_message(chat_id=user_id, text=message, parse_mode='HTML')
        return True
    except Exception as e:
        print(f"❌ Errore invio messaggio a {user_id}: {e}")
        return False

async def check_and_notify_users(bot_token: str):
    """Controlla tariffe e invia notifiche (chiamata da bot.py)"""
    print("🔍 Inizio controllo tariffe...")

    # Carica dati
    users = load_json(USERS_FILE)
    current_rates = load_json(RATES_FILE)

    if not users:
        print("⚠️  Nessun utente registrato")
        return

    if not current_rates:
        print("❌ Nessuna tariffa disponibile. Esegui prima scraper.py")
        return

    # Inizializza bot
    bot = Bot(token=bot_token)

    # Controlla ogni utente
    notifications_sent = 0
    users_updated = False

    for user_id, user_rates in users.items():
        print(f"📊 Controllo utente {user_id}...")

        savings = check_better_rates(user_rates, current_rates)

        if savings['has_savings']:
            # Costruisci oggetto con tariffe Octopus attuali (in base al tipo utente)
            current_octopus = {}

            # Determina chiave luce in base al tipo dell'utente
            luce_tipo = user_rates['luce_tipo']
            luce_key = None
            if luce_tipo == "Fissa":
                luce_key = 'luce_fissa'
            elif luce_tipo == "Variabile Monoraria":
                luce_key = 'luce_variabile_mono'
            elif luce_tipo == "Variabile Trioraria":
                luce_key = 'luce_variabile_multi'

            if luce_key and current_rates.get(luce_key):
                current_octopus['luce_energia'] = current_rates[luce_key]['energia']
                current_octopus['luce_comm'] = current_rates[luce_key]['commercializzazione']

            # Aggiungi gas solo se l'utente ce l'ha
            if user_rates.get('gas_energia') is not None:
                gas_tipo = user_rates['gas_tipo']
                gas_key = None
                if gas_tipo == "Fissa":
                    gas_key = 'gas_fisso'
                elif gas_tipo == "Variabile Monoraria":
                    gas_key = 'gas_variabile'

                if gas_key and current_rates.get(gas_key):
                    current_octopus['gas_energia'] = current_rates[gas_key]['energia']
                    current_octopus['gas_comm'] = current_rates[gas_key]['commercializzazione']

            # Controlla se abbiamo già notificato queste stesse tariffe
            last_notified = user_rates.get('last_notified_rates', {})

            if last_notified == current_octopus:
                print(f"  ⏭️  Tariffe migliori già notificate in precedenza, skip")
            else:
                # Tariffe diverse o prima notifica - invia messaggio
                message = format_notification(savings, user_rates, current_rates)
                success = await send_notification(bot, user_id, message)
                if success:
                    # Aggiorna last_notified_rates per questo utente
                    users[user_id]['last_notified_rates'] = current_octopus
                    users_updated = True
                    notifications_sent += 1
                    print(f"  ✅ Notifica inviata e tariffe salvate")
        else:
            print(f"  ℹ️  Nessun risparmio trovato")

    # Salva users.json se ci sono stati aggiornamenti
    if users_updated:
        save_users(users)
        print(f"💾 Dati utenti aggiornati")

    print(f"\n✅ Controllo completato. Notifiche inviate: {notifications_sent}/{len(users)}")

async def main():
    """Main per esecuzione standalone"""
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")
    await check_and_notify_users(token)

if __name__ == '__main__':
    import asyncio
    asyncio.run(main())
