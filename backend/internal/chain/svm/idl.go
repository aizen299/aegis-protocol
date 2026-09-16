package svm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const discriminatorLen = 8

// Anchor's tag for an event recorded as instruction data by emit_cpi!.
var eventInstructionTag = []byte{0xe4, 0x45, 0xa5, 0x2e, 0x51, 0xcb, 0x9a, 0x1d}

type idlDoc struct {
	Address  string       `json:"address"`
	Events   []idlNamed   `json:"events"`
	Accounts []idlNamed   `json:"accounts"`
	Types    []idlTypeDef `json:"types"`
}

type idlNamed struct {
	Name          string `json:"name"`
	Discriminator []int  `json:"discriminator"`
}

type idlTypeDef struct {
	Name string `json:"name"`
	Type struct {
		Kind     string     `json:"kind"`
		Fields   []idlField `json:"fields"`
		Variants []struct {
			Name   string          `json:"name"`
			Fields json.RawMessage `json:"fields"`
		} `json:"variants"`
	} `json:"type"`
}

type idlField struct {
	Name string          `json:"name"`
	Type json.RawMessage `json:"type"`
}

type fieldKind int

const (
	kindPubkey fieldKind = iota
	kindUint
	kindInt
	kindBool
	kindEnum
	kindBytes
	kindArray
	kindString
)

type field struct {
	name     string
	kind     fieldKind
	size     int
	variants []string
	elem     *field
	count    int
}

type layout struct {
	discriminator []byte
	fields        []field
}

// IDL is a program's interface, reduced to what indexing reads: events and fixed-size accounts.
// Anything outside that subset is refused when the IDL is loaded, so an unsupported type fails at
// startup rather than on the first event that carries it.
type IDL struct {
	Program  pbtypes.Identity
	events   map[string]layout
	accounts map[string]layout
}

func ParseIDL(raw []byte) (*IDL, error) {
	var doc idlDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse idl: %w", err)
	}
	program, err := Decode(doc.Address)
	if err != nil {
		return nil, fmt.Errorf("idl address: %w", err)
	}
	types := make(map[string]idlTypeDef, len(doc.Types))
	for _, t := range doc.Types {
		types[t.Name] = t
	}

	build := func(named []idlNamed, what string) (map[string]layout, error) {
		out := make(map[string]layout, len(named))
		for _, n := range named {
			def, ok := types[n.Name]
			if !ok || def.Type.Kind != "struct" {
				return nil, fmt.Errorf("%s %s: no struct definition", what, n.Name)
			}
			disc, err := discriminator(n.Discriminator)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", what, n.Name, err)
			}
			fields := make([]field, 0, len(def.Type.Fields))
			for _, f := range def.Type.Fields {
				parsed, err := parseField(f, types, what == "event")
				if err != nil {
					return nil, fmt.Errorf("%s %s.%s: %w", what, n.Name, f.Name, err)
				}
				fields = append(fields, parsed)
			}
			out[n.Name] = layout{discriminator: disc, fields: fields}
		}
		return out, nil
	}

	events, err := build(doc.Events, "event")
	if err != nil {
		return nil, err
	}
	accounts, err := build(doc.Accounts, "account")
	if err != nil {
		return nil, err
	}
	return &IDL{Program: program, events: events, accounts: accounts}, nil
}

func discriminator(values []int) ([]byte, error) {
	if len(values) != discriminatorLen {
		return nil, fmt.Errorf("discriminator is %d bytes, want %d", len(values), discriminatorLen)
	}
	out := make([]byte, discriminatorLen)
	for i, v := range values {
		if v < 0 || v > 255 {
			return nil, fmt.Errorf("discriminator byte %d out of range", v)
		}
		out[i] = byte(v)
	}
	return out, nil
}

var primitives = map[string]field{
	"pubkey": {kind: kindPubkey, size: 32},
	"bool":   {kind: kindBool, size: 1},
	"u8":     {kind: kindUint, size: 1}, "u16": {kind: kindUint, size: 2}, "u32": {kind: kindUint, size: 4},
	"u64": {kind: kindUint, size: 8}, "u128": {kind: kindUint, size: 16},
	"i8": {kind: kindInt, size: 1}, "i16": {kind: kindInt, size: 2}, "i32": {kind: kindInt, size: 4},
	"i64": {kind: kindInt, size: 8}, "i128": {kind: kindInt, size: 16},
}

