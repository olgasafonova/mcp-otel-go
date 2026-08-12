package mcpotel

import "reflect"

// isNilRef reports whether v is nil, or a non-nil interface value wrapping a
// nil pointer.
//
// The go-sdk hands this middleware two interfaces whose concrete types are
// always pointers: mcp.Params (from Request.GetParams) and mcp.Session (from
// Request.GetSession). Both are returned verbatim from a struct field, so an
// unpopulated field produces a NON-nil interface holding a nil pointer, and a
// plain `v == nil` compare is false against it. Calling any method then
// dereferences the nil receiver.
//
// That shape is not malformed input. MCP makes `params` OPTIONAL on
// notifications, so a compliant peer sending notifications/initialized with no
// params member produces exactly it, and every accessor on the params types is
// promoted from an embedded Meta VALUE, which dereferences on call.
//
// The go-sdk carries its own Params.isNil for this reason, but the method is
// unexported and unreachable from outside package mcp, so this reaches for
// reflect instead. IsNil panics on non-nillable kinds, hence the Kind guard.
func isNilRef(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return rv.IsNil()
	default:
		return false
	}
}
