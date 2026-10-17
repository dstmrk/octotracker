package database

// Porting di tests/test_database_errors.py: rami di gestione errori
// database (in Python sqlite3.Error simulato con patch di get_connection,
// qui con un database chiuso o un file non valido).

import (
	"os"
	"path/filepath"
	"testing"
)

var validUserData = User{
	Luce: Utility{Tipo: "fissa", Fascia: "monoraria", Energia: 0.12, Commercializzazione: 72.0},
}

// TestInitDbErrors_SqliteErrorPropagates: init_db logga e rilancia su sqlite3.Error.
func TestInitDbErrors_SqliteErrorPropagates(t *testing.T) {
	logs := captureLogs(t)

	// File esistente che non è un database SQLite → errore all'esecuzione dello schema.
	path := filepath.Join(t.TempDir(), "test.db")
	if err := os.WriteFile(path, []byte("questo non è un database SQLite, ma testo qualsiasi....."), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		_ = s.Close()
		t.Fatal("Open doveva fallire su un file non SQLite")
	}
	assertLogContains(t, logs, "- database - ERROR - ❌ Errore database:", "- database - ERROR - ❌ Errore inizializzazione database:")

	// InitDB su database chiuso → errore rilanciato.
	if err := brokenStore(t).InitDB(); err == nil {
		t.Fatal("InitDB doveva fallire su database chiuso")
	}
}

// TestLoadUserErrors_SqliteErrorReturnsNone: load_user ritorna None su sqlite3.Error.
func TestLoadUserErrors_SqliteErrorReturnsNone(t *testing.T) {
	logs := captureLogs(t)
	if u := brokenStore(t).LoadUser("123"); u != nil {
		t.Fatalf("atteso nil, got %+v", u)
	}
	assertLogContains(t, logs, "❌ Errore database:", "❌ Errore caricamento utente 123:")
}

// TestSaveUserErrors_SqliteErrorReturnsFalse: save_user ritorna False su sqlite3.Error.
func TestSaveUserErrors_SqliteErrorReturnsFalse(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).SaveUser("123", validUserData) {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore database salvando 123:")
}

// TestRemoveUserErrors_SqliteErrorReturnsFalse: remove_user ritorna False su sqlite3.Error.
func TestRemoveUserErrors_SqliteErrorReturnsFalse(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).RemoveUser("123") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore rimozione utente 123:")
}

// TestUserExistsErrors_SqliteErrorReturnsFalse: user_exists ritorna False su sqlite3.Error.
func TestUserExistsErrors_SqliteErrorReturnsFalse(t *testing.T) {
	logs := captureLogs(t)
	if brokenStore(t).UserExists("123") {
		t.Fatal("atteso false")
	}
	assertLogContains(t, logs, "❌ Errore controllo esistenza utente 123:")
}

// TestGetUserCountErrors_SqliteErrorReturnsZero: get_user_count ritorna 0 su sqlite3.Error.
func TestGetUserCountErrors_SqliteErrorReturnsZero(t *testing.T) {
	logs := captureLogs(t)
	if n := brokenStore(t).GetUserCount(); n != 0 {
		t.Fatalf("atteso 0, got %d", n)
	}
	assertLogContains(t, logs, "❌ Errore conteggio utenti:")
}
