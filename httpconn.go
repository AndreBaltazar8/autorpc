package autorpc

import (
	"errors"
	"net"
	"net/http"
	"time"
)

type httpConn struct {
	w http.ResponseWriter
	r *http.Request
}

type httpRemoteAddr string

func (addr httpRemoteAddr) Network() string {
	return "tcp"
}

func (addr httpRemoteAddr) String() string {
	return string(addr)
}

func newHTTPConn(w http.ResponseWriter, r *http.Request) net.Conn {
	return &httpConn{
		w: w,
		r: r,
	}
}

func (conn *httpConn) Read(b []byte) (n int, err error) {
	return conn.r.Body.Read(b)
}

func (conn *httpConn) Write(b []byte) (n int, err error) {
	return conn.w.Write(b)
}

func (conn *httpConn) Close() error {
	return conn.r.Body.Close()
}

func (conn *httpConn) LocalAddr() net.Addr {
	return nil
}

func (conn *httpConn) RemoteAddr() net.Addr {
	return httpRemoteAddr(conn.r.RemoteAddr)
}

func (conn *httpConn) SetDeadline(t time.Time) error {
	return errors.New("cannot set deadlines on this connection")
}

func (conn *httpConn) SetReadDeadline(t time.Time) error {
	return errors.New("cannot set deadlines on this connection")
}

func (conn *httpConn) SetWriteDeadline(t time.Time) error {
	return errors.New("cannot set deadlines on this connection")
}
