package jsn

import "unsafe"

const fixedTapeLimit = 4096

type fixedInstr struct {
	literal []byte
	offset  uintptr
	op      uint8
}

func buildFixedTape(tape *tapeEncoder) encoderFunc {
	size := len(tape.tail)
	if size > fixedTapeLimit {
		return nil
	}

	for index := range tape.instrs {
		instruction := &tape.instrs[index]
		if instruction.indirect || instruction.op < opBool || instruction.op > opUint64 {
			return nil
		}

		size += len(instruction.lit) + 20
		if size > fixedTapeLimit {
			return nil
		}
	}

	instructions := make([]fixedInstr, len(tape.instrs))

	for index := range tape.instrs {
		source := &tape.instrs[index]
		literal := source.lit

		if source.op == opBool {
			// Fuse the field name and boolean into one literal copy. Both choices
			// live in one allocation; the true choice is one byte shorter.
			literal = make([]byte, 0, len(source.lit)*2+9)
			literal = append(literal, source.lit...)
			literal = append(literal, "true"...)
			literal = append(literal, source.lit...)
			literal = append(literal, "false"...)
		}

		instructions[index] = fixedInstr{
			literal: literal,
			offset:  source.offset,
			op:      source.op,
		}
	}

	tail := tape.tail

	return func(enc *Encoder, buffer []byte, ptr unsafe.Pointer) ([]byte, error) {
		// No callbacks, pointers, or unbounded values occur in this plan. One
		// reservation covers the entire record, including its closing literals.
		buffer = reserveBytes(buffer, size)

		for index := range instructions {
			instruction := &instructions[index]
			field := unsafe.Add(ptr, instruction.offset)

			if instruction.op == opBool {
				middle := (len(instruction.literal) - 1) / 2

				if *(*bool)(field) {
					buffer = append(buffer, instruction.literal[:middle]...)
				} else {
					buffer = append(buffer, instruction.literal[middle:]...)
				}

				continue
			}

			buffer = append(buffer, instruction.literal...)

			switch instruction.op {
			case opInt:
				buffer = appendInt64Fast(buffer, int64(*(*int)(field)))
			case opInt8:
				buffer = appendInt64Fast(buffer, int64(*(*int8)(field)))
			case opInt16:
				buffer = appendInt64Fast(buffer, int64(*(*int16)(field)))
			case opInt32:
				buffer = appendInt64Fast(buffer, int64(*(*int32)(field)))
			case opInt64:
				buffer = appendInt64Fast(buffer, *(*int64)(field))
			case opUint:
				buffer = appendUint64Fast(buffer, uint64(*(*uint)(field)))
			case opUint8:
				buffer = appendUint64Fast(buffer, uint64(*(*uint8)(field)))
			case opUint16:
				buffer = appendUint64Fast(buffer, uint64(*(*uint16)(field)))
			case opUint32:
				buffer = appendUint64Fast(buffer, uint64(*(*uint32)(field)))
			case opUint64:
				buffer = appendUint64Fast(buffer, *(*uint64)(field))
			}
		}

		return append(buffer, tail...), nil
	}
}

func buildFixedSlice(encode encoderFunc, elementSize uintptr) encoderFunc {
	return func(enc *Encoder, buffer []byte, ptr unsafe.Pointer) ([]byte, error) {
		header := (*sliceHeader)(ptr)
		if header.Data == nil {
			return append(buffer, "null"...), nil
		}

		buffer = append(buffer, '[')

		for index := 0; index < header.Len; index++ {
			if index != 0 {
				buffer = append(buffer, ',')
			}

			var err error

			buffer, err = encode(enc, buffer, unsafe.Add(header.Data, uintptr(index)*elementSize))
			if err != nil {
				return buffer, err
			}

			buffer, err = enc.maybeFlush(buffer)
			if err != nil {
				return buffer, err
			}
		}

		return append(buffer, ']'), nil
	}
}
