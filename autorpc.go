package autorpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
)

// A RemoteResult contains the result of a RPC call.
type RemoteResult json.RawMessage

// Decode transforms the contents of the RemoteResult into the provided interface.
func (rr *RemoteResult) Decode(v interface{}) error {
	return json.Unmarshal(*rr, v)
}

// A RemotePromise holds the promise to be executed when the result of a RPC call is available.
type RemotePromise struct {
	handler  *handler
	r        io.Reader
	w        io.Writer
	result   RemoteResult
	err      error
	returned bool
	fns      []func(RemoteResult, error)
}

func newRemotePromise(handler *handler, r io.Reader, w io.Writer) *RemotePromise {
	return &RemotePromise{
		handler: handler,
		r:       r,
		w:       w,
	}
}

func (promise *RemotePromise) resolve(result RemoteResult) {
	promise.handler.r = promise.r
	promise.handler.w = promise.w
	for _, fn := range promise.fns {
		fn(result, nil)
	}
	promise.result = result
}

func (promise *RemotePromise) reject(err error) {
	for _, fn := range promise.fns {
		fn(nil, err)
	}
	promise.err = err
}

// Then allows to register new callbacks to be executed when the remote result is available.
func (promise *RemotePromise) Then(fn func(RemoteResult, error)) {
	if promise.returned {
		fn(promise.result, promise.err)
	} else {
		promise.fns = append(promise.fns, fn)
	}
}

// A Handler executes the RPC calls from the provided reader.
type Handler interface {
	// SetValue sets the value on the Connection API.
	SetValue(interface{}) error
	// SetRW sets the reader and writer for the RPC.
	SetRW(io.Reader, io.Writer)
	// Handle reads the RPC calls, executes them and then writes the result.
	Handle() error
}

type handler struct {
	pendingCalls map[int]*RemotePromise
	fnHandlers   map[string]func([]json.RawMessage) (interface{}, error)
	r            io.Reader
	w            io.Writer
	setValue     func(interface{}) error
}

type rpcCall struct {
	CallID   int               `json:"c"`
	Args     []json.RawMessage `json:"a"`
	FuncName string            `json:"f"`
}

type rpcCallReturn struct {
	CallID int         `json:"c"`
	Error  string      `json:"e,omitempty"`
	Data   interface{} `json:"d,omitempty"`
}

type rpcMessage struct {
	CallID   int               `json:"c"`
	FuncName string            `json:"f,omitempty"`
	Args     []json.RawMessage `json:"a,omitempty"`
	Error    string            `json:"e,omitempty"`
	Data     json.RawMessage   `json:"d,omitempty"`
}

func (msg *rpcMessage) isCall() bool {
	return msg.FuncName != ""
}

func (msg *rpcMessage) isResponse() bool {
	return msg.FuncName == ""
}

func (msg *rpcMessage) isRPCError() bool {
	return strings.HasPrefix(msg.Error, "autorpc:")
}

func (handler *handler) newCall(promise *RemotePromise) int {
	var callID int
	for {
		callID = rand.Int()
		if _, exists := handler.pendingCalls[callID]; !exists {
			handler.pendingCalls[callID] = promise
			return callID
		}
	}
}

func (handler *handler) send(v interface{}) error {
	return json.NewEncoder(handler.w).Encode(v)
}

// SetValue sets the value on the Connection API.
func (handler *handler) SetValue(v interface{}) error {
	return handler.setValue(v)
}

// SetRW sets the reader and writer for the RPC.
func (handler *handler) SetRW(r io.Reader, w io.Writer) {
	handler.r = r
	handler.w = w
}

// Handle reads the RPC calls, executes them and then writes the result.
func (handler *handler) Handle() error {
	decoder := json.NewDecoder(handler.r)
	msg := rpcMessage{}
	err := decoder.Decode(&msg)
	if err != nil {
		if err == io.EOF {
			return err
		}

		handler.send(&rpcCallReturn{CallID: msg.CallID, Error: "autorpc: internal error"})
		return err
	}

	if msg.isCall() {
		fn, ok := handler.fnHandlers[msg.FuncName]
		if !ok {
			err := &RPCError{Err: "function not found"}
			handler.send(&rpcCallReturn{CallID: msg.CallID, Error: err.Error()})
			return err
		}

		result, err := fn(msg.Args)
		errString := ""
		if err != nil {
			errString = err.Error()
			result = nil
		}

		writeErr := handler.send(&rpcCallReturn{CallID: msg.CallID, Error: errString, Data: result})
		if writeErr != nil {
			return &RPCError{ActualErr: writeErr}
		}
	} else if msg.isResponse() {
		promise, exists := handler.pendingCalls[msg.CallID]
		if exists {
			if msg.Error != "" {
				promise.reject(errors.New(msg.Error))
			} else {
				promise.resolve(RemoteResult(msg.Data))
			}
			delete(handler.pendingCalls, msg.CallID)
		}
	} else if msg.isRPCError() {
		promise, exists := handler.pendingCalls[msg.CallID]
		if exists {
			promise.reject(errors.New(msg.Error))
			delete(handler.pendingCalls, msg.CallID)
		}
		return &RPCError{Err: strings.TrimPrefix(msg.Error, "autorpc: ")}
	}

	return nil
}

