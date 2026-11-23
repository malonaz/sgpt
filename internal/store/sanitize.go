package store

import (
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// replaceNUL marks where a NUL stood: Postgres refuses it in text and jsonb
// (SQLSTATE 22P05), so a single one in a tool's output (a binary file, a
// command's stdout) would fail every write of the message holding it.
var replaceNUL = strings.NewReplacer("\x00", "\uFFFD")

// sanitize replaces NUL characters in every string of the message, in place.
func sanitize(message proto.Message) {
	sanitizeMessage(message.ProtoReflect())
}

func sanitizeMessage(message protoreflect.Message) {
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList():
			list := value.List()
			for i := range list.Len() {
				if sanitized, ok := sanitizeValue(field, list.Get(i)); ok {
					list.Set(i, sanitized)
				}
			}
		case field.IsMap():
			mapValue := value.Map()
			mapValue.Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
				if sanitized, ok := sanitizeValue(field.MapValue(), value); ok {
					mapValue.Set(key, sanitized)
				}
				return true
			})
		default:
			if sanitized, ok := sanitizeValue(field, value); ok {
				message.Set(field, sanitized)
			}
		}
		return true
	})
}

// sanitizeValue sanitizes a message in place, and returns a replacement for a
// string holding a NUL.
func sanitizeValue(field protoreflect.FieldDescriptor, value protoreflect.Value) (protoreflect.Value, bool) {
	switch field.Kind() {
	case protoreflect.StringKind:
		if text := value.String(); strings.Contains(text, "\x00") {
			return protoreflect.ValueOfString(replaceNUL.Replace(text)), true
		}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		sanitizeMessage(value.Message())
	}
	return value, false
}