// parseField reduces an IDL type to a decoder. Field names become camelCase, matching the Solidity
// event parameters the indexer's handlers read, so one handler serves both chains.
//
// A string is accepted only in events: an event is decoded in full and must consume its data
// exactly, but an account layout needs every field at a fixed offset.
func parseField(f idlField, types map[string]idlTypeDef, event bool) (field, error) {
	name := camelCase(f.Name)
	var typeName string
	if err := json.Unmarshal(f.Type, &typeName); err == nil {
		if typeName == "string" && event {
			return field{name: name, kind: kindString}, nil
		}
		p, ok := primitives[typeName]
		if !ok {
			return field{}, fmt.Errorf("type %q is not supported", typeName)
		}
		p.name = name
		return p, nil
	}

	var array struct {
		Array []json.RawMessage `json:"array"`
	}
	if err := json.Unmarshal(f.Type, &array); err == nil && len(array.Array) == 2 {
		var elem string
		var count int
		if json.Unmarshal(array.Array[0], &elem) == nil && json.Unmarshal(array.Array[1], &count) == nil && count > 0 {
			if elem == "u8" {
				return field{name: name, kind: kindBytes, size: count}, nil
			}
			if p, ok := primitives[elem]; ok && (p.kind == kindUint || p.kind == kindInt) {
				e := p
				return field{name: name, kind: kindArray, size: p.size * count, elem: &e, count: count}, nil
			}
		}
	}

	var defined struct {
		Defined struct {
			Name string `json:"name"`
		} `json:"defined"`
	}
	if err := json.Unmarshal(f.Type, &defined); err == nil && defined.Defined.Name != "" {
		def, ok := types[defined.Defined.Name]
		if !ok || def.Type.Kind != "enum" || len(def.Type.Variants) == 0 || len(def.Type.Variants) > 256 {
			return field{}, fmt.Errorf("type %s is not a supported enum", defined.Defined.Name)
		}
		variants := make([]string, len(def.Type.Variants))
		for i, v := range def.Type.Variants {
			if len(v.Fields) > 0 && string(v.Fields) != "null" {
				return field{}, fmt.Errorf("enum %s variant %s carries data", defined.Defined.Name, v.Name)
			}
			variants[i] = v.Name
		}
		return field{name: name, kind: kindEnum, size: 1, variants: variants}, nil
	}
	return field{}, fmt.Errorf("type %s is not supported", string(f.Type))
}

// DecodeEvent decodes emit_cpi! instruction data: the event tag, the event's discriminator, then its
// fields. Trailing bytes are an error, not ignored: a layout that leaves bytes over was read wrong.
func (idl *IDL) DecodeEvent(data []byte) (string, map[string]any, error) {
	if len(data) < 2*discriminatorLen || !bytes.Equal(data[:discriminatorLen], eventInstructionTag) {
		return "", nil, fmt.Errorf("not event data")
	}
	body := data[discriminatorLen:]
	for name, l := range idl.events {
		if !bytes.Equal(body[:discriminatorLen], l.discriminator) {
			continue
		}
		payload, err := decodeFields(l.fields, body[discriminatorLen:], true)
		if err != nil {
			return "", nil, fmt.Errorf("event %s: %w", name, err)
		}
		return name, payload, nil
	}
	return "", nil, fmt.Errorf("unknown event discriminator %x", body[:discriminatorLen])
}

// DecodeAccount decodes the named fields of an account, checking its discriminator. Accounts are
// allocated with room to spare, so bytes past the last field are expected.
func (idl *IDL) DecodeAccount(name string, data []byte) (map[string]any, error) {
	l, ok := idl.accounts[name]
	if !ok {
		return nil, fmt.Errorf("account %s is not in the idl", name)
	}
	if len(data) < discriminatorLen || !bytes.Equal(data[:discriminatorLen], l.discriminator) {
		return nil, fmt.Errorf("account data is not a %s", name)
	}
	return decodeFields(l.fields, data[discriminatorLen:], false)
}

func decodeFields(fields []field, data []byte, exact bool) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	offset := 0
	for _, f := range fields {
		size := f.size
		if f.kind == kindString {
			if offset+4 > len(data) {
				return nil, fmt.Errorf("field %s: no length prefix", f.name)
			}
			n := int(uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24)
			offset += 4
			if n < 0 || n > len(data)-offset {
				return nil, fmt.Errorf("field %s: length %d exceeds the data", f.name, n)
			}
			size = n
		}
		if offset+size > len(data) {
			return nil, fmt.Errorf("field %s: data ends at %d, need %d", f.name, len(data), offset+size)
		}
		chunk := data[offset : offset+size]
		offset += size

		switch f.kind {
		case kindPubkey:
			id, _ := pbtypes.IdentityFromBytes(chunk)
			out[f.name] = id
		case kindBool:
			if chunk[0] > 1 {
				return nil, fmt.Errorf("field %s: bool byte is %d", f.name, chunk[0])
			}
			out[f.name] = chunk[0] == 1
		case kindEnum:
			if int(chunk[0]) >= len(f.variants) {
				return nil, fmt.Errorf("field %s: variant %d out of range", f.name, chunk[0])
			}
			out[f.name] = f.variants[chunk[0]]
		case kindString:
			if !utf8.Valid(chunk) {
				return nil, fmt.Errorf("field %s: not valid UTF-8", f.name)
			}
			out[f.name] = string(chunk)
		case kindBytes:
			if f.size == 32 {
				out[f.name] = [32]byte(chunk)
			} else {
				out[f.name] = append([]byte(nil), chunk...)
			}
		case kindArray:
			values := make([]*big.Int, f.count)
			for i := range values {
				start := i * f.elem.size
				values[i] = littleEndian(chunk[start:start+f.elem.size], f.elem.kind == kindInt)
			}
			out[f.name] = values
		case kindUint, kindInt:
			// uint8 stays a uint8, as go-ethereum decodes it; wider integers are *big.Int.
			if f.kind == kindUint && f.size == 1 {
				out[f.name] = chunk[0]
			} else {
				out[f.name] = littleEndian(chunk, f.kind == kindInt)
			}
		}
	}
	if exact && offset != len(data) {
		return nil, fmt.Errorf("%d trailing bytes", len(data)-offset)
	}
	return out, nil
}

func camelCase(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func littleEndian(b []byte, signed bool) *big.Int {
	be := make([]byte, len(b))
	for i := range b {
		be[len(b)-1-i] = b[i]
	}
	n := new(big.Int).SetBytes(be)
	if signed && len(b) > 0 && b[len(b)-1]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return n
}
