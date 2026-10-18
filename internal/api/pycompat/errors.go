package pycompat

import "errors"

// Error è un'eccezione Python: Class è il nome della classe (es. "ValueError")
// e Error() restituisce str(e), cioè il solo messaggio.
type Error struct {
	Class   string
	Message string
}

// Error restituisce str(e).
func (e *Error) Error() string { return e.Message }

// NewError crea un'eccezione della classe indicata.
func NewError(class, message string) *Error {
	return &Error{Class: class, Message: message}
}

// pyParents è la gerarchia (classe → superclasse) delle eccezioni usate.
var pyParents = map[string]string{
	"JSONDecodeError":    "ValueError",
	"UnicodeDecodeError": "ValueError",
	"ValueError":         "Exception",
	"TypeError":          "Exception",
	"RecursionError":     "RuntimeError",
	"RuntimeError":       "Exception",
	"FileNotFoundError":  "OSError",
	"IsADirectoryError":  "OSError",
	"NotADirectoryError": "OSError",
	"PermissionError":    "OSError",
	"OSError":            "Exception",
	"EOFError":           "Exception",
}

// IsInstance replica isinstance(err, class) per gli errori *Error
// (attraversando la catena di wrapping): per esempio un JSONDecodeError è
// anche un ValueError. Ogni errore non nullo è un "Exception".
func IsInstance(err error, class string) bool {
	if err == nil {
		return false
	}
	if class == "Exception" {
		return true
	}
	var pe *Error
	if !errors.As(err, &pe) {
		return false
	}
	for c := pe.Class; c != ""; c = pyParents[c] {
		if c == class {
			return true
		}
	}
	return false
}
