// ABOUTME: Owned copies of llm.Message and llm.ContentBlock values, so a
// ABOUTME: caller holding one is immune to later mutation of the original.
//
// CloneBlock, CloneMessage, and CloneMessages copy exactly what a
// ContentBlock is documented to hold: JSON-compatible values inside Input
// (map[string]any, []any, nested combinations of the two, and scalars
// including json.Number), Source.Bytes, and Replay.Data. This is
// JSON-container ownership, not an arbitrary Go object-graph copier.
//
// A value in Input that reflect cannot walk as a map, slice, array, or
// interface — a pointer to an application struct, a channel, anything
// outside those four kinds — passes through unchanged and aliased to the
// original. That is deliberate, not a gap: such a value is not valid
// tool-call input to begin with, and the durable serialization boundary
// (json.Marshal, at the point a snapshot is actually persisted) is what
// rejects it. A successful clone is not proof a value can be serialized.
//
// A struct in Input is the one case that reasoning does not cover, so it
// gets stated on its own: it is shallow-copied, and json.Marshal accepts
// it. Its slice, map, and pointer fields stay shared with the original, so
// mutating the clone reaches back into the source and no later boundary
// catches it. Input is meant to hold decoded JSON, where a bare struct
// cannot arise; a caller assembling a tool call by hand must not put one
// there.
//
// Cloning walks the value with reflect rather than round-tripping through
// json.Marshal/Unmarshal, which would silently turn json.Number and other
// exact numeric encodings into float64 and lose precision above 2^53.
//
// A seen map keyed by a value's type, address, and length breaks cycles: a
// self-referential map or slice clones to an equally self-referential
// clone instead of recursing until the goroutine stack overflows. The
// source graph stays reachable for the entire call, so no address in the
// seen map can be reused out from under it; addresses are compared for
// identity only, never dereferenced.
package llmcopy

import (
	"reflect"
	"slices"

	"github.com/2389-research/mux/llm"
)

// cloneKey identifies one map or slice value being cloned, so a cyclic or
// repeated reference to it resolves to the clone already in progress
// instead of being walked again.
type cloneKey struct {
	Type    reflect.Type
	Pointer uintptr
	Length  int
}

// cloneValue returns an owned copy of v. Maps, slices, and arrays are
// copied recursively; every other kind is returned as-is (see the package
// doc for what that means for unsupported values).
func cloneValue(v reflect.Value, seen map[cloneKey]reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneValue(v.Elem(), seen))
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := cloneKey{v.Type(), uintptr(v.UnsafePointer()), v.Len()}
		if prior, ok := seen[key]; ok {
			return prior
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		seen[key] = out
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), cloneValue(iter.Value(), seen))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type())
		}
		key := cloneKey{v.Type(), uintptr(v.UnsafePointer()), v.Len()}
		if prior, ok := seen[key]; ok {
			return prior
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		seen[key] = out
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i), seen))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(cloneValue(v.Index(i), seen))
		}
		return out
	default:
		return v
	}
}

// CloneBlock returns a ContentBlock whose Input map, Source.Bytes, and
// Replay.Data are independent of block's: mutating the clone's copies
// never reaches back into block, and mutating block afterward never
// reaches the clone. See the package doc for the limits of "independent"
// here.
func CloneBlock(block llm.ContentBlock) llm.ContentBlock {
	if block.Input != nil {
		block.Input = cloneValue(reflect.ValueOf(block.Input), make(map[cloneKey]reflect.Value)).Interface().(map[string]any)
	}
	if block.Source != nil {
		source := *block.Source
		source.Bytes = slices.Clone(source.Bytes)
		block.Source = &source
	}
	if block.Replay != nil {
		replay := *block.Replay
		replay.Data = slices.Clone(replay.Data)
		block.Replay = &replay
	}
	return block
}

// CloneMessage returns a Message whose Blocks slice, and every block in
// it, are independent of message's, per CloneBlock.
func CloneMessage(message llm.Message) llm.Message {
	if message.Blocks != nil {
		blocks := make([]llm.ContentBlock, len(message.Blocks))
		for i, block := range message.Blocks {
			blocks[i] = CloneBlock(block)
		}
		message.Blocks = blocks
	}
	return message
}

// CloneMessages returns a slice of Messages independent of messages, per
// CloneMessage. A nil slice clones to nil; a non-nil empty slice clones to
// a non-nil empty slice.
func CloneMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return nil
	}
	result := make([]llm.Message, len(messages))
	for i, message := range messages {
		result[i] = CloneMessage(message)
	}
	return result
}
