package exporter

import (
	"flag"
	"log"
	"strings"
)

var (
	ready   = make(chan interface{}, 1)
	targets = make([]*target, 0)
	port    int
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var (
		skipDb, skipProd, skipNet bool
		nodes                     string
	)
	flag.StringVar(&nodes, "u", "http://localhost:8888", "nodes to monitor, comma seperated list of http urls")
	flag.BoolVar(&skipProd, "no-producer", false, "do not attempt to use the producer API")
	flag.BoolVar(&skipDb, "no-db", false, "do not attempt to use the db API")
	flag.BoolVar(&skipNet, "no-net", false, "do not attempt to use the net API")
	flag.IntVar(&port, "p", 13856, "port to listen on")
	flag.Parse()

	urls := strings.Split(nodes, ",")
	for _, u := range urls {
		t, e := newTarget(u)
		if e != nil {
			log.Printf("could not connect to %s: %v. Host WILL NOT be monitored", u, e)
			continue
		}
		switch false {
		case skipProd:
			t.check("producer")
			fallthrough
		case skipDb:
			t.check("db")
			fallthrough
		case skipNet:
			t.check("net")
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		flag.PrintDefaults()
		log.Fatal("could not connect to any targets.")
	}
	close(ready)
}
