package autorpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type rpcCall struct {
	CallID   string            `json:"c"`
	Args     []json.RawMessage `json:"a"`
	FuncName string            `json:"f"`
}

type rpcCallReturn struct {
	CallID string        `json:"c"`
	Error  string        `json:"e,omitempty"`
	Data   []interface{} `json:"d,omitempty"`
}

type rpcMessage struct {
	CallID   string            `json:"c"`
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

type remotePromise struct {
	conn        Connection
	returnTypes []reflect.Type
	promiseFunc reflect.Value
}

func (promise *remotePromise) resolve(data []json.RawMessage) error {
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

func (promise *remotePromise) reject(err error) {
	numReturns := len(promise.returnTypes)
	returns := make([]reflect.Value, numReturns+1)
	for j, returnType := range promise.returnTypes {
		returnValue := reflect.New(returnType)
		returns[j] = returnValue.Elem()
	}
	returns[numReturns] = reflect.ValueOf(err)
	promise.promiseFunc.Call(returns)
}

type connection struct {
	service  *service
	conn     net.Conn
	handling bool
}

func (conn *connection) GetRawConnection() net.Conn {
	return conn.conn
}

func (conn *connection) GetValue(typeval interface{}) (interface{}, error) {
	t := reflect.TypeOf(typeval)
	service := conn.service
	if _, ok := service.specialTypes[t]; !ok {
		return nil, fmt.Errorf("type %s not found has special type for service", t.String())
	}

	return service.getConnValue(conn, t), nil
}

func (conn *connection) AssignValue(val interface{}) error {
	t := reflect.TypeOf(val)
	service := conn.service
	if _, ok := service.specialTypes[t]; !ok {
		return fmt.Errorf("type %s not found has special type for service", t.String())
	}

	if values, ok := service.connValues.Load(conn); ok {
		values := values.(*sync.Map)
		values.Store(t, val)
		return nil
	}

	values := &sync.Map{}
	values.Store(t, val)
	service.connValues.Store(conn, values)
	return nil
}

func (conn *connection) StopHandling() {
	conn.handling = false
}

type service struct {
	wsUpgrader   websocket.Upgrader
	apiPtr       reflect.Value
	specialTypes map[reflect.Type]func(Connection) interface{}
	fnHandlers   map[string]func(conn Connection, args []json.RawMessage) ([]interface{}, error)
	connValues   sync.Map
	connDecoders sync.Map
	pendingCalls sync.Map
}

func (service *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn := newHTTPConn(w, r)
	sConn := &connection{
		service:  service,
		conn:     conn,
		handling: true,
	}

	service.handle(conn, sConn)
}

// HandleConnection handles the lifetime of a connection
func (service *service) HandleConnection(conn net.Conn, initFn func(Connection)) error {
	sConn := &connection{
		service:  service,
		conn:     conn,
		handling: true,
	}

	if initFn != nil {
		initFn(sConn)
	}

	service.initializeConnection(sConn)
	for sConn.handling {
		err := service.handle(conn, sConn)
		if err != nil {
			service.finalizeConnection(sConn)
			return err
		}
	}

	return nil
}

func (service *service) initializeConnection(conn Connection) {
}

func (service *service) finalizeConnection(conn Connection) {
	service.connValues.Delete(conn)
	service.connDecoders.Delete(conn)
}

func (service *service) newCall(promise *remotePromise) string {
	var callID int
	for {
		callID = rand.Int()
		if _, exists := service.pendingCalls.LoadOrStore(strconv.Itoa(callID), promise); !exists {
			return strconv.Itoa(callID)
		}
	}
}

func (service *service) callRemoteFunc(conn Connection, fn string, args []reflect.Value, returnTypes []reflect.Type) {
	nArgs := len(args) - 1
	promise := &remotePromise{
		conn:        conn,
		returnTypes: returnTypes,
		promiseFunc: args[nArgs],
	}

	var argVals []json.RawMessage
	for j := 0; j < nArgs; j++ {
		b, err := json.Marshal(args[j].Interface())
		if err != nil {
			promise.reject(err)
			return
		}

		argVals = append(argVals, b)
	}

	callID := service.newCall(promise)
	call := rpcCall{
		CallID:   callID,
		Args:     argVals,
		FuncName: fn,
	}

	err := json.NewEncoder(conn.GetRawConnection()).Encode(call)
	if err != nil {
		service.pendingCalls.Delete(callID)
		promise.reject(err)
		return
	}
}

func (service *service) getConnValue(conn Connection, t reflect.Type) interface{} {
	fn := service.specialTypes[t]
	if values, ok := service.connValues.Load(conn); ok {
		values := values.(*sync.Map)
		if value, ok := values.Load(t); ok {
			return value
		}

		newVal := fn(conn)
		values.Store(t, newVal)
		return newVal
	}

	values := &sync.Map{}
	newVal := fn(conn)
	values.Store(t, newVal)
	service.connValues.Store(conn, values)
	return newVal
}

func (service *service) send(conn net.Conn, v interface{}) error {
	return json.NewEncoder(conn).Encode(v)
}

func (service *service) getDecoder(conn net.Conn) *json.Decoder {
	if v, ok := service.connDecoders.Load(conn); ok {
		return v.(*json.Decoder)
	}

	decoder := json.NewDecoder(conn)
	service.connDecoders.Store(conn, decoder)
	return decoder
}

func (service *service) getPendingCall(callID string) (*remotePromise, bool) {
	val, ok := service.pendingCalls.Load(callID)
	if !ok {
		return nil, false
	}
	if v, ok := val.(*remotePromise); ok {
		return v, true
	}
	panic("stored pending call has wrong type")
}

func (service *service) handle(conn net.Conn, connection Connection) error {
	decoder := service.getDecoder(conn)

	msg := rpcMessage{}
	err := decoder.Decode(&msg)
	if err != nil {
		if err == io.EOF {
			return err
		}

		service.send(conn, &rpcCallReturn{CallID: msg.CallID, Error: "autorpc: internal error"})
		return err
	}

	if msg.isCall() {
		fn, ok := service.fnHandlers[msg.FuncName]
		if !ok {
			err := &RPCError{Err: "function not found"}
			service.send(conn, &rpcCallReturn{CallID: msg.CallID, Error: err.Error()})
			return err
		}

		result, err := fn(connection, msg.Args)
		errString := ""
		if err != nil {
			errString = err.Error()
			result = nil
		}

		writeErr := service.send(conn, &rpcCallReturn{CallID: msg.CallID, Error: errString, Data: result})
		if err != nil { // prioritize function error
			return err
		} else if writeErr != nil {
			return &RPCError{ActualErr: writeErr}
		}
	} else if msg.isResponse() {
		promise, exists := service.getPendingCall(msg.CallID)
		if exists {
			service.pendingCalls.Delete(msg.CallID)
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
		promise, exists := service.getPendingCall(msg.CallID)
		if exists {
			promise.reject(errors.New(msg.Error))
			service.pendingCalls.Delete(msg.CallID)
		}
		return &RPCError{Err: strings.TrimPrefix(msg.Error, "autorpc: ")}
	}

	return nil
}

func newService(apiPtr reflect.Value) *service {
	if apiPtr.Kind() != reflect.Ptr || apiPtr.IsNil() {
		panic("new Service must receive a pointer")
	}

	return &service{
		apiPtr:       apiPtr,
		specialTypes: make(map[reflect.Type]func(Connection) interface{}),
		fnHandlers:   make(map[string]func(conn Connection, args []json.RawMessage) ([]interface{}, error)),
	}
}
