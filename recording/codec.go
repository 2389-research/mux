// ABOUTME: The mux-json/1 canonical codec: EncodePayload/EncodeRecord produce
// ABOUTME: deterministic bytes, DecodeRecord/ValidateRecord check them strictly.
package recording

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var blobRefPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// EncodePayload produces the mux-json/1 canonical encoding of v: object keys
// sorted lexically, array order preserved, numeric literals preserved
// exactly (so "1" and "1.0" stay distinct and an integer like
// 9007199254740993 survives beyond float64 precision), insignificant
// whitespace removed, HTML-sensitive runes escaped, no trailing newline.
//
// If v is a json.RawMessage, its bytes are validated as untrusted raw JSON
// first: invalid UTF-8, an unpaired UTF-16 surrogate escape, a duplicate
// object key (even under different escaping of the same key), or trailing
// content after the single top-level value are all rejected before any
// re-encoding. Otherwise v is walked with reflect before json.Marshal is
// ever called: an invalid UTF-8 string or map key, a cyclic value, or a
// custom json.Marshaler/encoding.TextMarshaler other than time.Time or
// json.RawMessage is rejected at this boundary rather than silently
// accepted or handed to arbitrary user-defined encoding logic.
func EncodePayload(v any) (json.RawMessage, error) {
	const op = "EncodePayload"
	var raw []byte
	switch tv := v.(type) {
	case nil:
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("payload value is nil")}
	case json.RawMessage:
		if len(tv) == 0 {
			return nil, &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("payload is empty")}
		}
		if err := validateRawJSON(tv); err != nil {
			return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
		}
		raw = tv
	default:
		if err := validateTypedValue(reflect.ValueOf(v), map[uintptr]bool{}); err != nil {
			return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
		}
		raw = b
	}
	canon, err := canonicalize(raw)
	if err != nil {
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	return canon, nil
}

