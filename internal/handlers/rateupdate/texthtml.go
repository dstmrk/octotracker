package rateupdate

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// Port di Message.text_html di python-telegram-bot 22.6 (Message._parse_html
// con urled=False, parse_message_entities, parse_message_entity) e di
// html.escape: ricostruisce l'HTML del messaggio della notifica a partire
// da testo ed entità, così la modifica dopo il click è identica byte per
// byte a quella di Python. Gli offset delle entità sono in unità UTF-16 e
// le porzioni di testo sono estratte con lo slicing di Python sui byte
// UTF-16-LE (indici negativi o oltre la fine compresi).

// ptbEntityTypes è MessageEntity.ALL_TYPES: le entità di altri tipi sono
// ignorate da parse_entities() (il loro testo resta come testo semplice).
var ptbEntityTypes = map[string]bool{
	"blockquote": true, "bold": true, "bot_command": true, "cashtag": true, "code": true,
	"custom_emoji": true, "email": true, "expandable_blockquote": true, "hashtag": true,
	"italic": true, "mention": true, "phone_number": true, "pre": true, "spoiler": true,
	"strikethrough": true, "text_link": true, "text_mention": true, "underline": true, "url": true,
}

// entityKey identifica un'entità come TelegramObject.__eq__/__hash__ di
// MessageEntity (_id_attrs = type, offset, length).
type entityKey struct {
	typ            string
	offset, length int64
}

// parsedEntity è una coppia (entità, testo) del dict di parse_entities().
type parsedEntity struct {
	entity gotgbot.MessageEntity
	text   []uint16 // testo dell'entità in UTF-16
}

func (p parsedEntity) key() entityKey {
	return entityKey{p.entity.Type, p.entity.Offset, p.entity.Length}
}

// htmlEscaper replica html.escape(s, quote=True).
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// pySliceBounds normalizza gli indici di uno slicing Python seq[start:stop]
// su una sequenza lunga n.
func pySliceBounds(n, start, stop int64) (int64, int64) {
	norm := func(i int64) int64 {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		} else if i > n {
			i = n
		}
		return i
	}
	lo, hi := norm(start), norm(stop)
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// sliceUTF16 replica utf_16_text[start:stop] con indici in byte (il testo
// UTF-16-LE ha due byte per unità).
func sliceUTF16(u []uint16, byteStart, byteStop int64) []uint16 {
	lo, hi := pySliceBounds(int64(len(u))*2, byteStart, byteStop)
	return u[lo/2 : hi/2]
}

// decodeUTF16 replica bytes.decode("utf-16-le") con errors="strict": i
// surrogati spaiati sollevano UnicodeDecodeError con il messaggio di
// CPython (posizioni in byte relative alla porzione decodificata).
func decodeUTF16(u []uint16) (string, error) {
	decodeError := func(i int, reason string) error {
		return fmt.Errorf("UnicodeDecodeError: 'utf-16-le' codec can't decode bytes in position %d-%d: %s", 2*i, 2*i+1, reason)
	}
	var sb strings.Builder
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c < 0xd800 || c > 0xdfff:
			sb.WriteRune(rune(c))
		case c <= 0xdbff: // surrogato alto
			if i+1 >= len(u) {
				return "", decodeError(i, "unexpected end of data")
			}
			if next := u[i+1]; next >= 0xdc00 && next <= 0xdfff {
				sb.WriteRune(utf16.DecodeRune(rune(c), rune(next)))
				i++
				continue
			}
			return "", decodeError(i, "illegal UTF-16 surrogate")
		default: // surrogato basso isolato
			return "", decodeError(i, "illegal encoding")
		}
	}
	return sb.String(), nil
}

// parseMessageEntities replica parse_message_entities(text, entities) con
// i tipi di default (ALL_TYPES): un dict {entità: testo} che deduplica le
// entità uguali (stesso tipo, offset e lunghezza) tenendo la prima.
func parseMessageEntities(u []uint16, entities []gotgbot.MessageEntity) ([]parsedEntity, error) {
	var out []parsedEntity
	index := map[entityKey]int{}
	for _, e := range entities {
		if !ptbEntityTypes[e.Type] {
			continue
		}
		text := sliceUTF16(u, e.Offset*2, (e.Offset+e.Length)*2)
		if _, err := decodeUTF16(text); err != nil {
			return nil, err
		}
		pe := parsedEntity{entity: e, text: text}
		if i, dup := index[pe.key()]; dup {
			out[i].text = text // il dict aggiorna il valore, la chiave resta la prima
			continue
		}
		index[pe.key()] = len(out)
		out = append(out, pe)
	}
	return out, nil
}

