package nosql

import (
	"errors"
	"testing"
	"time"
)

type base struct {
	ID        uint      `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type widget struct {
	base
	Name   string  `json:"name"`
	Colour string  `db:"colour_column" json:"colour"`
	Note   *string `json:"note"`
	Hidden string  `json:"-"`
	secret string  //nolint:unused // present to prove unexported fields are skipped
}

// TestToDocumentFlattensEmbeddedStructs checks a base model's columns land
// alongside the entity's own rather than nested under a key.
func TestToDocumentFlattensEmbeddedStructs(t *testing.T) {
	doc, err := ToDocument(widget{base: base{ID: 7}, Name: "bolt"})
	if err != nil {
		t.Fatalf("ToDocument: %v", err)
	}

	if doc["id"] != uint(7) {
		t.Errorf("id = %v, want 7 at the top level", doc["id"])
	}
	if doc["name"] != "bolt" {
		t.Errorf("name = %v", doc["name"])
	}
}

// TestToDocumentPrefersDBTag confirms the db tag wins over json.
func TestToDocumentPrefersDBTag(t *testing.T) {
	doc, err := ToDocument(widget{Colour: "red"})
	if err != nil {
		t.Fatalf("ToDocument: %v", err)
	}
	if doc["colour_column"] != "red" {
		t.Errorf("expected the db tag name, got %v", doc)
	}
	if _, present := doc["colour"]; present {
		t.Error("the json name should not also appear")
	}
}

// TestToDocumentSkipsDashAndUnexported keeps fields out of the store that the
// model says are not persisted.
func TestToDocumentSkipsDashAndUnexported(t *testing.T) {
	doc, err := ToDocument(widget{Hidden: "nope", secret: "nope"})
	if err != nil {
		t.Fatalf("ToDocument: %v", err)
	}
	if _, present := doc["-"]; present {
		t.Error(`a json:"-" field was written`)
	}
	if _, present := doc["hidden"]; present {
		t.Error(`a json:"-" field was written under its Go name`)
	}
	if _, present := doc["secret"]; present {
		t.Error("an unexported field was written")
	}
}

// TestToDocumentOmitsNilPointers keeps a nil pointer absent rather than storing
// a typed nil, which is what makes a partial update expressible.
func TestToDocumentOmitsNilPointers(t *testing.T) {
	doc, err := ToDocument(widget{Name: "bolt"})
	if err != nil {
		t.Fatalf("ToDocument: %v", err)
	}
	if _, present := doc["note"]; present {
		t.Error("a nil pointer field should be omitted")
	}

	note := "hello"
	doc, err = ToDocument(widget{Note: &note})
	if err != nil {
		t.Fatalf("ToDocument: %v", err)
	}
	if doc["note"] != "hello" {
		t.Errorf("note = %v, want the dereferenced value", doc["note"])
	}
}

func TestToDocumentAcceptsMapsAndPointers(t *testing.T) {
	if doc, err := ToDocument(Document{"a": 1}); err != nil || doc["a"] != 1 {
		t.Errorf("Document passthrough failed: %v %v", doc, err)
	}
	if doc, err := ToDocument(map[string]any{"b": 2}); err != nil || doc["b"] != 2 {
		t.Errorf("map passthrough failed: %v %v", doc, err)
	}
	if doc, err := ToDocument(&widget{Name: "ptr"}); err != nil || doc["name"] != "ptr" {
		t.Errorf("pointer deref failed: %v %v", doc, err)
	}
}

func TestToDocumentRejectsBadInput(t *testing.T) {
	for name, in := range map[string]any{
		"nil":         nil,
		"nil pointer": (*widget)(nil),
		"scalar":      42,
	} {
		if _, err := ToDocument(in); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("ToDocument(%s) = %v, want ErrInvalidFilter", name, err)
		}
	}
}

// TestDecodeMap covers the read path, including the embedded base struct.
func TestDecodeMap(t *testing.T) {
	var w widget
	err := DecodeMap(map[string]any{
		"id":            uint(9),
		"name":          "nut",
		"colour_column": "blue",
	}, &w)
	if err != nil {
		t.Fatalf("DecodeMap: %v", err)
	}

	if w.ID != 9 {
		t.Errorf("ID = %d, want 9 (embedded field)", w.ID)
	}
	if w.Name != "nut" {
		t.Errorf("Name = %q", w.Name)
	}
	if w.Colour != "blue" {
		t.Errorf("Colour = %q, want the db-tagged column", w.Colour)
	}
}

// TestDecodeMapConvertsNumericTypes matters because drivers return int64 where
// the model declares uint.
func TestDecodeMapConvertsNumericTypes(t *testing.T) {
	var w widget
	if err := DecodeMap(map[string]any{"id": int64(11)}, &w); err != nil {
		t.Fatalf("DecodeMap: %v", err)
	}
	if w.ID != 11 {
		t.Errorf("ID = %d, want 11 converted from int64", w.ID)
	}
}

// TestDecodeMapAllocatesPointerFields checks a pointer field is populated rather
// than left nil.
func TestDecodeMapAllocatesPointerFields(t *testing.T) {
	var w widget
	if err := DecodeMap(map[string]any{"note": "filled"}, &w); err != nil {
		t.Fatalf("DecodeMap: %v", err)
	}
	if w.Note == nil || *w.Note != "filled" {
		t.Fatalf("Note = %v, want a pointer to \"filled\"", w.Note)
	}
}

// TestDecodeMapSkipsUnknownAndNil confirms schema drift does not crash a read.
func TestDecodeMapSkipsUnknownAndNil(t *testing.T) {
	w := widget{Name: "keep"}
	err := DecodeMap(map[string]any{
		"unknown_column": "ignored",
		"name":           nil,
	}, &w)
	if err != nil {
		t.Fatalf("DecodeMap: %v", err)
	}
	if w.Name != "keep" {
		t.Errorf("Name = %q; a nil value should leave the field alone", w.Name)
	}
}

func TestDecodeMapSlice(t *testing.T) {
	rows := []map[string]any{
		{"id": uint(1), "name": "a"},
		{"id": uint(2), "name": "b"},
	}

	var out []widget
	if err := DecodeMapSlice(rows, &out); err != nil {
		t.Fatalf("DecodeMapSlice: %v", err)
	}
	if len(out) != 2 || out[0].Name != "a" || out[1].ID != 2 {
		t.Fatalf("DecodeMapSlice = %+v", out)
	}

	var ptrOut []*widget
	if err := DecodeMapSlice(rows, &ptrOut); err != nil {
		t.Fatalf("DecodeMapSlice into a pointer slice: %v", err)
	}
	if len(ptrOut) != 2 || ptrOut[0].Name != "a" {
		t.Fatalf("DecodeMapSlice(ptr) = %+v", ptrOut)
	}
}

func TestDecodeRejectsBadDestinations(t *testing.T) {
	var w widget
	if err := DecodeMap(nil, w); !errors.Is(err, ErrInvalidFilter) {
		t.Error("DecodeMap into a non-pointer should fail")
	}
	if err := DecodeMap(nil, &[]widget{}); !errors.Is(err, ErrInvalidFilter) {
		t.Error("DecodeMap into a slice should fail")
	}
	if err := DecodeMapSlice(nil, &w); !errors.Is(err, ErrInvalidFilter) {
		t.Error("DecodeMapSlice into a struct should fail")
	}
	if err := DecodeMapSlice(nil, &[]int{}); !errors.Is(err, ErrInvalidFilter) {
		t.Error("DecodeMapSlice into a slice of scalars should fail")
	}
}

func TestBuildFindOptions(t *testing.T) {
	o := BuildFindOptions(
		WithLimit(10),
		WithSkip(20),
		WithSort(Sort{Field: "name", Direction: Descending}),
		WithProjection("id", "name"),
		nil, // a nil option must be tolerated
	)

	if o.Limit != 10 || o.Skip != 20 {
		t.Errorf("Limit/Skip = %d/%d", o.Limit, o.Skip)
	}
	if len(o.Sort) != 1 || o.Sort[0].Direction != Descending {
		t.Errorf("Sort = %+v", o.Sort)
	}
	if len(o.Projection) != 2 {
		t.Errorf("Projection = %v", o.Projection)
	}
}
