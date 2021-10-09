package exporter

/*
 fio-prometheus-exporter is a simple prometheus exporter for FIO nodeos nodes.
 It can connect to multiple nodes and report critical statistics.
*/

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"log"
	"net/http"
	"time"
)

// Serve starts the exporter.
func Serve() {
	<-ready
	go pollStats()
	log.Printf("serving metrics at 0.0.0.0:%d/metrics", port)
	http.Handle("/metrics", promhttp.Handler())
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

type channels struct {
	info   chan *infoUpdate
	net    chan *netUpdate
	paused chan *paused
	run    chan *runUpdate
	prod   chan *prodUpdate
	sched  chan *schedUpdate
	db     chan *dbUpdate
}

func pollStats() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := time.NewTicker(5 * time.Minute)
	check := time.NewTicker(10 * time.Second)
	chans := &channels{
		info:   make(chan *infoUpdate),
		net:    make(chan *netUpdate),
		paused: make(chan *paused),
		run:    make(chan *runUpdate),
		prod:   make(chan *prodUpdate),
		sched:  make(chan *schedUpdate),
		db:     make(chan *dbUpdate),
	}
	go processInfo(ctx, chans.info)
	go processNet(ctx, chans.net)
	go processPaused(ctx, chans.paused)
	go processRun(ctx, chans.run)
	go processProd(ctx, chans.prod)
	go processSched(ctx, chans.sched)
	go processDb(ctx, chans.db)

	for {
		select {
		case <-watchdog.C:
			for _, t := range targets {
				if t.last.Before(time.Now().Add(-5 * time.Minute)) {
					log.Printf("ERROR: watchdog has detected that %s has not updated in 5 minutes.", t.host)
					continue
				}
			}

		case <-check.C:
			for i := range targets {
				go func(i int) {
					updateHost(targets[i], chans)
				}(i)
			}
		}
	}
}

func updateHost(t *target, c *channels) {
	info, err := t.info()
	if err != nil {
		log.Println(err)
		return
	}
	c.info <- info

	net, err := t.net()
	switch err.(type) {
	case nil:
		c.net <- net
	case NotMonitoredErr: // noop
	case error:
		log.Println(err)
		return
	}

	isPaused, err := t.paused()
	switch err.(type) {
	case nil:
		c.paused <- isPaused
	case NotMonitoredErr, NullErr: // noop
	case error:
		log.Println(err)
		return
	}

	runtime, err := t.runtime()
	switch err.(type) {
	case nil:
		c.run <- runtime
	case NotMonitoredErr: // noop
	case error:
		log.Println(err)
	}

	prods, err := t.producers()
	switch err.(type) {
	case nil:
		for i := range prods {
			c.prod <- prods[i]
		}
	case NotMonitoredErr: // noop
	case error:
		log.Println(err)
	}

	sched, err := t.schedule()
	if err != nil {
		log.Println(err)
	} else {
		c.sched <- sched
	}

	db, err := t.db()
	switch err.(type) {
	case nil:
		c.db <- db
	case NotMonitoredErr: // noop
	case error:
		log.Println(err)
	}
}
