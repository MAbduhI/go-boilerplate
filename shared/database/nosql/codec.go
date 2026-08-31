package nosql

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// This file converts between Go structs and the map[string]any shape that
// row-oriented backends work in. Mongo and DynamoDB have their own codecs
// (bson and attributevalue); Cassandra does not, so these helpers fill that gap
// and stay in the contract package where any future backend can reuse them.
//
// Field names come from the `db` tag, falling back to `json`, falling back to
// the lower-cased field name — so a model already tagged for JSON needs no extra
// annotation.

// fieldName resolves the column name for a struct field, returning ok=false
// when the field should be skipped.
func fieldName(f reflect.StructField) (string, bool) {
	if f.PkgPath != "" {
		return "", false // unexported
	}
	for _, tag := range []string{"db", "json"} {
		if v, present := f.Tag.Lookup(tag); present {
			name := strings.SplitN(v, ",", 2)[0]
			if name == "-" {
				return "", false
			}
			if name != "" {
				return name, true
			}
		}
	}
	return strings.ToLower(f.Name), true
}

// ToDocument flattens a struct, map or Document into a Document.
//
// Embedded structs are flattened into the parent, matching how a base model's
// columns sit alongside the entity's own.
func ToDocument(v any) (Document, error) {
	switch typed := v.(type) {
	case nil:
		return nil, fmt.Errorf("%w: document is nil", ErrInvalidFilter)
	case Document:
		return typed, nil
	case map[string]any:
		return Document(typed), nil
	}

	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("%w: document is a nil pointer", ErrInvalidFilter)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%w: want a struct or map, got %s", ErrInvalidFilter, rv.Kind())
	}

	out := Document{}
	flattenStruct(rv, out)
	return out, nil
}

func flattenStruct(rv reflect.Value, out Document) {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		fv := rv.Field(i)

		// An anonymous struct field is a column group, not a column, so its
		// fields are lifted into the parent document.
		if f.Anonymous && fv.Kind() == reflect.Struct && !isTimeType(fv.Type()) {
			flattenStruct(fv, out)
			continue
		}

		name, ok := fieldName(f)
		if !ok {
			continue
		}
		// A nil pointer means "no value", which must stay absent rather than
		// being written as a typed nil.
		if fv.Kind() == reflect.Pointer {
			if fv.IsNil() {
				continue
			}
			fv = fv.Elem()
		}
		out[name] = fv.Interface()
	}
}

func isTimeType(t reflect.Type) bool { return t == reflect.TypeOf(time.Time{}) }

// DecodeMap copies m into dest, which must be a pointer to a struct.
func DecodeMap(m map[string]any, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: dest must be a non-nil pointer", ErrInvalidFilter)
	}
	elem := rv.Elem()
	if elem.Kind() != reflect.Struct {
		return fmt.Errorf("%w: dest must point to a struct, got %s", ErrInvalidFilter, elem.Kind())
	}
	assignStruct(m, elem)
	return nil
}

// DecodeMapSlice copies rows into dest, which must be a pointer to a slice of
// structs.
func DecodeMapSlice(rows []map[string]any, dest any) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: dest must be a non-nil pointer", ErrInvalidFilter)
	}
	slice := rv.Elem()
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("%w: dest must point to a slice, got %s", ErrInvalidFilter, slice.Kind())
	}

	elemType := slice.Type().Elem()
	isPtr := elemType.Kind() == reflect.Pointer
	structType := elemType
	if isPtr {
		structType = elemType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return fmt.Errorf("%w: slice element must be a struct, got %s", ErrInvalidFilter, structType.Kind())
	}

	// Allocate exactly once: the row count is already known.
	out := reflect.MakeSlice(slice.Type(), 0, len(rows))
	for _, row := range rows {
		item := reflect.New(structType)
		assignStruct(row, item.Elem())
		if isPtr {
			out = reflect.Append(out, item)
		} else {
			out = reflect.Append(out, item.Elem())
		}
	}
	slice.Set(out)
	return nil
}

// assignStruct writes matching entries of m onto the fields of rv.
func assignStruct(m map[string]any, rv reflect.Value) {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		fv := rv.Field(i)

		if f.Anonymous && fv.Kind() == reflect.Struct && !isTimeType(fv.Type()) {
			assignStruct(m, fv)
			continue
		}

		name, ok := fieldName(f)
		if !ok || !fv.CanSet() {
			continue
		}
		raw, present := m[name]
		if !present || raw == nil {
			continue
		}
		assignValue(fv, reflect.ValueOf(raw))
	}
}

// assignValue sets dst from src, converting when the types differ but are
// convertible. A value that cannot be assigned is skipped rather than panicking:
// a schema that has drifted should not crash a read.
func assignValue(dst, src reflect.Value) {
	if !src.IsValid() {
		return
	}

	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		assignValue(dst.Elem(), src)
		return
	}

	switch {
	case src.Type().AssignableTo(dst.Type()):
		dst.Set(src)
	case src.Type().ConvertibleTo(dst.Type()):
		dst.Set(src.Convert(dst.Type()))
	}
}
