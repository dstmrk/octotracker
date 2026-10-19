package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dstmrk/octotracker/internal/logging"
)

// Port del parser di python-dotenv (v1.2): load_dotenv(path) con i default
// usati dalla versione Python (override=False, interpolate=True).
//
// Le espressioni regolari sono le stesse di dotenv/parser.py; \s di Python
// (str.isspace, Unicode) non coincide con \s di Go (solo ASCII), quindi le
// classi di spazi sono esplicite.

// dotenvLogger replica logging.getLogger("dotenv.main").
var dotenvLogger = logging.Logger("dotenv.main")

const (
	// pyWS: caratteri di \s in Python (str.isspace()).
	pyWS = `\t\n\v\f\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	// pyHWS: [^\S\r\n] in Python, cioè gli spazi esclusi \r e \n.
	pyHWS = `\t\v\f\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
)

func anchored(pattern string) *regexp.Regexp {
	return regexp.MustCompile(`\A(?:` + pattern + `)`)
}

var (
	reMultilineWhitespace = anchored(`[` + pyWS + `]*`)
	reExport              = anchored(`(?:export[` + pyHWS + `]+)?`)
	reSingleQuotedKey     = anchored(`'([^']+)'`)
	reUnquotedKey         = anchored(`([^=#` + pyWS + `]+)`)
	reWhitespace          = anchored(`[` + pyHWS + `]*`)
	reEqualSign           = anchored(`(=[` + pyHWS + `]*)`)
	reSingleQuotedValue   = anchored(`'((?:\\'|[^'])*)'`)
	reDoubleQuotedValue   = anchored(`"((?:\\"|[^"])*)"`)
	reUnquotedValue       = anchored(`([^\r\n]*)`)
	reComment             = anchored(`(?:[` + pyHWS + `]*#[^\r\n]*)?`)
	reEndOfLine           = anchored(`[` + pyHWS + `]*(?:\r\n|\n|\r|$)`)
	reRestOfLine          = anchored(`[^\r\n]*(?:\r|\n|\r\n)?`)

	reDoubleQuoteEscapes = regexp.MustCompile(`\\[\\'"abfnrtv]`)
	reSingleQuoteEscapes = regexp.MustCompile(`\\[\\']`)
	reInlineComment      = regexp.MustCompile(`[` + pyWS + `]+#.*`)
	rePosixVariable      = regexp.MustCompile(`\$\{([^}:]*)(?::-([^}]*))?\}`)
)

// errDotenvParse corrisponde a dotenv.parser.Error.
var errDotenvParse = errors.New("dotenv: pattern not found")

// dotenvBinding corrisponde a dotenv.parser.Binding (nil = None).
type dotenvBinding struct {
	key   *string
	value *string
	line  int
	err   bool
}

// dotenvReader corrisponde a dotenv.parser.Reader.
type dotenvReader struct {
	s        string
	pos      int
	line     int
	markLine int
}

func (r *dotenvReader) hasNext() bool { return r.pos < len(r.s) }

func (r *dotenvReader) peek() string {
	if !r.hasNext() {
		return ""
	}
	_, size := utf8.DecodeRuneInString(r.s[r.pos:])
	return r.s[r.pos : r.pos+size]
}

func (r *dotenvReader) readRegex(re *regexp.Regexp) ([]string, error) {
	loc := re.FindStringSubmatchIndex(r.s[r.pos:])
	if loc == nil {
		return nil, errDotenvParse
	}
	groups := make([]string, 0, len(loc)/2-1)
	for i := 2; i < len(loc); i += 2 {
		if loc[i] < 0 {
			groups = append(groups, "")
			continue
		}
		groups = append(groups, r.s[r.pos+loc[i]:r.pos+loc[i+1]])
	}
	consumed := r.s[r.pos : r.pos+loc[1]]
	r.line += strings.Count(consumed, "\n")
	r.pos += loc[1]
	return groups, nil
}

func decodeEscapes(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		switch m[1] {
		case 'a':
			return "\a"
		case 'b':
			return "\b"
		case 'f':
			return "\f"
		case 'n':
			return "\n"
		case 'r':
			return "\r"
		case 't':
			return "\t"
		case 'v':
			return "\v"
		default: // \\ \' \"
			return m[1:]
		}
	})
}

func parseKey(r *dotenvReader) (*string, error) {
	var re *regexp.Regexp
	switch r.peek() {
	case "#":
		return nil, nil
	case "'":
		re = reSingleQuotedKey
	default:
		re = reUnquotedKey
	}
	g, err := r.readRegex(re)
	if err != nil {
		return nil, err
	}
	return &g[0], nil
}

func parseUnquotedValue(r *dotenvReader) (string, error) {
	g, err := r.readRegex(reUnquotedValue)
	if err != nil {
		return "", err
	}
	part := reInlineComment.ReplaceAllString(g[0], "")
	return strings.TrimRightFunc(part, pyIsSpace), nil
}

func parseValue(r *dotenvReader) (string, error) {
	switch r.peek() {
	case "'":
		g, err := r.readRegex(reSingleQuotedValue)
		if err != nil {
			return "", err
		}
		return decodeEscapes(reSingleQuoteEscapes, g[0]), nil
	case `"`:
		g, err := r.readRegex(reDoubleQuotedValue)
		if err != nil {
			return "", err
		}
		return decodeEscapes(reDoubleQuoteEscapes, g[0]), nil
	case "", "\n", "\r":
		return "", nil
	default:
		return parseUnquotedValue(r)
	}
}

