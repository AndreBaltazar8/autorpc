# AutoRPC for Go
Simple RPC library that allows to expose native functions to be called remotely and also allows to call remote functions easily.

# Example of usage

```go
type Remote struct { // Struct with the remote functions that can be called
    World func(int) *autorpc.RemotePromise
}

type Local struct { // Struct with local functions that will be exposed as the connection api
    Remote `autorpc:"remote"`
}

// Hello is a function that can be called remotely
func (local *Local) Hello(a int) int {
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
    localAPI := Local{}
    conn := connectToService()
    rpcHandler, _ := autorpc.CreateHandler(&localAPI)
    rpcHandler.SetRW(conn, conn)
    go func() {
        for {
            err := rpcHandler.Handle()
            if err != nil {
                os.Exit(0)
            }
        }
    }()

    // Call a remote function
    localAPI.Remote.World(123456).Then(func(result autorpc.RemoteResult, err error) {
        // check for function error
        if err != nil {
            fmt.Println("World remote function returned error:", err)
            return
        }

        // decode the remote result to a string
        var str string
        errDec := result.Decode(&str)
        if errDec != nil {
            fmt.Println("Could not decode result: ", err)
            return
        }

        // print the result
        fmt.Println("World returned:", str)
    })
}
```

# About
The first version of the AutoRPC project was created in a single day to be used by [Nuntius](https://lab.andrebaltazar.com/?p=nuntius), an upcoming project that will allow the connection between clients, easily and securely, even behind NAT. This project is responsible by executing all the functions of the Nuntius protocol.

# License
The AutoRPC project is released under the MIT license. For the full license see LICENSE file.
