# stalkerlib
A Go library for interacting with Stalker Middleware APIs, supporting channel retrieval, EPG fetching, and logo downloading.

## Installation
```bash
go get github.com/yourusername/stalkerlib



#USAGE:
package main

import (
    "github.com/ericmi/stalkerlib"
    "fmt"
)

func main() {
    client := stalkerlib.NewStalkerClient("http://example.com", "00:1A:79:18:05:75", "UTC")
    // Use client methods
}
