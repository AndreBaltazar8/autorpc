# AutoRPC for Go
Simple RPC library that allows to expose native functions to be called remotely and also allows to call remote functions easily.

# Example

```go
type Remote struct { // Struct with the remote functions that can be called
    World func(int, func(string, error)) // the second argument here, is a callback to be called when the remote function returns the result
}

type Local struct { // Struct with local functions that will be exposed as the connection api
}

type Client struct {
    gotHello int
}

// Hello is a function that can be called remotely
func (local *Local) Hello(a int, client *Client) int { // the Client object is automatically created for this connection
    client.gotHello = a
    return a + 1 // return the value that was received plus 1
}

func connectToService() net.Conn {
    conn, err := net.Dial("tcp", "localhost:12345")
    if err != nil {
        log.Fatal(err)
    }
    return conn
}

func main() {
    conn := connectToService()

    // create the service, with the Local and Remote APIs
    service := autorpc.NewServiceBuilder(&Local{}).EachConnectionAssign(Client{}, func(conn autorpc.Connection) interface{} {
        return &Client{ // we can initialize a client here
            gotHello: -1,
        }
    }).UseRemote(Remote{})

    var remote *Remote
    waitRemote := make(chan interface{})

    go func() {
        err := service.HandleConnection(conn, func(connection autorpc.Connection) {
            val, _ := connection.GetValue(Remote{}) // get the remote api for this connection
            remote = val.(*Remote)
            close(waitRemote)
        })

        if err != nil {
            os.Exit(0)
        }
    }()

    <-waitRemote

    // Call a remote function
    remote.World(123456, func(str string, err error) {
        // check for function error
        if err != nil {
            fmt.Println("World remote function returned error:", err)
            return
        }

        // print the result
        fmt.Println("World returned:", str)
    })
}
```

# Change Log

- 2018/01/18 - Initial version
- 2018/02/02 - New API implementation

# About
The first version of the AutoRPC project was created in a single day to be used by [Nuntius](https://lab.andrebaltazar.com/?p=nuntius), an upcoming project that will allow the connection between clients, easily and securely, even behind NAT. This project is responsible by executing all the functions of the Nuntius protocol.

# License
The AutoRPC project is released under the MIT license. For the full license see LICENSE file.
