// Package streamjson extracts string fields from a JSON object while it is
// still being written, so a UI can show text as the model produces it.
//
// It understands exactly one shape: a flat object whose values are strings,
// which is what the content schema produces. It is a display aid only. Whatever
// it emits is shown to the user as it arrives; the complete text is decoded and
// validated again with encoding/json when the stream ends, and that result is
// the one that counts.
package streamjson

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Delta is a piece of a field's decoded text: escapes are resolved, so "\n"
// arrives as a newline and "\u00e7" as "ç".
type Delta struct {
	Field string
	Text  string
}

// ErrUnexpected means the input is not a flat object of string values. The
// extractor stops at the first such byte and ignores everything after it.
var ErrUnexpected = errors.New("unexpected input")

// maxKeyBytes bounds memory spent on one property name.
const maxKeyBytes = 256

type state int

const (
	stObject     state = iota // before "{"
	stKeyOrEnd                // after "{": a key or "}"
	stKey                     // after ",": a key
	stColon                   // after a key
	stValue                   // after ":": a string value
	stAfterValue              // after a value: "," or "}"
	stDone                    // after the closing "}"
	stInString                // inside a key or a value (see strState)
)

type strState int

const (
	strText    strState = iota // ordinary characters
	strEscape                  // after a backslash
	strUnicode                 // inside \uXXXX
)

// Extractor turns chunks of JSON text into field deltas. The zero value is not
// usable; create one with New. It is not safe for concurrent use.
type Extractor struct {
	fields []string // fields whose text is reported; others are skipped

	state state
	str   strState
	isKey bool

	key     strings.Builder // decoded property name being read
	field   string          // reported field whose value is being read, "" otherwise
	seen    map[string]bool
	partial []byte // bytes of a UTF-8 sequence cut by a chunk boundary
	hex     rune   // value of the \uXXXX digits read so far
	hexLen  int
	high    rune // pending high surrogate waiting for its pair, 0 if none

	pos int // bytes consumed, for error messages
	err error

	cur strings.Builder // text of the current Write for e.field
	out []Delta
}

// New returns an Extractor that reports the given fields.
func New(fields ...string) *Extractor {
	return &Extractor{fields: slices.Clone(fields), seen: map[string]bool{}}
}

// Err returns the error that stopped the extractor, or nil.
func (e *Extractor) Err() error { return e.err }

// Done reports whether the closing brace of the object has been read.
func (e *Extractor) Done() bool { return e.state == stDone }

// Write consumes the next chunk and returns the text it completed, at most one
// Delta per field value touched by the chunk. A chunk may end anywhere,
// including in the middle of an escape sequence or a multi-byte character; the
// unfinished part is held back until the rest arrives.
func (e *Extractor) Write(chunk string) []Delta {
	for i := 0; i < len(chunk) && e.err == nil; i++ {
		e.step(chunk[i])
		e.pos++
	}
	e.flush()
	out := e.out
	e.out = nil
	return out
}