// textHTML replica Message.text_html. Con testo assente (None) Python
// solleva AttributeError: su text.encode se c'è almeno un'entità di tipo
// noto, altrimenti sul successivo .replace del risultato None.
func textHTML(msg *gotgbot.Message) (string, error) {
	if msg == nil {
		return "", attributeError("text_html")
	}
	if msg.Text == "" {
		for _, e := range msg.Entities {
			if ptbEntityTypes[e.Type] {
				return "", attributeError("encode")
			}
		}
		return "", attributeError("replace")
	}
	u := utf16.Encode([]rune(msg.Text))
	entities, err := parseMessageEntities(u, msg.Entities)
	if err != nil {
		return "", err
	}
	return parseHTML(u, entities, 0)
}

// parseHTML replica Message._parse_html(message_text, entities, urled=False,
// offset): text è il testo (UTF-16) del messaggio o dell'entità padre,
// offset la posizione del padre nel messaggio.
func parseHTML(text []uint16, entities []parsedEntity, offset int64) (string, error) {
	var sb strings.Builder
	lastOffset := int64(0)

	sorted := append([]parsedEntity(nil), entities...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].entity.Offset < sorted[j].entity.Offset })
	var parsed []entityKey

	for _, pe := range sorted {
		entity := pe.entity
		if containsKey(parsed, pe.key()) {
			continue
		}

		var nested []parsedEntity
		for _, e := range sorted {
			if e.entity.Offset >= entity.Offset && e.entity.Offset+e.entity.Length <= entity.Offset+entity.Length && e.key() != pe.key() {
				nested = append(nested, e)
			}
		}
		for _, e := range nested {
			parsed = append(parsed, e.key())
		}

		var escapedText string
		if len(nested) > 0 {
			var err error
			if escapedText, err = parseHTML(pe.text, nested, entity.Offset); err != nil {
				return "", err
			}
		} else {
			s, err := decodeUTF16(pe.text)
			if err != nil {
				return "", err
			}
			escapedText = htmlEscaper.Replace(s)
		}

		var insert string
		switch entity.Type {
		case "text_link":
			insert = `<a href="` + pyOptional(entity.Url) + `">` + escapedText + "</a>"
		case "text_mention":
			if entity.User != nil {
				insert = `<a href="tg://user?id=` + strconv.FormatInt(entity.User.Id, 10) + `">` + escapedText + "</a>"
			} else {
				insert = escapedText
			}
		case "blockquote":
			insert = "<blockquote>" + escapedText + "</blockquote>"
		case "expandable_blockquote":
			insert = "<blockquote expandable>" + escapedText + "</blockquote>"
		case "bold":
			insert = "<b>" + escapedText + "</b>"
		case "italic":
			insert = "<i>" + escapedText + "</i>"
		case "code":
			insert = "<code>" + escapedText + "</code>"
		case "pre":
			if entity.Language != "" {
				insert = `<pre><code class="` + entity.Language + `">` + escapedText + "</code></pre>"
			} else {
				insert = "<pre>" + escapedText + "</pre>"
			}
		case "underline":
			insert = "<u>" + escapedText + "</u>"
		case "strikethrough":
			insert = "<s>" + escapedText + "</s>"
		case "spoiler":
			insert = `<span class="tg-spoiler">` + escapedText + "</span>"
		case "custom_emoji":
			insert = `<tg-emoji emoji-id="` + pyOptional(entity.CustomEmojiId) + `">` + escapedText + "</tg-emoji>"
		default: // url (urled=False), mention, hashtag, ...
			insert = escapedText
		}

		// Il testo fuori dalle entità va comunque escapato (anche dentro
		// un'entità padre, di cui fa parte)
		gap, err := decodeUTF16(sliceUTF16(text, lastOffset*2, (entity.Offset-offset)*2))
		if err != nil {
			return "", err
		}
		sb.WriteString(htmlEscaper.Replace(gap))
		sb.WriteString(insert)

		lastOffset = entity.Offset - offset + entity.Length
	}

	tail, err := decodeUTF16(sliceUTF16(text, lastOffset*2, int64(len(text))*2))
	if err != nil {
		return "", err
	}
	sb.WriteString(htmlEscaper.Replace(tail))
	return sb.String(), nil
}

func containsKey(keys []entityKey, k entityKey) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

// pyOptional replica f"{value}" per un attributo opzionale: un campo
// assente (stringa vuota per gotgbot) è None in PTB.
func pyOptional(s string) string {
	if s == "" {
		return "None"
	}
	return s
}
