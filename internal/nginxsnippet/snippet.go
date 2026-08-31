// Package nginxsnippet validates the deliberately small Nginx fragment
// language NixCP accepts inside its generated `location /` block.
package nginxsnippet

import (
	"fmt"
	"strings"
	"unicode"
)

// Validate accepts only simple, semicolon-terminated directives which are
// meaningful in a location context. It is intentionally not an Nginx parser:
// blocks, unknown directives, and malformed quoting are rejected rather than
// interpreted permissively. Quoted arguments may contain punctuation safely.
func Validate(input string) error {
	tokens, err := tokenize(input)
	if err != nil {
		return err
	}
	for len(tokens) > 0 {
		first := tokens[0]
		if first.text == ";" || first.text == "{" || first.text == "}" {
			return first.errorf("expected a location directive")
		}
		directive := strings.ToLower(first.text)
		if !allowed[directive] {
			return first.errorf("directive %q is not allowed in a NixCP location snippet", directive)
		}
		tokens = tokens[1:]
		terminated := false
		for len(tokens) > 0 {
			t := tokens[0]
			tokens = tokens[1:]
			switch t.text {
			case ";":
				terminated = true
			case "{", "}":
				return t.errorf("blocks and context changes are not allowed")
			}
			if terminated {
				break
			}
		}
		if !terminated {
			return first.errorf("directive %q must end with a semicolon", directive)
		}
	}
	return nil
}

// This deliberately conservative allowlist covers common location behaviour
// without allowing configuration inclusion, alternate content roots, upstream
// selection, listener/server settings, or nested/context-changing directives.
var allowed = map[string]bool{
	"add_header": true, "access_log": true, "allow": true, "auth_basic": true,
	"auth_basic_user_file": true, "charset": true, "client_body_buffer_size": true,
	"client_body_timeout": true, "client_max_body_size": true, "default_type": true,
	"deny": true, "error_page": true, "etag": true, "expires": true, "gzip": true,
	"gzip_types": true, "index": true, "internal": true, "limit_rate": true,
	"limit_rate_after": true, "log_not_found": true, "max_ranges": true,
	"recursive_error_pages": true, "return": true, "rewrite": true, "satisfy": true,
	"send_timeout": true, "set": true, "try_files": true,
}

type token struct {
	text string
	line int
}

func (t token) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", t.line, fmt.Sprintf(format, args...))
}

func tokenize(s string) ([]token, error) {
	var out []token
	line := 1
	for i := 0; i < len(s); {
		if unicode.IsSpace(rune(s[i])) {
			if s[i] == '\n' {
				line++
			}
			i++
			continue
		}
		if s[i] == '#' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		start, tokenLine := i, line
		if strings.ContainsRune("{};", rune(s[i])) {
			out = append(out, token{text: s[i : i+1], line: line})
			i++
			continue
		}
		if s[i] == '\'' || s[i] == '"' {
			quote := s[i]
			i++
			for i < len(s) && s[i] != quote {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == '\n' {
					line++
				}
				i++
			}
			if i == len(s) {
				return nil, token{text: s[start:], line: tokenLine}.errorf("unterminated quoted argument")
			}
			i++
			out = append(out, token{text: s[start:i], line: tokenLine})
			continue
		}
		for i < len(s) && !unicode.IsSpace(rune(s[i])) && !strings.ContainsRune("#{};'\"", rune(s[i])) {
			i++
		}
		if start == i {
			return nil, token{text: s[i : i+1], line: line}.errorf("unexpected character")
		}
		out = append(out, token{text: s[start:i], line: tokenLine})
	}
	return out, nil
}
