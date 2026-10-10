package contracts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// EncodeJSON writes ordinary contract JSON. Saved byte identities are never
// obtained by decoding and re-encoding an existing content blob.
func EncodeJSON(value any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// EncodeCanonicalJSON follows old context_attribution.ts: sort object keys in
// UTF-16 order, keep array order, encode JSON primitives as JSON.stringify does.
func EncodeCanonicalJSON(raw json.RawMessage) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	var b bytes.Buffer
	if err := canonicalValue(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func canonicalValue(b *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		writeJSONString(b, x)
	case json.Number:
		n, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return err
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			b.WriteString("null")
			return nil
		}
		if n == 0 {
			b.WriteByte('0')
			return nil
		}
		format := byte('f')
		if math.Abs(n) < 1e-6 || math.Abs(n) >= 1e21 {
			format = 'e'
		}
		s := strconv.FormatFloat(n, format, -1, 64)
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			exponent, _ := strconv.Atoi(s[i+1:])
			sign := ""
			if exponent >= 0 {
				sign = "+"
			}
			s = s[:i] + "e" + sign + strconv.Itoa(exponent)
		}
		b.WriteString(s)
	case []any:
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := canonicalValue(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return utf16Less(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, key)
			b.WriteByte(':')
			if err := canonicalValue(b, x[key]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("unsupported JSON value %T", v)
	}
	return nil
}

func utf16Less(a, b string) bool {
	aa, bb := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] != bb[i] {
			return aa[i] < bb[i]
		}
	}
	return len(aa) < len(bb)
}
func writeJSONString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
