#!/usr/bin/env python3
"""
Bot Telegram per registrare le tariffe degli utenti
"""
import os
import json
from pathlib import Path
from telegram import Update, InlineKeyboardButton, InlineKeyboardMarkup
from telegram.ext import Application, CommandHandler, MessageHandler, CallbackQueryHandler, filters, ContextTypes, ConversationHandler
from dotenv import load_dotenv

load_dotenv()

# Stati conversazione
LUCE_ENERGIA, LUCE_COMM, HA_GAS, GAS_ENERGIA, GAS_COMM = range(5)

# File dati
DATA_DIR = Path(__file__).parent / "data"
USERS_FILE = DATA_DIR / "users.json"

def load_users():
    """Carica dati utenti"""
    if USERS_FILE.exists():
        with open(USERS_FILE, 'r') as f:
            return json.load(f)
    return {}

def save_users(users):
    """Salva dati utenti"""
    DATA_DIR.mkdir(exist_ok=True)
    with open(USERS_FILE, 'w') as f:
        json.dump(users, f, indent=2)

async def start(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Avvia registrazione tariffe"""
    await update.message.reply_text(
        "👋 Ciao! Ti aiuto a monitorare le tariffe Octopus Energy.\n\n"
        "Inserisci le tue tariffe attuali.\n\n"
        "💡 **LUCE - Costo Energia** (€/kWh)\n"
        "Esempio: 0.12"
    )
    return LUCE_ENERGIA

async def luce_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia luce"""
    try:
        context.user_data['luce_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "💡 **LUCE - Costo Commercializzazione** (€/anno)\n"
            "Esempio: 96.00"
        )
        return LUCE_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.12)")
        return LUCE_ENERGIA

async def luce_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo commercializzazione luce e chiedi se ha gas"""
    try:
        context.user_data['luce_comm'] = float(update.message.text.replace(',', '.'))

        # Chiedi se ha anche il gas con bottoni
        keyboard = [
            [
                InlineKeyboardButton("✅ Sì", callback_data="gas_si"),
                InlineKeyboardButton("❌ No", callback_data="gas_no")
            ]
        ]
        reply_markup = InlineKeyboardMarkup(keyboard)

        await update.message.reply_text(
            "🔥 **Hai anche il gas?**",
            reply_markup=reply_markup
        )
        return HA_GAS
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 96.50)")
        return LUCE_COMM

async def ha_gas(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Gestisci risposta se ha gas"""
    query = update.callback_query
    await query.answer()

    if query.data == "gas_si":
        await query.edit_message_text(
            "🔥 **GAS - Costo Energia** (€/Smc)\n"
            "Esempio: 0.45"
        )
        return GAS_ENERGIA
    else:  # gas_no
        # Salva solo luce
        return await salva_e_conferma(query, context, solo_luce=True)

