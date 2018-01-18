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

// An TooManyOutputsRemoteError describes a remote function with too many outputs
type TooManyOutputsRemoteError struct {
	Function   reflect.StructField
	NumOutputs int
}

func (e *TooManyOutputsRemoteError) Error() string {
	return fmt.Sprintf("autorpc: too many outputs for remote function %s, max num outputs is one of type RemotePromise but got %d", e.Function.Name, e.NumOutputs)
}

// An NotRemotePromiseError describes a remote function which does not have the output of type RemotePromise
type NotRemotePromiseError struct {
	Function reflect.StructField
	Type     reflect.Type
}

func (e *NotRemotePromiseError) Error() string {
	return fmt.Sprintf("autorpc: remote function %s must have RemotePromise has output but has %s", e.Function.Name, e.Type.String())
}

// An APIFuncTooManyOutputsError describes a connect api function with too many outputs
type APIFuncTooManyOutputsError struct {
	Function   reflect.Method
	NumOutputs int
}

func (e *APIFuncTooManyOutputsError) Error() string {
	return fmt.Sprintf("autorpc: too many outputs for connect api function %s, max num outputs is two (data and error) but got %d", e.Function.Name, e.NumOutputs)
}

// An APIFuncNotErrorOutputError describes a connect api function which does not have the second output of type error
type APIFuncNotErrorOutputError struct {
	Function reflect.Method
	Type     reflect.Type
}

func (e *APIFuncNotErrorOutputError) Error() string {
	return fmt.Sprintf("autorpc: remote function %s must have error has the second output but has %s", e.Function.Name, e.Type.String())
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
