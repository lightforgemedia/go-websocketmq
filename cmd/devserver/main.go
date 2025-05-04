// cmd/devserver/main.go
package main

import (
    "log"
    "net/http"

    "github.com/lightforgemedia/go-websocketmq/pkg/broker/ps"
    "github.com/lightforgemedia/go-websocketmq/pkg/server"
)

func main() {
    b := ps.New(128)
    h := server.New(b)

    http.Handle("/ws", h)
    http.Handle("/", http.FileServer(http.Dir("static")))

    log.Println("dev server on :8000")
    log.Fatal(http.ListenAndServe(":8000", nil))
}
