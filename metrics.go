package exporter

import (
	"context"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"log"
	"sync"
	"time"
)

type endpointInfo struct {
	ChainId string
	Version string

	updated time.Time
}

type endpoints struct {
	mux       sync.RWMutex
	endpoints map[string]*endpointInfo
}

func (ep *endpoints) get(s string) *endpointInfo {
	ep.mux.RLock()
	defer ep.mux.RUnlock()
	return ep.endpoints[s]
}

func (ep *endpoints) set(name string, info endpointInfo) {
	ep.mux.Lock()
	defer ep.mux.Unlock()
	info.updated = time.Now().UTC()
	ep.endpoints[name] = &info
}

func (ep *endpoints) scrub() {
	ep.mux.Lock()
	defer ep.mux.Unlock()
	for k, v := range ep.endpoints {
		if v.updated.Before(time.Now().UTC().Add(-10*time.Minute)) {
			delete(ep.endpoints, k)
		}
	}
}

var (
	ep = endpoints{
		endpoints: make(map[string]*endpointInfo),
	}

	// labels
	stdLbl  = []string{"chain_id", "version", "endpoint"}
	prodLbl = []string{"chain_id", "version", "endpoint", "producer"}

	// chain/get_info
	head = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_info_head_block",
		Help: "head block number from get_info endpoint",
	}, stdLbl)
	lib = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_info_lib",
		Help: "last irreversible block number from get_info endpoint",
	}, stdLbl)
	lag = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_info_head_lag",
		Help: "seconds behind the head block",
	}, stdLbl)

	// net/connections
	netGood = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_net_connected_peers",
		Help: "number of connected peers",
	}, stdLbl)
	netBad = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_net_disconnected_peers",
		Help: "number of unreachable peers",
	}, stdLbl)
	netSyncing = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_net_syncing_peers",
		Help: "number of peers catching up",
	}, stdLbl)

	// producer/paused
	prodPaused = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_producing",
		Help: "if producer api is paused: 1 means actively signing blocks, otherwise null",
	}, stdLbl)

	// producer/get_runtime_options
	maxTxTime = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_runtime_max_transaction_time",
		Help: "maximum transaction time (http_plugin)",
	}, stdLbl)

	// chain/get_producers
	prodActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_is_active",
		Help: "whether producer is set to active",
	}, prodLbl)
	prodTop = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_is_top21",
		Help: "whether producer is in the top 21: 0 if false",
	}, prodLbl)
	prodClaimSec = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_last_bpclaim_delta",
		Help: "seconds since last bpclaim",
	}, prodLbl)
	prodRank = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_rank",
		Help: "producer rank",
	}, prodLbl)
	prodVotes = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_votes",
		Help: "producer votes in FIO",
	}, prodLbl)

	// chain/get_producer_schedule
	schedActive = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_schedule_active",
		Help: "active producer schedule",
	}, stdLbl)
	schedPending = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_schedule_pending",
		Help: "pending producer schedule",
	}, stdLbl)
	schedProposed = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_producer_schedule_proposed",
		Help: "proposed producer schedule",
	}, stdLbl)

	// db_size/get
	dbFree = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_db_free_bytes",
		Help: "bytes remaining for memory state",
	}, stdLbl)
	dbUsed = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "fio_db_used_bytes",
		Help: "bytes used by memory state",
	}, stdLbl)
)

type infoUpdate struct {
	Head  float64
	Lib   float64
	Delta float64

	ChainId  string
	Endpoint string
	Version  string
	Url      string
}

func processInfo(ctx context.Context, updates chan *infoUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processInfo routine exiting")
			return

		case u := <-updates:
			if u == nil {
				continue
			}
			l := map[string]string{
				"chain_id": u.ChainId,
				"endpoint": u.Endpoint,
				"version":  u.Version,
			}
			ep.set(u.Url, endpointInfo{
				ChainId: u.ChainId,
				Version: u.Version,
				updated: time.Now().UTC(),
			})

			head.With(l).Set(u.Head)
			lib.With(l).Set(u.Lib)
			lag.With(l).Set(u.Delta)
		}
	}
}

type netUpdate struct {
	Connected    float64
	Disconnected float64
	Syncing      float64

	Endpoint string
	Url      string
}

func processNet(ctx context.Context, updates chan *netUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processNet routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for net update:", u.Url)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
			}
			netGood.With(l).Set(u.Connected)
			netBad.With(l).Set(u.Disconnected)
			netSyncing.With(l).Set(u.Syncing)
		}
	}
}

type paused struct {
	Active float64

	Endpoint string
	Url      string
}

func processPaused(ctx context.Context, updates chan *paused) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processRun routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for runtime update:", u.Url)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
			}
			prodPaused.With(l).Set(u.Active)
		}
	}
}

type runUpdate struct {
	MaxTx float64

	Endpoint string
	Url      string
}

func processRun(ctx context.Context, updates chan *runUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processRun routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for runtime update:", u.Url)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
			}
			maxTxTime.With(l).Set(u.MaxTx)
		}
	}
}

type prodUpdate struct {
	Active float64
	Claim  float64
	Rank   float64
	Votes  float64
	Top    float64

	Producer string
	Endpoint string
	Url      string
}

func processProd(ctx context.Context, updates chan *prodUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processProd routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for producer update:", u.Endpoint)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
				"producer": u.Producer,
			}
			prodActive.With(l).Set(u.Active)
			prodTop.With(l).Set(u.Top)
			prodClaimSec.With(l).Set(u.Claim)
			prodRank.With(l).Set(u.Rank)
			prodVotes.With(l).Set(u.Votes)
		}
	}
}

type schedUpdate struct {
	Active   float64
	Pending  float64
	Proposed float64

	Endpoint string
	Url      string
}

func processSched(ctx context.Context, updates chan *schedUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processSched routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for schedule update:", u.Url)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
			}
			schedActive.With(l).Set(u.Active)
			schedPending.With(l).Set(u.Pending)
			schedProposed.With(l).Set(u.Proposed)
		}
	}
}

type dbUpdate struct {
	Free float64
	Used float64

	Endpoint string
	Url      string
}

func processDb(ctx context.Context, updates chan *dbUpdate) {
	for {
		select {
		case <-ctx.Done():
			log.Println("processRun routine exiting")

		case u := <-updates:
			if u == nil {
				continue
			}
			h := ep.get(u.Url)
			if h == nil {
				log.Println("could not get host info for runtime update:", u.Url)
				continue
			}
			l := map[string]string{
				"chain_id": h.ChainId,
				"version":  h.Version,
				"endpoint": u.Endpoint,
			}
			dbFree.With(l).Set(u.Free)
			dbUsed.With(l).Set(u.Used)
		}
	}
}