// EncodeRecord produces the mux-json/1 canonical encoding of the whole
// Record, including its Payload: this is the sole wire producer.
// OccurredAt is canonicalized to UTC before encoding. Host storage must keep
// these exact bytes; a host must never independently serialize a parsed
// copy and assume its bytes match.
func EncodeRecord(r Record) ([]byte, error) {
	const op = "EncodeRecord"
	if len(r.Payload) == 0 {
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: fmt.Errorf("payload is empty")}
	}
	if err := validateRawJSON(r.Payload); err != nil {
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	r.OccurredAt = r.OccurredAt.UTC()
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	canon, err := canonicalize(raw)
	if err != nil {
		return nil, &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	return []byte(canon), nil
}

// DecodeRecord parses mux-json/1 record bytes strictly: an unknown outer
// field, a duplicate key anywhere in the document (including inside
// payload), invalid UTF-8, an unpaired surrogate escape, or trailing
// content after the value are all rejected. It does not itself validate the
// payload against its per-kind schema or check binding identity — see
// ValidateRecord.
func DecodeRecord(data []byte) (Record, error) {
	const op = "DecodeRecord"
	var r Record
	if err := decodeStrict(data, &r); err != nil {
		return Record{}, &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	r.OccurredAt = r.OccurredAt.UTC()
	return r, nil
}

// RecordSHA256 hashes a Record's canonical encoding.
func RecordSHA256(r Record) (string, error) {
	raw, err := EncodeRecord(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateRecord checks r against record.schema.json's framing rules, the
// current trusted Binding, and the per-kind payload schema named by r.Kind.
// A framing or payload defect is InvalidRecord; a record whose session,
// runtime instance, or execution epoch does not match b is StaleBinding.
func ValidateRecord(r Record, b Binding) error {
	const op = "ValidateRecord"
	if err := validateRecordFraming(r); err != nil {
		return &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	if err := validateRecordIdentityRequirements(r); err != nil {
		return &Error{Kind: InvalidRecord, Op: op, Cause: err}
	}
	if err := ValidatePayload(r.Kind, r.Payload); err != nil {
		return err
	}
	if b.SessionID == "" || b.RuntimeInstanceID == "" || b.ExecutionEpoch == 0 {
		return &Error{Kind: InvalidConfig, Op: op, Cause: fmt.Errorf("binding is incomplete")}
	}
	if r.SessionID != b.SessionID || r.RuntimeInstanceID != b.RuntimeInstanceID || r.ExecutionEpoch != b.ExecutionEpoch {
		return &Error{Kind: StaleBinding, Op: op, Cause: fmt.Errorf("record identity does not match current binding")}
	}
	return nil
}

func validateRecordFraming(r Record) error {
	if r.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if !nonemptyMax(r.EventID, 160) {
		return fmt.Errorf("event_id must be 1-160 bytes")
	}
	if !nonemptyMax(r.SessionID, 160) {
		return fmt.Errorf("session_id must be 1-160 bytes")
	}
	if !nonemptyMax(r.RuntimeInstanceID, 160) {
		return fmt.Errorf("runtime_instance_id must be 1-160 bytes")
	}
	if r.ExecutionEpoch == 0 {
		return fmt.Errorf("execution_epoch must be nonzero")
	}
	if r.Kind == "" {
		return fmt.Errorf("kind is empty")
	}
	if _, ok := payloadKinds[r.Kind]; !ok {
		return fmt.Errorf("kind %q is not recognized", r.Kind)
	}
	if !nonemptyMax(r.TurnID, 160) {
		return fmt.Errorf("turn_id must be 1-160 bytes")
	}
	if r.MessageID != "" && !nonemptyMax(r.MessageID, 160) {
		return fmt.Errorf("message_id must be at most 160 bytes")
	}
	if r.ToolCallID != "" && !nonemptyMax(r.ToolCallID, 512) {
		return fmt.Errorf("tool_call_id must be at most 512 bytes")
	}
	if r.OperationID != "" && !nonemptyMax(r.OperationID, 160) {
		return fmt.Errorf("operation_id must be at most 160 bytes")
	}
	if r.BlobRef != "" && !blobRefPattern.MatchString(r.BlobRef) {
		return fmt.Errorf("blob_ref must match sha256:<64 lowercase hex>")
	}
	if len(r.Payload) == 0 {
		return fmt.Errorf("payload is empty")
	}
	return nil
}

// validateRecordIdentityRequirements enforces record.schema.json's allOf
// rules keyed on Kind's prefix: every tool.* kind requires tool_call_id and
// operation_id; every message.* kind requires message_id.
func validateRecordIdentityRequirements(r Record) error {
	if strings.HasPrefix(r.Kind, "tool.") {
		if r.ToolCallID == "" || r.OperationID == "" {
			return fmt.Errorf("kind %q requires tool_call_id and operation_id", r.Kind)
		}
	}
	if strings.HasPrefix(r.Kind, "message.") {
		if r.MessageID == "" {
			return fmt.Errorf("kind %q requires message_id", r.Kind)
		}
	}
	return nil
}

// canonicalize decodes raw with UseNumber (so numeric literal text survives
// exactly) and re-marshals the resulting tree: this is what sorts object
// keys lexically, drops insignificant whitespace, and fixes HTML-escaping
// and no-trailing-newline behavior, for both the json.RawMessage and the
// typed-value paths through EncodePayload alike.
func canonicalize(raw []byte) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var tree any
	if err := dec.Decode(&tree); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if err := expectEOF(dec); err != nil {
		return nil, err
	}
	out, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return json.RawMessage(out), nil
}

// decodeStrict is the one strict-decode entry point shared by ValidatePayload
// and DecodeRecord: it validates data as untrusted raw JSON (UTF-8,
// surrogate pairing, duplicate keys, single top-level value), then decodes
// it into v rejecting unknown fields and trailing content.
func decodeStrict(data []byte, v any) error {
	if err := validateRawJSON(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	return expectEOF(dec)
}

// expectEOF reports an error unless dec has nothing left but the value
// already consumed: it is how both canonicalize and decodeStrict reject
// trailing content after a single top-level JSON value.
func expectEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing content after top-level JSON value")
		}
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// validateRawJSON checks data as untrusted raw JSON, independent of any Go
// target type: valid UTF-8, no unpaired UTF-16 surrogate escape, no
// duplicate object key at any nesting level (even under different escaping
// of the same key), syntactically well-formed, and nothing but the one
// top-level value.
func validateRawJSON(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8")
	}
	if err := checkSurrogatePairing(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := walkNoDuplicateKeys(dec, tok); err != nil {
		return err
	}
	return expectEOF(dec)
}

// walkNoDuplicateKeys recursively confirms every JSON object in the value
// starting at tok has no duplicate key, using a fresh seen-key set per
// object exactly as record.schema.json's additionalProperties:false
// framing implies: a key repeated anywhere else in the document, at a
// different nesting level, is not a duplicate.
func walkNoDuplicateKeys(dec *json.Decoder, tok json.Token) error {
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // scalar: string, json.Number, bool, or nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if seen[key] {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = true
			valTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			if err := walkNoDuplicateKeys(dec, valTok); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // consume closing '}'
			return fmt.Errorf("invalid JSON: %w", err)
		}
	case '[':
		for dec.More() {
			valTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON: %w", err)
			}
			if err := walkNoDuplicateKeys(dec, valTok); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // consume closing ']'
			return fmt.Errorf("invalid JSON: %w", err)
		}
	}
	return nil
}

// checkSurrogatePairing scans data's string literals for \uXXXX escapes and
// rejects any unpaired UTF-16 surrogate: a high surrogate (D800-DBFF) not
// immediately followed by a low surrogate (DC00-DFFF) escape, or a low
// surrogate with no preceding high surrogate. Go's own json decoder accepts
// these and silently substitutes U+FFFD; this runs on the raw bytes before
// that substitution can happen.
func checkSurrogatePairing(data []byte) error {
	inString := false
	for i := 0; i < len(data); {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			i++
			continue
		}
		switch c {
		case '"':
			inString = false
			i++
		case '\\':
			if i+1 >= len(data) {
				return fmt.Errorf("truncated escape sequence")
			}
			if data[i+1] != 'u' {
				i += 2
				continue
			}
			if i+6 > len(data) {
				return fmt.Errorf("truncated unicode escape")
			}
			r, err := parseHex4(data[i+2 : i+6])
			if err != nil {
				return err
			}
			switch {
			case r >= 0xD800 && r <= 0xDBFF:
				if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
					return fmt.Errorf("unpaired UTF-16 surrogate")
				}
				r2, err := parseHex4(data[i+8 : i+12])
				if err != nil {
					return err
				}
				if r2 < 0xDC00 || r2 > 0xDFFF {
					return fmt.Errorf("unpaired UTF-16 surrogate")
				}
				i += 12
			case r >= 0xDC00 && r <= 0xDFFF:
				return fmt.Errorf("unpaired UTF-16 surrogate")
			default:
				i += 6
			}
		default:
			i++
		}
	}
	return nil
}

