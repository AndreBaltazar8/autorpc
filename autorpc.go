package autorpc

import (
	"net"
	"net/http"
)

type Connection interface {
	GetRawConnection() net.Conn
	GetValue(val interface{}) (interface{}, error)
	AssignValue(val interface{}) error
	StopHandling()
}

type Service interface {
	http.Handler
	// HandleConnection handles the lifetime of a connection
	HandleConnection(conn net.Conn, initFn func(Connection)) error
}

type ServiceBuilder interface {
	EachConnectionAssign(val interface{}, createFn func(Connection) interface{}) ServiceBuilder
	UseRemote(val interface{}) ServiceBuilder
	Build() Service
}

func NewServiceBuilder(ptr interface{}) ServiceBuilder {
	return newServiceBuilder(ptr)
}

type Object interface {
	GetValue() interface{}
}

func NewObject(ptr interface{}) Object {
	return nil
}

// IgnoreReturn is a promise to ignore the return
var IgnoreReturn = func(error) {}