func (e *Extractor) fail(format string, args ...any) {
	e.err = fmt.Errorf("%w at byte %d: %s", ErrUnexpected, e.pos, fmt.Sprintf(format, args...))
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

func (e *Extractor) step(b byte) {
	switch e.state {
	case stInString:
		e.stringByte(b)
		return
	case stDone:
		return // whatever follows is for the final decode to judge
	}
	if isSpace(b) {
		return
	}
	switch e.state {
	case stObject:
		if b != '{' {
			e.fail("want '{' to start the object")
			return
		}
		e.state = stKeyOrEnd
	case stKeyOrEnd, stKey:
		switch {
		case b == '"':
			e.beginString(true)
		case b == '}' && e.state == stKeyOrEnd:
			e.state = stDone
		default:
			e.fail("want a property name")
		}
	case stColon:
		if b != ':' {
			e.fail("want ':' after the property name")
			return
		}
		e.state = stValue
	case stValue:
		if b != '"' {
			e.fail("want a string value; only string values are supported")
			return
		}
		if e.field != "" {
			if e.seen[e.field] {
				e.fail("duplicate property %q", e.field)
				return
			}
			e.seen[e.field] = true
		}
		e.beginString(false)
	case stAfterValue:
		switch b {
		case ',':
			e.state = stKey
		case '}':
			e.state = stDone
		default:
			e.fail("want ',' or '}' after the value")
		}
	}
}

func (e *Extractor) beginString(isKey bool) {
	e.state = stInString
	e.str = strText
	e.isKey = isKey
	if isKey {
		e.key.Reset()
	}
}

func (e *Extractor) endString() {
	if e.isKey {
		e.field = ""
		if name := e.key.String(); slices.Contains(e.fields, name) {
			e.field = name
		}
		e.state = stColon
		return
	}
	e.flush()
	e.field = ""
	e.state = stAfterValue
}

func (e *Extractor) stringByte(b byte) {
	switch e.str {
	case strText:
		switch b {
		case '"':
			e.flushPartial()
			e.flushHigh()
			e.endString()
		case '\\':
			e.flushPartial()
			e.str = strEscape
		default:
			e.flushHigh()
			e.text(b)
		}
	case strEscape:
		e.escape(b)
	case strUnicode:
		e.unicodeDigit(b)
	}
}

// text handles one byte of ordinary text. Bytes of a multi-byte character may
// arrive in different chunks, so they are collected until the character is
// complete; invalid bytes become U+FFFD one by one, as encoding/json does.
func (e *Extractor) text(b byte) {
	if b < utf8.RuneSelf && len(e.partial) == 0 {
		e.emit(rune(b))
		return
	}
	e.partial = append(e.partial, b)
	for len(e.partial) > 0 && utf8.FullRune(e.partial) {
		r, size := utf8.DecodeRune(e.partial)
		e.emit(r)
		e.partial = e.partial[size:]
	}
	if len(e.partial) == 0 {
		e.partial = nil
	}
}

// flushPartial turns a character that was cut off by a quote or backslash into
// one U+FFFD per byte, which is what encoding/json produces for it.
func (e *Extractor) flushPartial() {
	for range e.partial {
		e.emit(utf8.RuneError)
	}
	e.partial = nil
}

// flushHigh emits U+FFFD for a high surrogate whose partner never came.
func (e *Extractor) flushHigh() {
	if e.high != 0 {
		e.emit(utf8.RuneError)
		e.high = 0
	}
}

func (e *Extractor) escape(b byte) {
	if b == 'u' {
		e.str = strUnicode
		e.hex, e.hexLen = 0, 0
		return
	}
	var r rune
	switch b {
	case '"', '\\', '/':
		r = rune(b)
	case 'b':
		r = '\b'
	case 'f':
		r = '\f'
	case 'n':
		r = '\n'
	case 'r':
		r = '\r'
	case 't':
		r = '\t'
	default:
		e.fail("invalid escape \\%c", b)
		return
	}
	e.flushHigh()
	e.emit(r)
	e.str = strText
}

func (e *Extractor) unicodeDigit(b byte) {
	var d rune
	switch {
	case b >= '0' && b <= '9':
		d = rune(b - '0')
	case b >= 'a' && b <= 'f':
		d = rune(b-'a') + 10
	case b >= 'A' && b <= 'F':
		d = rune(b-'A') + 10
	default:
		e.fail("invalid hex digit %q in \\u escape", b)
		return
	}
	e.hex = e.hex<<4 | d
	if e.hexLen++; e.hexLen < 4 {
		return
	}
	e.str = strText

	r := e.hex
	if e.high != 0 {
		// A pending high surrogate pairs only with a low surrogate right behind it.
		if pair := utf16.DecodeRune(e.high, r); pair != utf8.RuneError {
			e.high = 0
			e.emit(pair)
			return
		}
		e.flushHigh()
	}
	switch {
	case utf16.IsSurrogate(r) && r < 0xDC00:
		e.high = r // wait for the low half
	case utf16.IsSurrogate(r):
		e.emit(utf8.RuneError) // a low surrogate on its own
	default:
		e.emit(r)
	}
}

// emit adds a decoded character to the property name or the field's text.
func (e *Extractor) emit(r rune) {
	switch {
	case e.isKey:
		if e.key.Len() > maxKeyBytes {
			e.fail("property name longer than %d bytes", maxKeyBytes)
			return
		}
		e.key.WriteRune(r)
	case e.field != "":
		e.cur.WriteRune(r)
	}
}

// flush moves the text collected since the last flush into the output.
func (e *Extractor) flush() {
	if e.cur.Len() == 0 {
		return
	}
	e.out = append(e.out, Delta{Field: e.field, Text: e.cur.String()})
	e.cur.Reset()
}
