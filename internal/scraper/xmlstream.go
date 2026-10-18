package scraper

// Parser XML in streaming che replica DefusedET.fromstring + _remove_namespace
// di data_reader.py senza costruire l'intero albero: il documento viene letto
// token per token (xml.Decoder.RawToken) e solo i sottoalberi richiesti (le
// singole offerte) vengono materializzati, uno alla volta.
//
// encoding/xml non verifica alcune regole di buona forma che expat applica
// (radice unica, corrispondenza dei tag con RawToken, prefissi non dichiarati,
// attributi duplicati, posizione e sintassi della dichiarazione XML); qui
// vengono verificate esplicitamente, così un documento rifiutato da Python
// viene rifiutato anche in Go. Come defusedxml (forbid_entities=True), ogni
// dichiarazione <!ENTITY> nel DOCTYPE fa fallire il parsing con
// EntitiesForbidden; encoding/xml non espande comunque entità esterne né
// definite nel DTD, quindi XXE e "billion laughs" non sono possibili.

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	xmlNamespaceURL   = "http://www.w3.org/XML/1998/namespace"
	xmlnsNamespaceURL = "http://www.w3.org/2000/xmlns/"
)

// xmlSyntaxError è l'equivalente di xml.etree.ElementTree.ParseError.
type xmlSyntaxError struct {
	msg string
}

func (e *xmlSyntaxError) Error() string { return e.msg }

// element è un nodo dell'albero come lo vede data_reader.py dopo
// _remove_namespace: tag senza namespace, text (testo prima del primo figlio,
// nil se assente come None) e figli.
type element struct {
	tag      string
	text     *string
	children []*element

	textBuf  []byte
	textDone bool
}

// find replica elem.find("tag"): primo figlio diretto con quel tag.
func (e *element) find(tag string) *element {
	for _, c := range e.children {
		if c.tag == tag {
			return c
		}
	}
	return nil
}

// walkDescendants visita i discendenti (escluso e) in ordine di documento;
// si ferma quando fn restituisce false.
func (e *element) walkDescendants(fn func(*element) bool) bool {
	for _, c := range e.children {
		if !fn(c) || !c.walkDescendants(fn) {
			return false
		}
	}
	return true
}

// findDesc replica elem.find(".//tag"): primo discendente con quel tag.
func (e *element) findDesc(tag string) *element {
	var found *element
	e.walkDescendants(func(c *element) bool {
		if c.tag == tag {
			found = c
			return false
		}
		return true
	})
	return found
}

// findAllDesc replica elem.findall(".//tag").
func (e *element) findAllDesc(tag string) []*element {
	var out []*element
	e.walkDescendants(func(c *element) bool {
		if c.tag == tag {
			out = append(out, c)
		}
		return true
	})
	return out
}

// textOf replica `elem.text if elem is not None else None`.
func textOf(e *element) *string {
	if e == nil {
		return nil
	}
	return e.text
}

// closeText fissa elem.text: il testo raccolto prima del primo figlio
// (commenti e processing instruction esclusi), None se vuoto.
func (e *element) closeText() {
	if e.textDone {
		return
	}
	e.textDone = true
	if len(e.textBuf) > 0 {
		s := string(e.textBuf)
		e.text = &s
	}
	e.textBuf = nil
}

// ========== Scansione del documento ==========

// xmlScanner legge un documento XML token per token.
//
// capture decide, per ogni elemento che non sta già dentro un sottoalbero
// catturato, se materializzarne il sottoalbero (depth 1 = radice); onTree
// riceve ciascun sottoalbero completo.
type xmlScanner struct {
	capture func(tag string, depth int) bool
	onTree  func(*element)

	dec        *xml.Decoder
	stack      []openElement
	ns         []nsBinding
	build      []*element
	rootSeen   bool
	rootClosed bool
	doctype    bool
}

type openElement struct {
	name xml.Name // nome grezzo (prefisso in Space)
	nsN  int      // len(ns) prima delle dichiarazioni di questo elemento
}

type nsBinding struct {
	prefix, uri string
}

// scanXML esegue la scansione di r. Restituisce nil se il documento è ben
// formato, *xmlSyntaxError se è malformato (ParseError), *PyException per le
// eccezioni di defusedxml (EntitiesForbidden), oppure l'errore di lettura di r.
func scanXML(r io.Reader, capture func(tag string, depth int) bool, onTree func(*element)) error {
	s := &xmlScanner{capture: capture, onTree: onTree}
	return s.run(r)
}

