package render

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/kirahowe/acp-tui/internal/core"
)

// When the session is contaminated, text the agent wrote may have been
// shaped by an outsider, and a link in it is a way to carry data out: a
// click sends the URL, query string and all. So such text shows its URLs
// defanged, https://evil.com/x?d=1 as hxxps://evil[.]com/x?d=1, which no
// terminal turns into a link. Nothing here ever writes an OSC 8
// hyperlink; sanitize drops the ESC that would start one.

// urlRE finds a URL: a scheme and "://", or "www.", then everything up to
// a space or a character that usually closes a URL in prose or markdown.
var urlRE = regexp.MustCompile("(?i)(?:[a-z][a-z0-9+.\\-]*://|www\\.)[^\\s<>\"'`)\\]\\x1b]*")

// Defang defangs every URL in s. The host loses its dots to [.], http and
// https become hxxp and hxxps, and any other scheme's :// becomes [:]//,
// so no "://" is left. s should be sanitized first.
func Defang(s string) string {
	out := urlRE.ReplaceAllStringFunc(s, func(u string) string {
		scheme, rest := "", u
		if i := strings.Index(u, "://"); i >= 0 {
			scheme, rest = u[:i], u[i+3:]
			switch strings.ToLower(scheme) {
			case "http":
				scheme = "hxxp" + "://"
			case "https":
				scheme = "hxxps" + "://"
			case "hxxp", "hxxps": // defanged already
				scheme += "://"
			default:
				scheme += "[:]//"
			}
		}
		host, path := rest, ""
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			host, path = rest[:i], rest[i:]
		}
		host = strings.ReplaceAll(strings.ReplaceAll(host, "[.]", "."), ".", "[.]")
		return scheme + host + path
	})
	// A "://" can be left that the match did not own (it came from the
	// middle of another URL's host); none survives but hxxp(s)://.
	return schemeSepRE.ReplaceAllStringFunc(out, func(m string) string {
		if len(m) > 3 {
			return m
		}
		return "[:]//"
	})
}

// schemeSepRE finds "://" with a defanged http scheme before it, which
// stays, or without, which does not.
var schemeSepRE = regexp.MustCompile("(?i)hxxps?://|://")

// defangLines is a last check on rendered lines: markdown can put a
// "://" together from pieces (an emphasis mark between the slashes), which
// Defang on the source did not see. A line that has one is shown plain
// and defanged.
func defangLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		plain := ansi.Strip(l)
		if live(plain) {
			l = styled(faint, Defang(plain))
		}
		out[i] = l
	}
	return out
}

// live says whether text still holds a URL a terminal could link: a
// "://" that is not a defanged http scheme's, or a www. host.
func live(plain string) bool {
	for _, m := range schemeSepRE.FindAllString(plain, -1) {
		if len(m) == 3 {
			return true
		}
	}
	return strings.Contains(strings.ToLower(plain), "www.")
}

// defanged is e with its agent-written text defanged.
func defanged(e core.Entry) core.Entry {
	d := func(s string) string { return Defang(sanitize(s)) }
	e.Text, e.Detail = d(e.Text), d(e.Detail)
	calls := make([]core.Call, len(e.Calls))
	for i, c := range e.Calls {
		c.Name, c.Title, c.Input, c.Output = d(c.Name), d(c.Title), d(c.Input), d(c.Output)
		calls[i] = c
	}
	e.Calls = calls
	return e
}