func parseBinding(r *dotenvReader) dotenvBinding {
	r.markLine = r.line
	b, err := func() (dotenvBinding, error) {
		if _, err := r.readRegex(reMultilineWhitespace); err != nil {
			return dotenvBinding{}, err
		}
		if !r.hasNext() {
			return dotenvBinding{line: r.markLine}, nil
		}
		if _, err := r.readRegex(reExport); err != nil {
			return dotenvBinding{}, err
		}
		key, err := parseKey(r)
		if err != nil {
			return dotenvBinding{}, err
		}
		if _, err := r.readRegex(reWhitespace); err != nil {
			return dotenvBinding{}, err
		}
		var value *string
		if r.peek() == "=" {
			if _, err := r.readRegex(reEqualSign); err != nil {
				return dotenvBinding{}, err
			}
			v, err := parseValue(r)
			if err != nil {
				return dotenvBinding{}, err
			}
			value = &v
		}
		if _, err := r.readRegex(reComment); err != nil {
			return dotenvBinding{}, err
		}
		if _, err := r.readRegex(reEndOfLine); err != nil {
			return dotenvBinding{}, err
		}
		return dotenvBinding{key: key, value: value, line: r.markLine}, nil
	}()
	if err != nil {
		_, _ = r.readRegex(reRestOfLine) // corrisponde sempre (anche vuoto)
		return dotenvBinding{line: r.markLine, err: true}
	}
	return b
}

// parseDotenv corrisponde a parse_stream + DotEnv.parse: restituisce le
// coppie chiave/valore nell'ordine del file (valore nil per "KEY" senza "=")
// e logga un warning per ogni istruzione non valida.
func parseDotenv(content string) []dotenvBinding {
	r := &dotenvReader{s: content, line: 1}
	var out []dotenvBinding
	for r.hasNext() {
		b := parseBinding(r)
		if b.err {
			dotenvLogger.Warn(fmt.Sprintf("python-dotenv could not parse statement starting at line %d", b.line))
		}
		if b.key != nil {
			out = append(out, b)
		}
	}
	return out
}

// resolveDotenvVariables corrisponde a resolve_variables(values, override=False):
// espande ${VAR} e ${VAR:-default}, dando precedenza all'ambiente del
// processo rispetto ai valori già letti dal file. Restituisce le chiavi in
// ordine di prima apparizione e i valori risolti.
func resolveDotenvVariables(bindings []dotenvBinding) ([]string, map[string]*string) {
	var order []string
	newValues := map[string]*string{}

	lookup := func(name, def string) string {
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if v, ok := newValues[name]; ok {
			if v == nil {
				return ""
			}
			return *v
		}
		return def
	}

	for _, b := range bindings {
		var result *string
		if b.value != nil {
			res := interpolateDotenv(*b.value, lookup)
			result = &res
		}
		if _, seen := newValues[*b.key]; !seen {
			order = append(order, *b.key)
		}
		newValues[*b.key] = result
	}
	return order, newValues
}

// interpolateDotenv sostituisce in value ogni ${VAR} o ${VAR:-default} con
// lookup(VAR, default).
func interpolateDotenv(value string, lookup func(name, def string) string) string {
	var sb strings.Builder
	cursor := 0
	for _, m := range rePosixVariable.FindAllStringSubmatchIndex(value, -1) {
		sb.WriteString(value[cursor:m[0]])
		def := ""
		if m[4] >= 0 {
			def = value[m[4]:m[5]]
		}
		sb.WriteString(lookup(value[m[2]:m[3]], def))
		cursor = m[1]
	}
	sb.WriteString(value[cursor:])
	return sb.String()
}

// LoadDotEnv carica un file .env (se esiste) senza sovrascrivere le variabili
// già presenti nell'ambiente, come load_dotenv() di python-dotenv con
// override=False e interpolate=True.
//
// Supporta righe KEY=VALUE, commenti con #, prefisso "export ", chiavi e
// valori tra virgolette singole o doppie (anche su più righe, con le stesse
// sequenze di escape), commenti in linea dopo uno spazio e l'espansione di
// ${VAR} / ${VAR:-default}. Le istruzioni non valide vengono saltate con un
// warning. Se PYTHON_DOTENV_DISABLED è "1", "true", "t", "yes" o "y" non
// carica nulla. Un file che non è UTF-8 valido produce un errore (come
// UnicodeDecodeError in Python).
func LoadDotEnv(path string) error {
	if dotenvDisabled() {
		dotenvLogger.Debug("python-dotenv: .env loading disabled by PYTHON_DOTENV_DISABLED environment variable")
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || !(info.Mode().IsRegular() || info.Mode()&os.ModeNamedPipe != 0) {
		return nil // _is_file_or_fifo: file assente → nessuna variabile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("%s: contenuto non UTF-8", path)
	}
	// Python legge in modalità testo con universal newlines
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	order, values := resolveDotenvVariables(parseDotenv(content))
	for _, key := range order {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if v := values[key]; v != nil {
			if err := os.Setenv(key, *v); err != nil {
				return fmt.Errorf("impostazione %q: %w", key, err)
			}
		}
	}
	return nil
}

// dotenvDisabled corrisponde a _load_dotenv_disabled().
func dotenvDisabled() bool {
	v, ok := os.LookupEnv("PYTHON_DOTENV_DISABLED")
	if !ok {
		return false
	}
	for _, truthy := range []string{"1", "true", "t", "yes", "y"} {
		if strings.EqualFold(v, truthy) {
			return true
		}
	}
	return false
}