func (s *xmlScanner) syntaxErr(what string) error {
	line, col := s.dec.InputPos()
	return &xmlSyntaxError{msg: fmt.Sprintf("%s: line %d, column %d", what, line, col-1)}
}

func (s *xmlScanner) run(r io.Reader) error {
	br := bufio.NewReader(r)
	if err := readXMLDecl(br); err != nil {
		return err
	}
	s.dec = xml.NewDecoder(br)
	s.dec.Strict = true
	for {
		tok, err := s.dec.RawToken()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s.atEOF()
			}
			var se *xml.SyntaxError
			if errors.As(err, &se) || strings.HasPrefix(err.Error(), "xml: ") {
				return &xmlSyntaxError{msg: err.Error()}
			}
			return err
		}
		if err := s.handle(tok); err != nil {
			return err
		}
	}
}

func (s *xmlScanner) atEOF() error {
	if !s.rootSeen || len(s.stack) > 0 {
		return s.syntaxErr("no element found")
	}
	return nil
}

func (s *xmlScanner) handle(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		return s.start(t)
	case xml.EndElement:
		return s.end(t)
	case xml.CharData:
		return s.charData(t)
	case xml.Comment:
		if !validXMLChars(t) {
			return s.syntaxErr("not well-formed (invalid token)")
		}
	case xml.ProcInst:
		if strings.EqualFold(t.Target, "xml") {
			return s.syntaxErr("XML or text declaration not at start of entity")
		}
		if !validXMLChars(t.Inst) {
			return s.syntaxErr("not well-formed (invalid token)")
		}
	case xml.Directive:
		return s.directive(t)
	}
	return nil
}

func (s *xmlScanner) lookupNS(prefix string) (string, bool) {
	switch prefix {
	case "xml":
		return xmlNamespaceURL, true
	case "xmlns":
		return "", false
	}
	for i := len(s.ns) - 1; i >= 0; i-- {
		if s.ns[i].prefix == prefix {
			return s.ns[i].uri, true
		}
	}
	return "", false
}

func (s *xmlScanner) start(t xml.StartElement) error {
	if s.rootClosed {
		return s.syntaxErr("junk after document element")
	}
	if strings.Contains(t.Name.Local, ":") {
		return s.syntaxErr("not well-formed (invalid token)")
	}
	nsN := len(s.ns)
	seen := make(map[xml.Name]bool, len(t.Attr))
	for _, a := range t.Attr {
		if strings.Contains(a.Name.Local, ":") {
			return s.syntaxErr("not well-formed (invalid token)")
		}
		if seen[a.Name] {
			return s.syntaxErr("duplicate attribute")
		}
		seen[a.Name] = true
		switch {
		case a.Name.Space == "xmlns":
			switch {
			case a.Name.Local == "xmlns":
				return s.syntaxErr("reserved prefix (xmlns) must not be declared or undeclared")
			case a.Name.Local == "xml":
				if a.Value != xmlNamespaceURL {
					return s.syntaxErr("reserved prefix (xml) must not be undeclared or bound to another namespace name")
				}
				continue
			case a.Value == "":
				return s.syntaxErr("must not undeclare prefix")
			case a.Value == xmlNamespaceURL || a.Value == xmlnsNamespaceURL:
				return s.syntaxErr("reserved namespace name must not be bound")
			}
			s.ns = append(s.ns, nsBinding{a.Name.Local, a.Value})
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			if a.Value == xmlNamespaceURL || a.Value == xmlnsNamespaceURL {
				return s.syntaxErr("reserved namespace name must not be bound")
			}
			s.ns = append(s.ns, nsBinding{"", a.Value})
		}
	}
	if t.Name.Space != "" {
		if _, ok := s.lookupNS(t.Name.Space); !ok {
			return s.syntaxErr("unbound prefix")
		}
	}
	expanded := make(map[xml.Name]bool, len(t.Attr))
	for _, a := range t.Attr {
		if a.Name.Space == "" || a.Name.Space == "xmlns" {
			continue
		}
		uri, ok := s.lookupNS(a.Name.Space)
		if !ok {
			return s.syntaxErr("unbound prefix")
		}
		key := xml.Name{Space: uri, Local: a.Name.Local}
		if expanded[key] {
			return s.syntaxErr("duplicate attribute")
		}
		expanded[key] = true
	}

	s.stack = append(s.stack, openElement{name: t.Name, nsN: nsN})
	s.rootSeen = true
	depth := len(s.stack)

	if n := len(s.build); n > 0 {
		parent := s.build[n-1]
		parent.closeText()
		child := &element{tag: t.Name.Local}
		parent.children = append(parent.children, child)
		s.build = append(s.build, child)
	} else if s.capture(t.Name.Local, depth) {
		s.build = append(s.build, &element{tag: t.Name.Local})
	}
	return nil
}

