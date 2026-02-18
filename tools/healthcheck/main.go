// Minimal HTTP healthcheck binary for use in distroless containers.
// Usage: healthcheck <url>
// Exits 0 if the URL returns 2xx, 1 otherwise.
package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: healthcheck <url>")
		os.Exit(1)
	}
	resp, err := http.Get(os.Args[1]) //nolint:gosec,noctx
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "unhealthy: %s\n", resp.Status)
		os.Exit(1)
	}
}
