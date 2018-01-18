package autorpc

import (
	"fmt"
	"reflect"
)

// An InvalidConnectionAPIError describes an invalid Connection API passed to CreateHandler.
// (The argument to CreateHandler must be a non-nil pointer.)
type InvalidConnectionAPIError struct {
	Type reflect.Type
}

func (e *InvalidConnectionAPIError) Error() string {
	if e.Type == nil {
		return "autorpc: CreateHandler(nil)"
	}

	if e.Type.Kind() != reflect.Ptr {
		return "autorpc: CreateHandler(non-pointer " + e.Type.String() + ")"
	}

	return "autorpc: CreateHandler(nil " + e.Type.String() + ")"
}

// An MultipleFieldsError describes a Connection API with multiple fields with a specific tag
type MultipleFieldsError struct {
	Tag              string
	ConflictingField string
	PreviousField    string
}

func (e *MultipleFieldsError) Error() string {
	return fmt.Sprintf("autorpc: multiple fields found with tag %s in Connection API, only one allowed (found: %s and %s)", e.Tag, e.PreviousField, e.ConflictingField)
}

// An UnexportedFieldError describes an unexported field that was intended to be managed by the handler
type UnexportedFieldError struct {
	Field reflect.StructField
	Type  reflect.Type
}

func (e *UnexportedFieldError) Error() string {
	return fmt.Sprintf("autorpc: %s must be exported in %s to allow assignment of a value", e.Field.Name, e.Type.String())
}

// An ValueTypeMismatchError describes a type mismatch when assigning the value in the Connection API using the method Handle
type ValueTypeMismatchError struct {
	Expected reflect.Type
	Received reflect.Type
}

func (e *ValueTypeMismatchError) Error() string {
	return fmt.Sprintf("autorpc: value provided to Handle does not match type of value in Connection API (wanted %s but got %s)", e.Expected.String(), e.Received.String())
}

// An RemoteFieldTypeError describes a type error when setting up the remote functions for the Connection API
type RemoteFieldTypeError struct {
	Type reflect.Type
}

func (e *RemoteFieldTypeError) Error() string {
	return fmt.Sprintf("autorpc: expected a struct has remote in the Connection API but got %s", e.Type.Kind())
}

// An RPCError describes an error in the RPC service
type RPCError struct {
	Err       string
	ActualErr error
}

func (e *RPCError) Error() string {
	if e.Err == "" {
		return fmt.Sprintf("autorpc: internal error %s", e.ActualErr)
	}
	return fmt.Sprintf("autorpc: %s", e.Err)
}

// An RemoteFuncError describes an error in the declaration of a remote function
type RemoteFuncError struct {
	Err      string
	Function reflect.StructField
}

func (e *RemoteFuncError) Error() string {
	return fmt.Sprintf("autorpc: %s in remote function %s", e.Err, e.Function.Name)
}