func parseHex4(b []byte) (rune, error) {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, fmt.Errorf("invalid unicode escape")
		}
	}
	return r, nil
}

var (
	timeType          = reflect.TypeOf(time.Time{})
	rawMessageType    = reflect.TypeOf(json.RawMessage(nil))
	marshalerType     = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// validateTypedValue walks v, rejecting invalid UTF-8 in a string or map
// key, a cyclic map/slice/pointer, and any custom json.Marshaler or
// encoding.TextMarshaler other than the two types the codec explicitly
// supports: time.Time (trusted to encode itself) and json.RawMessage
// (recursively validated as its own untrusted raw JSON). seen tracks
// pointer/map/slice addresses currently being visited on this path, so a
// self-referential value is rejected instead of recursing forever.
func validateTypedValue(v reflect.Value, seen map[uintptr]bool) error {
	if !v.IsValid() {
		return nil
	}
	t := v.Type()
	switch t {
	case timeType:
		return nil
	case rawMessageType:
		raw := v.Interface().(json.RawMessage)
		if len(raw) == 0 {
			return nil
		}
		return validateRawJSON(raw)
	}
	if t.Implements(marshalerType) || t.Implements(textMarshalerType) {
		return fmt.Errorf("unsupported custom JSON marshaler: %s", t)
	}
	switch v.Kind() {
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("invalid UTF-8 string")
		}
		return nil
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return withCycleGuard(v.Pointer(), seen, func() error {
			return validateTypedValue(v.Elem(), seen)
		})
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return validateTypedValue(v.Elem(), seen)
	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		guard := func() error {
			for i := 0; i < v.Len(); i++ {
				if err := validateTypedValue(v.Index(i), seen); err != nil {
					return err
				}
			}
			return nil
		}
		if v.Len() == 0 {
			return guard()
		}
		return withCycleGuard(v.Pointer(), seen, guard)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validateTypedValue(v.Index(i), seen); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		return withCycleGuard(v.Pointer(), seen, func() error {
			iter := v.MapRange()
			for iter.Next() {
				k := iter.Key()
				if k.Kind() == reflect.String && !utf8.ValidString(k.String()) {
					return fmt.Errorf("invalid UTF-8 map key")
				}
				if err := validateTypedValue(iter.Value(), seen); err != nil {
					return err
				}
			}
			return nil
		})
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				continue // unexported field: not visible to encoding/json either
			}
			if err := validateTypedValue(v.Field(i), seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

// withCycleGuard rejects a value whose address is already on the current
// path, otherwise marks it visited for the duration of fn.
func withCycleGuard(ptr uintptr, seen map[uintptr]bool, fn func() error) error {
	if seen[ptr] {
		return fmt.Errorf("cyclic value")
	}
	seen[ptr] = true
	defer delete(seen, ptr)
	return fn()
}
