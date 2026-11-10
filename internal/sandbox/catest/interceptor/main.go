// Command interceptor is catest's intercepting proxy as a program, for tests
// that need it inside a container.
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/catest"
)

func main() {
	listen := flag.String("listen", ":3129", "the address to listen on")
	certFile := flag.String("cert", "", "the root's certificate")
	keyFile := flag.String("key", "", "the root's key")
	flag.Parse()
	certPEM, err := os.ReadFile(*certFile)
	if err != nil {
		log.Fatal(err)
	}
	keyPEM, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	ca, err := catest.LoadCA(certPEM, keyPEM)
	if err != nil {
		log.Fatal(err)
	}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(ca.Intercept(l))
}
