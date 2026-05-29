// healthcheck binary for distroless containers.
package main

import (
	"net/http"
	"os"
)

func main() {
	resp, err := http.Get("http://localhost:8080/healthz")
	if err != nil || resp.StatusCode != 200 {
		os.Exit(1)
	}
	os.Exit(0)
}
