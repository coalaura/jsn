package jsn

import (
	"encoding"
	"reflect"
	"unsafe"
)

type mapScratch struct {
	key      reflect.Value
	value    reflect.Value
	keyPtr   unsafe.Pointer
	valuePtr unsafe.Pointer
	next     *mapScratch
	pool     *mapScratchPool
}

type mapScratchPool struct {
	free *mapScratch
}

func (enc *Encoder) takeMapScratch(tp reflect.Type, copyValue bool) *mapScratch {
	if enc.mapScratch == nil {
		enc.mapScratch = make(map[reflect.Type]*mapScratchPool, 1)
	}

	pool := enc.mapScratch[tp]
	if pool == nil {
		pool = new(mapScratchPool)
		enc.mapScratch[tp] = pool
	}

	scratch := pool.free
	if scratch != nil {
		pool.free = scratch.next
		scratch.next = nil

		return scratch
	}

	key := reflect.New(tp.Key())

	scratch = &mapScratch{
		key:    key.Elem(),
		keyPtr: key.UnsafePointer(),
		pool:   pool,
	}

	if !copyValue {
		value := reflect.New(tp.Elem())
		scratch.value = value.Elem()
		scratch.valuePtr = value.UnsafePointer()
	}

	return scratch
}

func (scratch *mapScratch) release() {
	// Recycled slots must not keep previously encoded object graphs alive.
	scratch.key.SetZero()

	if scratch.value.IsValid() {
		scratch.value.SetZero()
	}

	scratch.next = scratch.pool.free
	scratch.pool.free = scratch
}

func buildMapEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	keyEncoder := buildMapKeyEncoder(tp.Key())
	valueType := tp.Elem()
	valueEncoder := buildEncoder(valueType, visiting)
	copyValue := mapValueNeedsCopy(valueType)

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		value := reflect.NewAt(tp, ptr).Elem()
		if value.IsNil() {
			return append(b, "null"...), nil
		}

		if value.Len() == 0 {
			return append(b, '{', '}'), nil
		}

		// Taking a slot removes it from the free list, so recursive maps of
		// the same type cannot overwrite an outer entry still being encoded.
		scratch := enc.takeMapScratch(tp, copyValue)
		defer scratch.release()

		var iterator reflect.MapIter

		iterator.Reset(value)
		b = append(b, '{')
		first := true

		for iterator.Next() {
			if !first {
				b = append(b, ',')
			}

			first = false
			scratch.key.SetIterKey(&iterator)

			var err error

			b, err = keyEncoder(enc, b, scratch.keyPtr)
			if err != nil {
				return b, err
			}

			valuePtr := scratch.valuePtr

			if copyValue {
				// A pointer-receiver callback may retain the address of this
				// entry (or one of its fields). Preserve that copy's lifetime.
				entry := reflect.New(valueType)
				entry.Elem().SetIterValue(&iterator)
				valuePtr = entry.UnsafePointer()
			} else {
				scratch.value.SetIterValue(&iterator)
			}

			b = append(b, ':')

			b, err = valueEncoder(enc, b, valuePtr)
			if err != nil {
				return b, err
			}

			b, err = enc.maybeFlush(b)
			if err != nil {
				return b, err
			}
		}

		return append(b, '}'), nil
	}
}

func buildMapKeyEncoder(tp reflect.Type) encoderFunc {
	if tp.Kind() == reflect.String {
		return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return writeString(b, *(*string)(ptr)), nil
		}
	}

	if tp.Implements(textMarshalerType) {
		typePtr := extractTypePtr(tp)
		direct := isDirectInterface(tp)

		return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			if tp.Kind() == reflect.Pointer && *(*unsafe.Pointer)(ptr) == nil {
				return append(b, '"', '"'), nil
			}

			var key any

			if tp.Kind() == reflect.Interface {
				key = reflect.NewAt(tp, ptr).Elem().Interface()
			} else {
				words := (*eface)(unsafe.Pointer(&key))
				words.typ = typePtr
				words.word = ptr

				if direct {
					words.word = *(*unsafe.Pointer)(ptr)
				}
			}

			text, err := key.(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return b, err
			}

			return writeString(b, string(text)), nil
		}
	}

	if tp.Kind() >= reflect.Int && tp.Kind() <= reflect.Int64 {
		return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			b = append(b, '"')
			b = appendInt64Fast(b, reflect.NewAt(tp, ptr).Elem().Int())

			return append(b, '"'), nil
		}
	}

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		b = append(b, '"')
		b = appendUint64Fast(b, reflect.NewAt(tp, ptr).Elem().Uint())

		return append(b, '"'), nil
	}
}

func mapValueNeedsCopy(tp reflect.Type) bool {
	if tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Interface {
		return false
	}

	if tp == timeType || tp == rawMessageType {
		return false
	}

	// Match encoder precedence: a pointer WriterMarshaler can override a
	// value ByteMarshaler, so checking all value methods first is unsafe.
	marshalers := [...]reflect.Type{writerMarshalerType, byteMarshalerType, textMarshalerType}

	for _, marshaler := range marshalers {
		if tp.Implements(marshaler) {
			return false
		}

		if reflect.PointerTo(tp).Implements(marshaler) {
			return true
		}
	}

	switch tp.Kind() {
	case reflect.Array:
		return tp.Len() != 0 && mapValueNeedsCopy(tp.Elem())
	case reflect.Struct:
		for _, field := range resolveStructFields(tp) {
			if len(field.parents) != 0 || field.typ.Kind() == reflect.Pointer || field.typ.Kind() == reflect.Interface {
				continue
			}

			if hasTagOption(field.opts, "omitzero") && !field.typ.Implements(isZeroerType) && reflect.PointerTo(field.typ).Implements(isZeroerType) {
				return true
			}

			if mapValueNeedsCopy(field.typ) {
				return true
			}
		}
	}

	return false
}