func (s *xmlScanner) end(t xml.EndElement) error {
	n := len(s.stack)
	if n == 0 || s.stack[n-1].name != t.Name {
		return s.syntaxErr("mismatched tag")
	}
	s.ns = s.ns[:s.stack[n-1].nsN]
	s.stack = s.stack[:n-1]
	if n == 1 {
		s.rootClosed = true
	}
	if b := len(s.build); b > 0 {
		e := s.build[b-1]
		e.closeText()
		s.build = s.build[:b-1]
		if b == 1 {
			s.onTree(e)
		}
	}
	return nil
}

func (s *xmlScanner) charData(t xml.CharData) error {
	if len(s.stack) == 0 {
		for _, c := range t {
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				if s.rootClosed {
					return s.syntaxErr("junk after document element")
				}
				return s.syntaxErr("not well-formed (invalid token)")
			}
		}
		return nil
	}
	if b := len(s.build); b > 0 {
		if e := s.build[b-1]; !e.textDone {
			e.textBuf = append(e.textBuf, t...)
		}
	}
	return nil
}

func (s *xmlScanner) directive(t xml.Directive) error {
	if s.rootClosed {
		return s.syntaxErr("junk after document element")
	}
	if s.rootSeen || s.doctype || !isDoctype(t) {
		return s.syntaxErr("syntax error")
	}
	s.doctype = true
	if err := checkDoctypeEntities(t); err != nil {
		if errors.Is(err, errBadEntityDecl) {
			return s.syntaxErr("not well-formed (invalid token)")
		}
		return err
	}
	return nil
}

func isDoctype(d xml.Directive) bool {
	rest, ok := bytes.CutPrefix(d, []byte("DOCTYPE"))
	return ok && len(rest) > 0 && isXMLSpace(rest[0])
}

func isXMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// isInCharacterRange replica la produzione Char di XML 1.0.
func isInCharacterRange(r rune) bool {
	return r == 0x09 || r == 0x0A || r == 0x0D ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

// validXMLChars verifica commenti e PI, che encoding/xml non controlla.
func validXMLChars(b []byte) bool {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if (r == utf8.RuneError && size == 1) || !isInCharacterRange(r) {
			return false
		}
		b = b[size:]
	}
	return true
}

// ========== DOCTYPE ==========

var errBadEntityDecl = errors.New("dichiarazione ENTITY malformata")

// checkDoctypeEntities replica defusedxml (forbid_entities=True): la prima
// dichiarazione <!ENTITY> (anche parametrica o NDATA) solleva
// EntitiesForbidden(name=..., system_id=..., public_id=...).
func checkDoctypeEntities(d []byte) error {
	var quote byte
	for i := 0; i < len(d); i++ {
		c := d[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '<' && bytes.HasPrefix(d[i:], []byte("<!ENTITY")):
			return parseEntityDecl(d[i+len("<!ENTITY"):])
		}
	}
	return nil
}

