package main

import "net/http"
import "log"

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        http.ServeFile(w, r, "index.html") 
    })

    srv := &http.Server{
        Addr: ":8036", 
        Handler: mux, 
    }
    log.Fatal(srv.ListenAndServe())
}
