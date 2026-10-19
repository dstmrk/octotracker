package rateupdate

import (
	"cmp"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// errNoText indica una notifica senza testo da modificare (messaggio
// inaccessibile o inline).
var errNoText = errors.New("messaggio della notifica senza testo")

// htmlEscaper è html.escape(s, quote=True) di Python, usato da
// python-telegram-bot: l'apice diventa &#x27;.
var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// plainEntities sono i tipi di entità noti che restano testo semplice; gli
// altri tipi sconosciuti vengono ignorati (MessageEntity.ALL_TYPES di
// python-telegram-bot 22.6, con htmlTags e i casi di wrapEntity).
var plainEntities = map[string]bool{
	"bot_command": true, "cashtag": true, "email": true, "hashtag": true,
	"mention": true, "phone_number": true, "url": true,
	"text_link": true, "text_mention": true, "pre": true, "custom_emoji": true,
}

// htmlTags sono i tag delle entità che avvolgono semplicemente il testo.
var htmlTags = map[string][2]string{
	"bold":                  {"<b>", "</b>"},
	"italic":                {"<i>", "</i>"},
	"code":                  {"<code>", "</code>"},
	"underline":             {"<u>", "</u>"},
	"strikethrough":         {"<s>", "</s>"},
	"spoiler":               {`<span class="tg-spoiler">`, "</span>"},
	"blockquote":            {"<blockquote>", "</blockquote>"},
	"expandable_blockquote": {"<blockquote expandable>", "</blockquote>"},
}

// textHTML ricostruisce l'HTML del messaggio della notifica dal testo e
// dalle entità, come Message.text_html di python-telegram-bot, così la
// modifica dopo il click mantiene la formattazione originale. Gli offset
// delle entità sono in unità UTF-16; le entità di tipo sconosciuto sono
// ignorate.
func textHTML(msg *gotgbot.Message) (string, error) {
	if msg == nil || msg.Text == "" {
		return "", errNoText
	}
	entities := slices.DeleteFunc(slices.Clone(msg.Entities), func(e gotgbot.MessageEntity) bool {
		_, tag := htmlTags[e.Type]
		return !tag && !plainEntities[e.Type]
	})
	slices.SortStableFunc(entities, func(a, b gotgbot.MessageEntity) int { return cmp.Compare(a.Offset, b.Offset) })
	return parseHTML(utf16.Encode([]rune(msg.Text)), entities, 0), nil
}

// parseHTML converte in HTML text (UTF-16), che nel messaggio inizia a
// offset, con le entità che contiene ordinate per offset. Ogni entità
// avvolge le entità annidate, convertite ricorsivamente.
func parseHTML(text []uint16, entities []gotgbot.MessageEntity, offset int64) string {
	var sb strings.Builder
	last := int64(0)
	parsed := make([]bool, len(entities))
	for i, e := range entities {
		if parsed[i] {
			continue
		}
		var nested []gotgbot.MessageEntity
		for j, n := range entities {
			if j != i && n.Offset >= e.Offset && n.Offset+n.Length <= e.Offset+e.Length {
				nested = append(nested, n)
				parsed[j] = true
			}
		}

		inner := sliceUTF16(text, e.Offset-offset, e.Offset-offset+e.Length)
		var escaped string
		if len(nested) > 0 {
			escaped = parseHTML(inner, nested, e.Offset)
		} else {
			escaped = escapeUTF16(inner)
		}

		// Il testo fuori dalle entità va comunque escapato
		sb.WriteString(escapeUTF16(sliceUTF16(text, last, e.Offset-offset)))
		sb.WriteString(wrapEntity(e, escaped))
		last = e.Offset - offset + e.Length
	}
	sb.WriteString(escapeUTF16(sliceUTF16(text, last, int64(len(text)))))
	return sb.String()
}

// wrapEntity avvolge il testo (già escapato) nel tag HTML dell'entità; le
// entità senza formattazione (url, mention, hashtag, ...) restano testo.
func wrapEntity(e gotgbot.MessageEntity, s string) string {
	switch e.Type {
	case "text_link":
		return `<a href="` + e.Url + `">` + s + "</a>"
	case "text_mention":
		if e.User != nil {
			return `<a href="tg://user?id=` + strconv.FormatInt(e.User.Id, 10) + `">` + s + "</a>"
		}
	case "pre":
		if e.Language != "" {
			return `<pre><code class="` + e.Language + `">` + s + "</code></pre>"
		}
		return "<pre>" + s + "</pre>"
	case "custom_emoji":
		return `<tg-emoji emoji-id="` + e.CustomEmojiId + `">` + s + "</tg-emoji>"
	}
	if tag, ok := htmlTags[e.Type]; ok {
		return tag[0] + s + tag[1]
	}
	return s
}

// sliceUTF16 restituisce u[start:stop] con gli indici limitati alla
// sequenza (vuota se stop precede start).
func sliceUTF16(u []uint16, start, stop int64) []uint16 {
	n := int64(len(u))
	start = min(max(start, 0), n)
	stop = min(max(stop, start), n)
	return u[start:stop]
}

// escapeUTF16 decodifica u ed esegue l'escape HTML.
func escapeUTF16(u []uint16) string {
	return htmlEscaper.Replace(string(utf16.Decode(u)))
}