func parseEntityDecl(d []byte) error {
	p := 0
	skipSpace := func() int {
		start := p
		for p < len(d) && isXMLSpace(d[p]) {
			p++
		}
		return p - start
	}
	token := func() string {
		start := p
		for p < len(d) && !isXMLSpace(d[p]) && d[p] != '"' && d[p] != '\'' && d[p] != '>' {
			p++
		}
		return string(d[start:p])
	}
	literal := func() (string, bool) {
		if p >= len(d) || (d[p] != '"' && d[p] != '\'') {
			return "", false
		}
		q := d[p]
		end := bytes.IndexByte(d[p+1:], q)
		if end < 0 {
			return "", false
		}
		v := string(d[p+1 : p+1+end])
		p += end + 2
		return v, true
	}

	if skipSpace() == 0 {
		return errBadEntityDecl
	}
	if p < len(d) && d[p] == '%' {
		p++
		if skipSpace() == 0 {
			return errBadEntityDecl
		}
	}
	name := token()
	if name == "" || skipSpace() == 0 {
		return errBadEntityDecl
	}
	var sysID, pubID *string
	switch kw := token(); kw {
	case "":
		if _, ok := literal(); !ok {
			return errBadEntityDecl
		}
	case "SYSTEM":
		skipSpace()
		v, ok := literal()
		if !ok {
			return errBadEntityDecl
		}
		sysID = &v
	case "PUBLIC":
		skipSpace()
		pub, ok := literal()
		if !ok {
			return errBadEntityDecl
		}
		skipSpace()
		sys, ok := literal()
		if !ok {
			return errBadEntityDecl
		}
		pubID, sysID = &pub, &sys
	default:
		return errBadEntityDecl
	}
	return &PyException{
		Type: "EntitiesForbidden",
		Msg:  fmt.Sprintf("EntitiesForbidden(name='%s', system_id=%s, public_id=%s)", name, pyOptStrRepr(sysID), pyOptStrRepr(pubID)),
	}
}

// ========== BOM e dichiarazione XML ==========

// readXMLDecl salta il BOM UTF-8 iniziale (che decode("utf-8") conserva ed
// expat accetta) e consuma l'eventuale dichiarazione <?xml ...?> validandola
// come expat. L'encoding dichiarato viene ignorato, come fa pyexpat quando
// riceve una str (il contenuto è già UTF-8).
func readXMLDecl(br *bufio.Reader) error {
	if b, err := br.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		_, _ = br.Discard(3)
	}
	b, _ := br.Peek(6)
	if len(b) < 6 || string(b[:5]) != "<?xml" || (!isXMLSpace(b[5]) && b[5] != '?') {
		return nil
	}
	_, _ = br.Discard(5)
	var content []byte
	for {
		c, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return &xmlSyntaxError{msg: "unclosed token: line 1, column 0"}
			}
			return err
		}
		if c == '>' && len(content) > 0 && content[len(content)-1] == '?' {
			content = content[:len(content)-1]
			break
		}
		content = append(content, c)
	}
	if !validXMLDecl(content) {
		return &xmlSyntaxError{msg: "XML declaration not well-formed: line 1, column 0"}
	}
	return nil
}

// validXMLDecl verifica: S version Eq "VersionNum" [S encoding Eq "EncName"]
// [S standalone Eq "yes|no"] S?  (versione con i caratteri ammessi da expat,
// che accetta anche la stringa vuota).
func validXMLDecl(c []byte) bool {
	p := 0
	space := func() bool {
		start := p
		for p < len(c) && isXMLSpace(c[p]) {
			p++
		}
		return p > start
	}
	pseudoAttr := func(name string, valid func(string) bool) bool {
		if !bytes.HasPrefix(c[p:], []byte(name)) {
			return false
		}
		save := p
		p += len(name)
		space()
		if p >= len(c) || c[p] != '=' {
			p = save
			return false
		}
		p++
		space()
		if p >= len(c) || (c[p] != '"' && c[p] != '\'') {
			p = save
			return false
		}
		q := c[p]
		end := bytes.IndexByte(c[p+1:], q)
		if end < 0 || !valid(string(c[p+1:p+1+end])) {
			p = save
			return false
		}
		p += end + 2
		return true
	}
	if !space() || !pseudoAttr("version", validVersionNum) {
		return false
	}
	// Pseudo-attributi opzionali, in quest'ordine: se mancano si riparte
	// dallo spazio che li avrebbe preceduti.
	optional := func(name string, valid func(string) bool) {
		save := p
		if !space() || !pseudoAttr(name, valid) {
			p = save
		}
	}
	optional("encoding", validEncName)
	optional("standalone", func(v string) bool { return v == "yes" || v == "no" })
	space()
	return p == len(c)
}

func validVersionNum(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == ':' || c == '-') {
			return false
		}
	}
	return true
}

func validEncName(v string) bool {
	if v == "" || !(v[0] >= 'a' && v[0] <= 'z' || v[0] >= 'A' && v[0] <= 'Z') {
		return false
	}
	for i := 1; i < len(v); i++ {
		c := v[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
