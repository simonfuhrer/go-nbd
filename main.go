package main

import (
	"fmt"
	"log"
	"os"

	nbdclient "github.com/simonfuhrer/go-nbd/pkg/client"
	"golang.org/x/net/proxy"
)

func main() {

	name, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}
	var dialer proxy.Dialer
	if name == "Simons-iMac.local" {
		dialer, err = proxy.SOCKS5("tcp", "127.0.0.1:10000", nil, nil)
		if err != nil {
			log.Fatalf("%v", err)
		}
	}
	fmt.Println(dialer)
	server := "host:10809"
	export := "/exportname"
	fmt.Println("started")
	client, err := nbdclient.New("tcp", server, export)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer client.Close()
	err = client.Connect()
	if err != nil {
		log.Fatalf("%v", err)
	}

	bytes, err := client.Read(6691028992, 65536)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Println("LEN", len(bytes))
	fmt.Println("done")

}
