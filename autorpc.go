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

// A RemotePromise holds the promise to be executed when the result of a RPC call is available.
type RemotePromise struct {
	handler     *handler
	r           io.Reader
	w           io.Writer
	promiseFunc reflect.Value
	returnTypes []reflect.Type
}

func newRemotePromise(handler *handler, r io.Reader, w io.Writer, returnTypes []reflect.Type, promiseFunc reflect.Value) *RemotePromise {
	return &RemotePromise{
		handler:     handler,
		r:           r,
		w:           w,
		returnTypes: returnTypes,
		promiseFunc: promiseFunc,
	}
}

func (promise *RemotePromise) resolve(data []json.RawMessage) error {
	promise.handler.r = promise.r
	promise.handler.w = promise.w

	numReturns := len(promise.returnTypes)
	if len(data) != numReturns {
		return &RPCError{Err: "got wrong num of returns"}
	}

	returns := make([]reflect.Value, numReturns+1)
	for j, returnType := range promise.returnTypes {
		returnValue := reflect.New(returnType)
		err := json.Unmarshal(data[j], returnValue.Interface())
		if err != nil {
			return &RPCError{Err: fmt.Sprintf("error unmarshaling input %d: %s", j, err)}
		}
		returns[j] = returnValue.Elem()
	}
	returns[numReturns] = reflect.New(reflect.TypeOf((*error)(nil)).Elem()).Elem()
	promise.promiseFunc.Call(returns)
	return nil
}

func (promise *RemotePromise) reject(err error) {
	numReturns := len(promise.returnTypes)
	returns := make([]reflect.Value, numReturns+1)
	for j, returnType := range promise.returnTypes {
		returnValue := reflect.New(returnType)
		returns[j] = returnValue.Elem()
	}
	returns[numReturns] = reflect.ValueOf(err)
	promise.promiseFunc.Call(returns)
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
	fnHandlers   map[string]func([]json.RawMessage) ([]interface{}, error)
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
	CallID int           `json:"c"`
	Error  string        `json:"e,omitempty"`
	Data   []interface{} `json:"d,omitempty"`
}

type rpcMessage struct {
	CallID   int               `json:"c"`
	FuncName string            `json:"f,omitempty"`
	Args     []json.RawMessage `json:"a,omitempty"`
	Error    string            `json:"e,omitempty"`
	Data     []json.RawMessage `json:"d,omitempty"`
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
		if err != nil { // prioritize function error
			return err
		} else if writeErr != nil {
			return &RPCError{ActualErr: writeErr}
		}
	} else if msg.isResponse() {
		promise, exists := handler.pendingCalls[msg.CallID]
		if exists {
			delete(handler.pendingCalls, msg.CallID)
			if msg.Error != "" {
				promise.reject(errors.New(msg.Error))
			} else {
				err := promise.resolve(msg.Data)
				if err != nil {
					return err
				}
			}
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

func isPromiseFnType(t reflect.Type, remoteFunc reflect.StructField) error {
	if t.Kind() != reflect.Func {
		return &RemoteFuncError{"must have promise function as last argument", remoteFunc}
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	nIn := t.NumIn()
	if nIn == 0 {
		return &RemoteFuncError{"promise function must at least one argument of type error", remoteFunc}
	} else if nIn > 0 && !t.In(nIn-1).AssignableTo(errorType) {
		return &RemoteFuncError{"promise function must have the last argument of type error", remoteFunc}
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
		fnHandlers:   make(map[string]func([]json.RawMessage) ([]interface{}, error)),
		setValue:     setValueFn,
	}

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

				nIn := fnType.NumIn()
				if nIn == 0 {
					return nil, &RemoteFuncError{"must have a promise function as last argument", remoteFnField}
				} else if nIn > 0 {
					err := isPromiseFnType(fnType.In(nIn-1), remoteFnField)
					if err != nil {
						return nil, err
					}
				}

				promiseFnType := fnType.In(nIn - 1)
				numReturns := promiseFnType.NumIn() - 1
				returnTypes := make([]reflect.Type, numReturns)
				for j := 0; j < numReturns; j++ {
					returnTypes[j] = promiseFnType.In(j)
				}

				fnName := remoteFnField.Name

				field.Field(i).Set(reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
					nArgs := len(args) - 1
					r := newRemotePromise(&handler, handler.r, handler.w, returnTypes, args[nArgs])
					var argVals []json.RawMessage
					for j := 0; j < nArgs; j++ {
						b, err := json.Marshal(args[j].Interface())
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

					return []reflect.Value{}
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

		isLastArgError := false
		if nOut > 0 && mtype.Out(nOut-1).AssignableTo(errorType) {
			isLastArgError = true
		}

		nInEffective := mtype.NumIn() - 1

		fnName := method.Name
		handler.fnHandlers[fnName] = func(args []json.RawMessage) ([]interface{}, error) {
			if len(args) != nInEffective {
				return nil, &RPCError{"internal error", errors.New("method input length does not match")}
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

			if isLastArgError {
				outVals := make([]interface{}, len(out)-1)
				for j := 0; j < len(out)-1; j++ {
					outVals[j] = out[j].Interface()
				}

				errOut := out[len(out)-1].Interface()
				if err, ok := errOut.(error); ok || errOut == nil {
					return outVals, err
				}
				panic(fmt.Sprintf("last return is not of type error in function %s, got %s", fnName, out[len(out)-1].Type()))
			} else {
				outVals := make([]interface{}, len(out))
				for j := 0; j < len(out); j++ {
					outVals[j] = out[j].Interface()
				}
				return outVals, nil
			}
		}

	}
	return &handler, nil
}