// CreateHandler creates a handler for the provided Connection API.
func CreateHandler(connAPI interface{}) (Handler, error) {
	rv := reflect.ValueOf(connAPI)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return nil, &InvalidConnectionAPIError{reflect.TypeOf(connAPI)}
	}

	var valueField, remoteField int = -1, -1
	re := rv.Elem()
	rt := re.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		tag := field.Tag.Get("autorpc")
		if tag == "value" {
			if valueField != -1 {
				return nil, &MultipleFieldsError{"value", rt.Field(valueField).Name, field.Name}
			}

			if !re.Field(i).CanSet() {
				return nil, &UnexportedFieldError{field, rt}
			}
			valueField = i
		} else if tag == "remote" {
			if remoteField != -1 {
				return nil, &MultipleFieldsError{"remote", rt.Field(remoteField).Name, field.Name}
			}

			if !re.Field(i).CanSet() {
				return nil, &UnexportedFieldError{field, rt}
			}
			remoteField = i
		}
	}

	var setValueFn = func(interface{}) error { return nil }
	if valueField != -1 {
		field := re.Field(valueField)
		ft := field.Type()
		setValueFn = func(v interface{}) error {
			if ft != reflect.TypeOf(v) {
				return &ValueTypeMismatchError{ft, reflect.TypeOf(v)}
			}

			field.Set(reflect.ValueOf(v))
			return nil
		}
	}

	handler := handler{
		pendingCalls: make(map[int]*RemotePromise),
		fnHandlers:   make(map[string]func([]json.RawMessage) (interface{}, error)),
		setValue:     setValueFn,
	}

	remotePromiseType := reflect.TypeOf((*RemotePromise)(nil))
	if remoteField != -1 {
		field := re.Field(remoteField)
		ft := field.Type()
		if ft.Kind() != reflect.Struct {
			return nil, &RemoteFieldTypeError{ft}
		}

		for i := 0; i < ft.NumField(); i++ {
			remoteFnField := ft.Field(i)
			canSet := field.Field(i).CanSet()
			if remoteFnField.Type.Kind() == reflect.Func && canSet {
				fnType := remoteFnField.Type
				nOut := fnType.NumOut()
				if nOut > 1 {
					return nil, &TooManyOutputsRemoteError{remoteFnField, nOut}
				} else if nOut == 1 && !fnType.Out(0).AssignableTo(remotePromiseType) {
					return nil, &NotRemotePromiseError{remoteFnField, fnType.Out(0)}
				}
				fnName := remoteFnField.Name

				field.Field(i).Set(reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
					r := newRemotePromise(&handler, handler.r, handler.w)
					var argVals []json.RawMessage
					for _, arg := range args {
						b, err := json.Marshal(arg.Interface())
						if err != nil {
							r.reject(err)
							return []reflect.Value{reflect.ValueOf(r)}
						}

						argVals = append(argVals, b)
					}

					callID := handler.newCall(r)
					call := rpcCall{
						CallID:   callID,
						Args:     argVals,
						FuncName: fnName,
					}

					err := json.NewEncoder(r.w).Encode(call)
					if err != nil {
						delete(handler.pendingCalls, callID)
						r.reject(err)
						return []reflect.Value{reflect.ValueOf(r)}
					}

					return []reflect.Value{reflect.ValueOf(r)}
				}))
			}
		}
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	ri := reflect.TypeOf(connAPI)
	for i := 0; i < ri.NumMethod(); i++ {
		method := ri.Method(i)
		mtype := method.Type
		nOut := mtype.NumOut()
		methodValue := rv.Method(i)

		if nOut > 2 {
			return nil, &APIFuncTooManyOutputsError{method, nOut}
		} else if nOut == 2 && !mtype.Out(1).AssignableTo(errorType) { // second output must be of type error
			return nil, &APIFuncNotErrorOutputError{method, mtype.Out(1)}
		}

		nInEffective := mtype.NumIn() - 1

		fnName := method.Name
		handler.fnHandlers[fnName] = func(args []json.RawMessage) (interface{}, error) {
			if len(args) != nInEffective {
				return json.RawMessage{}, &RPCError{"internal error", errors.New("method input length does not match")}
			}

			in := make([]reflect.Value, nInEffective)
			for j := 0; j < nInEffective; j++ {
				inType := mtype.In(j + 1)
				inValue := reflect.New(inType)
				err := json.Unmarshal(args[j], inValue.Interface())
				if err != nil {
					return nil, &RPCError{"internal error", fmt.Errorf("error unmarshaling input %d: %s", j, err)}
				}
				in[j] = inValue.Elem()
			}

			out := methodValue.Call(in)
			if len(out) == 2 {
				val := out[0].Interface()
				errOut := out[1].Interface()
				if err, ok := errOut.(error); ok || errOut == nil {
					return val, err
				}
				// should never happen..
				panic(fmt.Sprintf("IMPLEMENTATION ERROR - Method %s second output must be of type error", fnName))
			} else if len(out) == 1 {
				val := out[0].Interface()
				if err, ok := val.(error); ok {
					return nil, err
				}
				return val, nil
			}
			return nil, nil
		}

	}
	return &handler, nil
}
