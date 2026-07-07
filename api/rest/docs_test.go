package rest

import (
	"reflect"
	"testing"

	"runtime.link/api/internal/oas"
	"runtime.link/xyz/enum"
)

// Fruit is a runtime.link/xyz/enum type: a plain named string type whose
// values live in the enum registry rather than in a ValuesJSON method.
type Fruit string

var Fruits = enum.Register[Fruit, struct {
	Apple  Fruit `json:"apple"`
	Banana Fruit `json:"banana"`
}]()

// TestSchemaFor_Enum verifies that an enum type produces a string schema
// whose Enum lists the registered values (via enum.ValuesJSON), even though
// the type has no ValuesJSON method of its own.
func TestSchemaFor_Enum(t *testing.T) {
	schema := schemaFor(nil, Fruit(""))
	if len(schema.Type) != 1 || schema.Type[0] != oas.Types.String {
		t.Fatalf("expected string type, got %v", schema.Type)
	}
	if len(schema.Enum) != 2 {
		t.Fatalf("expected 2 enum values, got %d: %v", len(schema.Enum), schema.Enum)
	}
	if string(schema.Enum[0]) != `"apple"` || string(schema.Enum[1]) != `"banana"` {
		t.Errorf("unexpected enum values: %q, %q", schema.Enum[0], schema.Enum[1])
	}
}

func TestNamespaceName(t *testing.T) {

	type Generic[T any] struct{}
	type Thing struct{}

	type Complex[A, B any] struct{}

	namespace, name := namespaceName(reflect.TypeOf(Generic[Thing]{}))
	if namespace != "rest" {
		t.Fatal("unexpected value")
	}
	if name != "Generic[rest.Thing]" {
		t.Fatal("unexpected value")
	}

	namespace, name = namespaceName(reflect.TypeOf(Complex[Thing, Thing]{}))
	if namespace != "rest" {
		t.Fatal("unexpected value")
	}
	if name != "Complex[rest.Thing, rest.Thing]" {
		t.Fatal("unexpected value")
	}
}
