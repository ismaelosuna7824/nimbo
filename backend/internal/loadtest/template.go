package loadtest

import (
	"encoding/hex"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// template is a payload with {{seq}}, {{uuid}}, {{now}} and {{rand}}
// placeholders. {{seq}} is the 1-based request number within the run,
// {{uuid}} a random UUIDv4, {{now}} the current UTC time in RFC 3339 with
// nanoseconds and {{rand}} a random integer in [0, 2^31). Other {{...}}
// sequences are kept literally.
type template struct {
	parts []part
}

type part struct {
	lit string
	ph  placeholder
}

type placeholder int

const (
	phNone placeholder = iota
	phSeq
	phUUID
	phNow
	phRand
)

var placeholders = map[string]placeholder{
	"seq":  phSeq,
	"uuid": phUUID,
	"now":  phNow,
	"rand": phRand,
}

func compileTemplate(s string) template {
	var t template
	var lit strings.Builder
	for {
		i := strings.Index(s, "{{")
		if i < 0 {
			break
		}
		j := strings.Index(s[i+2:], "}}")
		if j < 0 {
			break
		}
		name := strings.TrimSpace(s[i+2 : i+2+j])
		ph, ok := placeholders[name]
		if !ok {
			lit.WriteString(s[:i+2+j+2])
			s = s[i+2+j+2:]
			continue
		}
		lit.WriteString(s[:i])
		if lit.Len() > 0 {
			t.parts = append(t.parts, part{lit: lit.String()})
			lit.Reset()
		}
		t.parts = append(t.parts, part{ph: ph})
		s = s[i+2+j+2:]
	}
	lit.WriteString(s)
	if lit.Len() > 0 {
		t.parts = append(t.parts, part{lit: lit.String()})
	}
	return t
}

// static reports whether rendering always yields the same string.
func (t template) static() bool {
	for _, p := range t.parts {
		if p.ph != phNone {
			return false
		}
	}
	return true
}

func (t template) render(seq int64) string {
	if len(t.parts) == 1 && t.parts[0].ph == phNone {
		return t.parts[0].lit
	}
	var b strings.Builder
	for _, p := range t.parts {
		switch p.ph {
		case phNone:
			b.WriteString(p.lit)
		case phSeq:
			b.WriteString(strconv.FormatInt(seq, 10))
		case phUUID:
			b.WriteString(newUUID())
		case phNow:
			b.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
		case phRand:
			b.WriteString(strconv.FormatInt(int64(rand.Int32()), 10))
		}
	}
	return b.String()
}

func newUUID() string {
	var u [16]byte
	hi, lo := rand.Uint64(), rand.Uint64()
	for i := range 8 {
		u[i] = byte(hi >> (56 - 8*i))
		u[8+i] = byte(lo >> (56 - 8*i))
	}
	u[6] = u[6]&0x0f | 0x40
	u[8] = u[8]&0x3f | 0x80
	var out [36]byte
	hex.Encode(out[0:8], u[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], u[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], u[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], u[8:10])
	out[23] = '-'
	hex.Encode(out[24:], u[10:])
	return string(out[:])
}
