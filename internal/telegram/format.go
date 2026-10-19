package telegram

import (
	"errors"
	"html"
	"regexp"
	"strings"
)

// MaxMessage is the limit of one message of the Bot API, in characters.
const MaxMessage = 4096

// MaxParts is how many parts the engine splits a long text into; a longer one
// goes as a summary and the whole text as a .md file.
const MaxParts = 3

// partSize leaves room for the tags the conversion adds.
const partSize = 3500

// ErrBadMarkdown is a text the conversion does not take (an unclosed fence or
// span): it goes out as plain text.
var ErrBadMarkdown = errors.New("telegram: the markdown does not convert")

var (
	boldRe = regexp.MustCompile(`\*\*([^*\n][^*\n]*?)\*\*`)
	linkRe = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
)

// ToHTML converts the Markdown of an agent to the HTML the Telegram accepts
// (parse_mode=HTML): bold, code, fences and http(s) links; everything else is
// text, and every `<`, `>` and `&` of the text is escaped. A fence or a code
// span that is not closed is ErrBadMarkdown.
func ToHTML(md string) (string, error) {
	var out strings.Builder
	rest := md
	for rest != "" {
		i := strings.Index(rest, "```")
		if i < 0 {
			s, err := inline(rest)
			if err != nil {
				return "", err
			}
			out.WriteString(s)
			break
		}
		s, err := inline(rest[:i])
		if err != nil {
			return "", err
		}
		out.WriteString(s)
		body := rest[i+3:]
		j := strings.Index(body, "```")
		if j < 0 {
			return "", ErrBadMarkdown
		}
		code := body[:j]
		if nl := strings.IndexByte(code, '\n'); nl >= 0 && !strings.ContainsAny(code[:nl], " \t") {
			code = code[nl+1:] // the language tag of the fence
		}
		out.WriteString("<pre>" + html.EscapeString(strings.Trim(code, "\n")) + "</pre>")
		rest = body[j+3:]
	}
	return out.String(), nil
}

// inline converts the text outside the fences: code spans first, so what is
// inside them is never read as bold or as a link.
func inline(s string) (string, error) {
	var out strings.Builder
	for s != "" {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			out.WriteString(spans(s))
			break
		}
		out.WriteString(spans(s[:i]))
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			return "", ErrBadMarkdown
		}
		out.WriteString("<code>" + html.EscapeString(s[i+1:i+1+j]) + "</code>")
		s = s[i+1+j+1:]
	}
	return out.String(), nil
}

func spans(s string) string {
	s = html.EscapeString(s)
	s = linkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	return boldRe.ReplaceAllString(s, "<b>$1</b>")
}

// Split cuts a text in parts of at most size characters, at the end of a
// paragraph, a line or a word, never inside a code fence when it can help it.
func Split(text string, size int) []string {
	var parts []string
	for len([]rune(text)) > size {
		r := []rune(text)
		cut := size
		window := string(r[:size])
		for _, sep := range []string{"\n\n", "\n", " "} {
			if i := strings.LastIndex(window, sep); i > size/2 {
				cut = len([]rune(window[:i]))
				break
			}
		}
		parts = append(parts, strings.TrimRight(string(r[:cut]), " \n"))
		text = strings.TrimLeft(string(r[cut:]), " \n")
	}
	return append(parts, text)
}
