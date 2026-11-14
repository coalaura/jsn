package jsn

import (
	"reflect"
	"slices"
	"strings"
	"unicode"
)

type resolvedField struct {
	name      string
	opts      string
	typ       reflect.Type
	parents   []uintptr
	index     []int
	offset    uintptr
	tagged    bool
	ambiguous bool
}

func resolveStructFields(tp reflect.Type) []resolvedField {
	fields := make([]resolvedField, 0, tp.NumField())
	names := make(map[string]int, tp.NumField())
	ancestors := make(map[reflect.Type]bool)

	var walk func(reflect.Type, uintptr, []uintptr, []int)

	walk = func(current reflect.Type, base uintptr, parents []uintptr, index []int) {
		if ancestors[current] {
			return
		}

		ancestors[current] = true

		for position := range current.NumField() {
			field := current.Field(position)

			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}

			if !field.IsExported() && (!field.Anonymous || embedded.Kind() != reflect.Struct) {
				continue
			}

			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}

			name, opts, _ := strings.Cut(tag, ",")
			if !validFieldName(name) {
				name = ""
			}

			fieldIndex := make([]int, len(index)+1)
			copy(fieldIndex, index)
			fieldIndex[len(index)] = position
			offset := base + field.Offset

			if field.Anonymous && name == "" && embedded.Kind() == reflect.Struct {
				if field.Type.Kind() == reflect.Pointer {
					fieldParents := make([]uintptr, len(parents)+1)
					copy(fieldParents, parents)
					fieldParents[len(parents)] = offset

					walk(embedded, 0, fieldParents, fieldIndex)
				} else {
					walk(embedded, offset, parents, fieldIndex)
				}

				continue
			}

			tagged := name != ""

			if name == "" {
				name = field.Name
			}

			candidate := resolvedField{
				name:    name,
				opts:    opts,
				typ:     field.Type,
				parents: parents,
				index:   fieldIndex,
				offset:  offset,
				tagged:  tagged,
			}

			previous, exists := names[name]
			if !exists {
				names[name] = len(fields)
				fields = append(fields, candidate)

				continue
			}

			selected := &fields[previous]
			sameDepth := len(fieldIndex) == len(selected.index)

			if len(fieldIndex) < len(selected.index) || sameDepth && tagged && !selected.tagged {
				*selected = candidate
			} else if sameDepth && tagged == selected.tagged {
				selected.ambiguous = true
			}
		}

		delete(ancestors, current)
	}

	walk(tp, 0, nil, nil)

	fields = slices.DeleteFunc(fields, func(field resolvedField) bool {
		return field.ambiguous
	})

	slices.SortFunc(fields, func(left, right resolvedField) int {
		return slices.Compare(left.index, right.index)
	})

	return fields
}

func hasTagOption(opts, option string) bool {
	for current := range strings.SplitSeq(opts, ",") {
		if current == option {
			return true
		}
	}

	return false
}

func validFieldName(name string) bool {
	for _, value := range name {
		if strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", value) || unicode.IsLetter(value) || unicode.IsDigit(value) {
			continue
		}

		return false
	}

	return name != ""
}