async def gas_energia(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva costo energia gas"""
    try:
        context.user_data['gas_energia'] = float(update.message.text.replace(',', '.'))
        await update.message.reply_text(
            "🔥 **GAS - Costo Commercializzazione** (€/anno)\n"
            "Esempio: 144.00"
        )
        return GAS_COMM
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 0.45)")
        return GAS_ENERGIA

async def gas_comm(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Salva gas e conferma"""
    try:
        context.user_data['gas_comm'] = float(update.message.text.replace(',', '.'))
        return await salva_e_conferma(update, context, solo_luce=False)
    except ValueError:
        await update.message.reply_text("❌ Inserisci un numero valido (es: 144.00)")
        return GAS_COMM

async def salva_e_conferma(update_or_query, context: ContextTypes.DEFAULT_TYPE, solo_luce: bool):
    """Salva dati utente e mostra conferma"""
    users = load_users()

    # Gestisci sia Update che CallbackQuery
    if hasattr(update_or_query, 'message') and hasattr(update_or_query.message, 'reply_text'):
        # È un Update normale
        user_id = str(update_or_query.effective_user.id)
        send_message = lambda text: update_or_query.message.reply_text(text)
    else:
        # È una CallbackQuery
        user_id = str(update_or_query.from_user.id)
        send_message = lambda text: update_or_query.edit_message_text(text)

    # Prepara dati da salvare
    user_data = {
        'luce_energia': context.user_data['luce_energia'],
        'luce_comm': context.user_data['luce_comm'],
    }

    if not solo_luce:
        user_data['gas_energia'] = context.user_data['gas_energia']
        user_data['gas_comm'] = context.user_data['gas_comm']
    else:
        user_data['gas_energia'] = None
        user_data['gas_comm'] = None

    users[user_id] = user_data
    save_users(users)

    # Messaggio di conferma
    messaggio = (
        "✅ **Dati registrati con successo!**\n\n"
        f"💡 **Luce:**\n"
        f"  - Energia: €{user_data['luce_energia']:.3f}/kWh\n"
        f"  - Commercializzazione: €{user_data['luce_comm']:.2f}/anno\n"
    )

    if not solo_luce:
        messaggio += (
            f"\n🔥 **Gas:**\n"
            f"  - Energia: €{user_data['gas_energia']:.3f}/Smc\n"
            f"  - Commercializzazione: €{user_data['gas_comm']:.2f}/anno\n"
        )

    messaggio += (
        "\n📬 Riceverai notifiche quando troverò tariffe più convenienti!\n\n"
        "Comandi disponibili:\n"
        "• /status - Visualizza i tuoi dati\n"
        "• /update - Aggiorna i tuoi dati\n"
        "• /remove - Cancella i tuoi dati"
    )

    await send_message(messaggio)
    return ConversationHandler.END

async def cancel(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Annulla registrazione"""
    await update.message.reply_text("❌ Registrazione annullata. Usa /start per ricominciare.")
    return ConversationHandler.END

async def status(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Mostra dati salvati"""
    users = load_users()
    user_id = str(update.effective_user.id)

    if user_id not in users:
        await update.message.reply_text(
            "❌ Non hai ancora salvato i tuoi dati.\n"
            "Usa /start per registrarti."
        )
        return

    data = users[user_id]
    messaggio = (
        "📊 **I tuoi dati:**\n\n"
        f"💡 **Luce:**\n"
        f"  - Energia: €{data['luce_energia']:.3f}/kWh\n"
        f"  - Commercializzazione: €{data['luce_comm']:.2f}/anno\n"
    )

    if data.get('gas_energia') is not None:
        messaggio += (
            f"\n🔥 **Gas:**\n"
            f"  - Energia: €{data['gas_energia']:.3f}/Smc\n"
            f"  - Commercializzazione: €{data['gas_comm']:.2f}/anno\n"
        )

    messaggio += "\nPer modificarli usa /update"

    await update.message.reply_text(messaggio)

async def remove_data(update: Update, context: ContextTypes.DEFAULT_TYPE):
    """Cancella dati utente"""
    users = load_users()
    user_id = str(update.effective_user.id)

    if user_id in users:
        del users[user_id]
        save_users(users)
        await update.message.reply_text(
            "✅ I tuoi dati sono stati cancellati.\n"
            "Usa /start se vuoi registrarti nuovamente."
        )
    else:
        await update.message.reply_text(
            "❌ Non hai dati da cancellare.\n"
            "Usa /start per registrarti."
        )

def main():
    """Avvia il bot"""
    token = os.getenv('TELEGRAM_BOT_TOKEN')
    if not token:
        raise ValueError("TELEGRAM_BOT_TOKEN non impostato in .env")

    app = Application.builder().token(token).build()

    # Handler conversazione registrazione
    conv_handler = ConversationHandler(
        entry_points=[
            CommandHandler('start', start),
            CommandHandler('update', start)
        ],
        states={
            LUCE_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_energia)],
            LUCE_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, luce_comm)],
            HA_GAS: [CallbackQueryHandler(ha_gas)],
            GAS_ENERGIA: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_energia)],
            GAS_COMM: [MessageHandler(filters.TEXT & ~filters.COMMAND, gas_comm)],
        },
        fallbacks=[CommandHandler('cancel', cancel)]
    )

    app.add_handler(conv_handler)
    app.add_handler(CommandHandler('status', status))
    app.add_handler(CommandHandler('remove', remove_data))

    print("🤖 Bot avviato!")
    app.run_polling(allowed_updates=Update.ALL_TYPES)

if __name__ == '__main__':
    main()
