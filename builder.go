package autorpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type serviceBuilder struct {
	service *service
	remotes []reflect.Type
}

func (sb *serviceBuilder) EachConnectionAssign(val interface{}, createFn func(Connection) interface{}) ServiceBuilder {
	valType := reflect.TypeOf(val)
	if createFn == nil {
		sb.service.specialTypes[valType] = func(Connection) interface{} {
			if valType.Kind() == reflect.Ptr {
				panic(fmt.Sprintf("special type %s of type Ptr, should be assigned during initialization or have a custom create function", valType.String()))
			}

			return reflect.New(valType).Interface()
		}
	} else {
		sb.service.specialTypes[valType] = createFn
	}
	return sb
}

func checkPromiseFnType(t reflect.Type, remoteFunc reflect.StructField) {
	if t.Kind() != reflect.Func {
		panic(fmt.Sprintf("Remote Function %s must have promise function as last argument", remoteFunc.Name))
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	nIn := t.NumIn()
	if nIn == 0 {
		panic(fmt.Sprintf("Promise function of %s must have at least one argument of type error", remoteFunc.Name))
	} else if nIn > 0 && !t.In(nIn-1).AssignableTo(errorType) {
		panic(fmt.Sprintf("Promise function of %s must have a value of type error as it's last argument", remoteFunc.Name))
	}
}

func (sb *serviceBuilder) RegisterRemoteObject(val interface{}) ServiceBuilder {
	panic("RegisterRemoteObject not implemented yet.")
}

func (sb *serviceBuilder) UseRemote(val interface{}) ServiceBuilder {
	remoteType := reflect.TypeOf(val)
	if remoteType.Kind() != reflect.Struct {
		panic("Parameter of UseRemote must be a struct")
	}

	sb.remotes = append(sb.remotes, remoteType)
	return sb
}

func (sb *serviceBuilder) buildRemotes() {
	for _, remoteType := range sb.remotes {
		remoteReflectedVal := reflect.New(remoteType).Elem()

		remoteFuncs := make([]reflect.StructField, 0)
		remoteReturnTypes := make([][]reflect.Type, 0)

		for i := 0; i < remoteType.NumField(); i++ {
			field := remoteType.Field(i)
			canSet := remoteReflectedVal.Field(i).CanSet()
			if field.Type.Kind() != reflect.Func || !canSet {
				continue
			}

			fnType := field.Type

			nIn := fnType.NumIn()
			if nIn == 0 {
				panic(fmt.Sprintf("remote Function %s must have promise function as last argument", field.Name))
			} else if nIn > 0 {
				checkPromiseFnType(fnType.In(nIn-1), field)
			}

			promiseFnType := fnType.In(nIn - 1)
			numReturns := promiseFnType.NumIn() - 1
			returnTypes := make([]reflect.Type, numReturns)
			for j := 0; j < numReturns; j++ {
				returnTypes[j] = promiseFnType.In(j)
			}

			remoteFuncs = append(remoteFuncs, field)
			remoteReturnTypes = append(remoteReturnTypes, returnTypes)
		}

		service := sb.service
		buildRemoteFn := func(conn Connection) interface{} {
			newRemotePtr := reflect.New(remoteType)
			newRemote := newRemotePtr.Elem()

			for i, remoteFnField := range remoteFuncs {
				remoteFnName := remoteFnField.Name
				newRemote.FieldByIndex(remoteFnField.Index).Set(reflect.MakeFunc(remoteFnField.Type, func(args []reflect.Value) (results []reflect.Value) {
					service.callRemoteFunc(conn, remoteFnName, args, remoteReturnTypes[i])
					return []reflect.Value{}
				}))
			}

			return newRemotePtr.Interface()
		}
		sb.service.specialTypes[remoteType] = buildRemoteFn
	}
}

func (sb *serviceBuilder) buildAPI() {
	apiPtr := sb.service.apiPtr
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	apiInterface := apiPtr.Type()
	service := sb.service
	for i := 0; i < apiInterface.NumMethod(); i++ {
		method := apiInterface.Method(i)
		mtype := method.Type
		nOut := mtype.NumOut()
		methodValue := apiPtr.Method(i)

		isLastArgError := false
		if nOut > 0 && mtype.Out(nOut-1).AssignableTo(errorType) {
			isLastArgError = true
		}

		inParams := make([]int, 0)
		specialInParams := make([]int, 0)
		specialInPtrParams := make([]int, 0)

		for j := 1; j < mtype.NumIn(); j++ {
			inType := mtype.In(j)
			if inType.Kind() != reflect.Ptr {
				inParams = append(inParams, j)
				continue
			}

			if _, ok := service.specialTypes[inType.Elem()]; ok {
				specialInParams = append(specialInParams, j)
			} else if _, ok := service.specialTypes[inType]; ok {
				specialInPtrParams = append(specialInPtrParams, j)
			} else {
				panic(fmt.Sprintf("pointer to non-special type %s in function %s", inType.String(), method.Name))
			}
		}

		fnName := method.Name
		service.fnHandlers[fnName] = func(conn Connection, args []json.RawMessage) ([]interface{}, error) {
			if len(args) != len(inParams) {
				return nil, &RPCError{"internal error", errors.New("method input length does not match")}
			}

			in := make([]reflect.Value, mtype.NumIn()-1)
			for k, j := range inParams {
				inType := mtype.In(j)
				inValue := reflect.New(inType)
				err := json.Unmarshal(args[k], inValue.Interface())
				if err != nil {
					return nil, &RPCError{"internal error", fmt.Errorf("error unmarshaling input %d: %s", k, err)}
				}
				in[j-1] = inValue.Elem()
			}

			for _, j := range specialInParams {
				inType := mtype.In(j)
				in[j-1] = reflect.ValueOf(service.getConnValue(conn, inType.Elem()))
			}

			for _, j := range specialInPtrParams {
				inType := mtype.In(j)
				in[j-1] = reflect.ValueOf(service.getConnValue(conn, inType))
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
				panic(fmt.Sprintf("last return is not of type error in function %s, got %s", fnName, out[len(out)-1].Type())) // should never happen!
			} else {
				outVals := make([]interface{}, len(out))
				for j := 0; j < len(out); j++ {
					outVals[j] = out[j].Interface()
				}
				return outVals, nil
			}
		}
	}
}

func (sb *serviceBuilder) Build() Service {
	sb.buildRemotes()
	sb.buildAPI()
	return sb.service
}

func newServiceBuilder(ptr interface{}) ServiceBuilder {
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		panic("new Service Builder must receive a pointer")
	}

	return &serviceBuilder{
		service: newService(rv),
	}
}
