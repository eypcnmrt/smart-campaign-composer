package content

import "strings"

// Encoding is the character set an SMS is sent in.
type Encoding string

const (
	GSM7 Encoding = "GSM-7"
	UCS2 Encoding = "UCS-2"
)

// SMS size limits per encoding: one message, and each part of a longer
// (concatenated) message, which spends part of the payload on a header.
const (
	gsm7Single = 160
	gsm7Multi  = 153
	ucs2Single = 70
	ucs2Multi  = 67
)

// SMSInfo describes how a text would be sent. It is information for the
// marketer, not a limit: the content limit is MaxSMS characters.
type SMSInfo struct {
	Encoding Encoding
	Units    int // septets (GSM-7) or UTF-16 code units (UCS-2)
	Segments int // messages needed; 0 for an empty text
	Single   int // capacity of a one-part message
	Multi    int // capacity of each part of a longer message
}

// gsm7Basic is the GSM 03.38 default alphabet (one septet per character) and
// gsm7Extension the escape table (two septets). Greek letters are written as
// escapes so a look-alike Latin letter cannot slip in.
const (
	gsm7Basic = "@£$¥èéùìòÇ\nØø\rÅå" +
		"Δ_ΦΓΛΩΠΨΣΘΞ" +
		"ÆæßÉ !\"#¤%&'()*+,-./0123456789:;<=>?" +
		"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§" +
		"¿abcdefghijklmnopqrstuvwxyzäöñüà"
	gsm7Extension = "^{}\\[~]|€\f"
)

// AnalyzeSMS reports the encoding and the number of messages for a text.
//
// A text stays GSM-7 only if every character is in the GSM alphabet. Of the
// Turkish letters, Ü Ö Ç ü ö are (so a text with only those stays GSM-7),
// while ç ğ ı İ ş Ş Ğ are not, and a single one of them switches the whole
// text to UCS-2 with its 70 / 67 limits. Emoji count as two UTF-16 units.
//
// Characters are never split across messages: one that does not fit in the
// current part starts the next. For a template the result is an estimate,
// since variables change the length when they are filled in.
func AnalyzeSMS(text string) SMSInfo {
	info := SMSInfo{Encoding: GSM7, Single: gsm7Single, Multi: gsm7Multi}
	for _, r := range text {
		if !strings.ContainsRune(gsm7Basic, r) && !strings.ContainsRune(gsm7Extension, r) {
			info = SMSInfo{Encoding: UCS2, Single: ucs2Single, Multi: ucs2Multi}
			break
		}
	}

	size := func(r rune) int {
		switch {
		case info.Encoding == GSM7 && strings.ContainsRune(gsm7Extension, r):
			return 2
		case info.Encoding == UCS2 && r > 0xFFFF:
			return 2 // surrogate pair
		}
		return 1
	}
	for _, r := range text {
		info.Units += size(r)
	}

	switch {
	case info.Units == 0:
		return info
	case info.Units <= info.Single:
		info.Segments = 1
		return info
	}
	info.Segments = 1
	used := 0
	for _, r := range text {
		if s := size(r); used+s > info.Multi {
			info.Segments++
			used = s
		} else {
			used += s
		}
	}
	return info
}
