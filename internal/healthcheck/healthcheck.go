// Package healthcheck lets distroless images run their own Docker HEALTHCHECK (`<binary> healthcheck <url>`),
// since they ship no curl or wget.
package healthcheck

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Run probes url and exits 0 on HTTP 200, 1 otherwise.
func Run(url string) {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "status", resp.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}
