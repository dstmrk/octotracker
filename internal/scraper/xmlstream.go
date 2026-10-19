package scraper

// Parser XML in streaming: il documento viene letto token per token con
// encoding/xml e solo i sottoalberi richiesti (le singole offerte) vengono
// materializzati, uno alla volta. I nomi dei tag sono senza namespace.
//
// encoding/xml non espande entità esterne né definite nel DTD (in modalità
// Strict un riferimento sconosciuto è un errore di sintassi), quindi XXE e
// "billion laughs" non sono possibili; in più un DOCTYPE con dichiarazioni
// ENTITY viene rifiutato, come faceva defusedxml.

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
)

var (
	errNoRoot            = errors.New("nessun elemento radice")
	errOutsideRoot       = errors.New("contenuto dopo o prima dell'elemento radice")
	errEntitiesForbidden = errors.New("dichiarazioni ENTITY non ammesse")
)

// xmlSpaceOrBOM sono i caratteri ammessi fuori dall'elemento radice (il BOM
// iniziale arriva come testo).
const xmlSpaceOrBOM = " \t\r\n\ufeff"

// element è un nodo dell'albero: tag senza namespace, text (testo prima del
// primo figlio, nil se assente) e figli.
type element struct {
	tag      string
	text     *string
	children []*element

	textBuf  []byte
	textDone bool
}

// find restituisce il primo figlio diretto con quel tag.
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

// findDesc restituisce il primo discendente con quel tag.
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

// findAllDesc restituisce tutti i discendenti con quel tag.
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

// textOf restituisce il testo dell'elemento, nil se l'elemento manca.
func textOf(e *element) *string {
	if e == nil {
		return nil
	}
	return e.text
}

// closeText fissa il testo raccolto prima del primo figlio (nil se vuoto).
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

// xmlScanner costruisce i sottoalberi scelti da capture (chiamata per ogni
// elemento fuori da un sottoalbero già catturato; depth 1 = radice) e li
// passa completi a onTree.
type xmlScanner struct {
	capture  func(tag string, depth int) bool
	onTree   func(*element)
	depth    int
	rootSeen bool
	build    []*element
}

// scanXML legge il documento da r. Restituisce nil se è ben formato, un
// errore di encoding/xml o di struttura (radice assente o multipla, testo
// fuori dalla radice, dichiarazioni ENTITY) altrimenti.
func scanXML(r io.Reader, capture func(tag string, depth int) bool, onTree func(*element)) error {
	dec := xml.NewDecoder(r)
	// Il contenuto è già UTF-8: l'encoding dichiarato viene ignorato.
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	s := &xmlScanner{capture: capture, onTree: onTree}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !s.rootSeen {
				return errNoRoot
			}
			return nil
		}
		if err != nil {
			return err
		}
		if err := s.handle(tok); err != nil {
			return err
		}
	}
}

func (s *xmlScanner) handle(tok xml.Token) error {
	switch t := tok.(type) {
	case xml.StartElement:
		return s.start(t.Name.Local)
	case xml.EndElement:
		s.end()
	case xml.CharData:
		return s.charData(t)
	case xml.Directive:
		if bytes.Contains(t, []byte("ENTITY")) {
			return errEntitiesForbidden
		}
	}
	return nil
}

func (s *xmlScanner) start(tag string) error {
	if s.depth == 0 {
		if s.rootSeen {
			return errOutsideRoot
		}
		s.rootSeen = true
	}
	s.depth++
	if n := len(s.build); n > 0 {
		parent := s.build[n-1]
		parent.closeText()
		child := &element{tag: tag}
		parent.children = append(parent.children, child)
		s.build = append(s.build, child)
	} else if s.capture(tag, s.depth) {
		s.build = append(s.build, &element{tag: tag})
	}
	return nil
}

func (s *xmlScanner) end() {
	s.depth--
	n := len(s.build)
	if n == 0 {
		return
	}
	e := s.build[n-1]
	e.closeText()
	s.build = s.build[:n-1]
	if n == 1 {
		s.onTree(e)
	}
}

func (s *xmlScanner) charData(t xml.CharData) error {
	if s.depth == 0 {
		if len(bytes.Trim(t, xmlSpaceOrBOM)) > 0 {
			return errOutsideRoot
		}
		return nil
	}
	if n := len(s.build); n > 0 && !s.build[n-1].textDone {
		s.build[n-1].textBuf = append(s.build[n-1].textBuf, t...)
	}
	return nil
}
