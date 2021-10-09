package exporter

import (
	"encoding/json"
	"github.com/fioprotocol/fio-go"
	"io"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NotMonitoredErr will be returned if an endpoint is not available, or has been explicitly excluded.
type NotMonitoredErr struct {
	Api string
}

func (nm NotMonitoredErr) Error() string {
	return nm.Api + " endpoint is not monitored"
}

// NullErr will be returned when a response is empty, primarily when getting information about whether a node
// has block production enabled.
type NullErr struct{}

func (ne NullErr) Error() string {
	return ""
}

type target struct {
	hasNet      bool
	hasProducer bool
	hasDb       bool
	last        time.Time

	host string
	api  *fio.API
}

func newTarget(u string) (*target, error) {
	api, _, err := fio.NewConnection(nil, u)
	if err != nil {
		return nil, err
	}
	h, _ := url.Parse(u)
	return &target{
		host: h.Hostname(),
		api:  api,
	}, nil
}

func (t *target) check(endpoint string) {
	var err error
	switch endpoint {
	case "producer":
		_, err = t.api.IsProducerPaused()
		if err != nil {
			log.Printf("not checking %s on %s: %v", endpoint, t.api.BaseURL, err)
			return
		}
		t.hasProducer = true
	case "net":
		_, err = t.api.GetNetConnections()
		if err != nil {
			log.Printf("not checking %s on %s: %v", endpoint, t.api.BaseURL, err)
			return
		}
		t.hasNet = true
	case "db":
		_, err = t.api.GetDBSize()
		if err != nil {
			log.Printf("not checking %s on %s: %v", endpoint, t.api.BaseURL, err)
			return
		}
		t.hasDb = true
	default:
		log.Printf("requested unknown api %s for %s", endpoint, t.api.BaseURL)
	}
}

func (t *target) info() (*infoUpdate, error) {
	i, e := t.api.GetInfo()
	if e != nil {
		return nil, e
	}
	var chain string
	switch i.ChainID.String() {
	case fio.ChainIdMainnet:
		chain = "mainnet"
	case fio.ChainIdTestnet:
		chain = "testnet"
	default:
		chain = i.ChainID.String()
	}
	t.last = time.Now().UTC() // watchdog timer
	return &infoUpdate{
		Head:     float64(i.HeadBlockNum),
		Lib:      float64(i.LastIrreversibleBlockNum),
		Delta:    float64(time.Now().UTC().Unix() - i.HeadBlockTime.Unix()),
		ChainId:  chain,
		Endpoint: t.host,
		Version:  i.ServerVersionString,
		Url:      t.api.BaseURL,
	}, e
}

func (t *target) net() (*netUpdate, error) {
	if !t.hasNet {
		return nil, NotMonitoredErr{Api: "net"}
	}
	n, e := t.api.GetNetConnections()
	if e != nil {
		return nil, e
	}
	var connected, disconnected, syncing float64
	for _, c := range n {
		if c.Connecting {
			disconnected += 1
		} else {
			connected += 1
		}
		if c.Syncing {
			syncing += 1
		}
	}
	return &netUpdate{
		Connected:    connected,
		Disconnected: disconnected,
		Syncing:      syncing,
		Endpoint:     t.host,
		Url:          t.api.BaseURL,
	}, e
}

type groResp struct {
	Mtt float64 `json:"max_transaction_time"`
}

func (t *target) runtime() (*runUpdate, error) {
	if !t.hasProducer {
		return nil, NotMonitoredErr{Api: "producer"}
	}
	resp, err := t.api.HttpClient.Post(t.api.BaseURL+"/v1/producer/get_runtime_options", "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	mtt := &groResp{}
	err = json.Unmarshal(body, mtt)
	if err != nil {
		return nil, err
	}
	return &runUpdate{
		MaxTx:    mtt.Mtt,
		Endpoint: t.host,
		Url:      t.api.BaseURL,
	}, err
}

func (t *target) producers() ([]*prodUpdate, error) {
	p, e := t.api.GetFioProducers()
	if e != nil {
		return nil, e
	}
	prodCount := len(p.Producers)
	// only top 42 to keep noise down.
	if prodCount > 42 {
		prodCount = 42
	}
	update := make([]*prodUpdate, prodCount)
	for i := range p.Producers {
		lct, err := time.Parse("2006-01-02T15:04:05.999", p.Producers[i].LastClaimTime)
		if err != nil {
			return nil, err
		}
		votes, err := strconv.Atoi(strings.Split(p.Producers[i].TotalVotes, ".")[0])
		if err != nil {
			return nil, err
		}
		var isTop float64
		if i < 21 && p.Producers[i].IsActive > 0 {
			isTop = 1
		}
		update[i] = &prodUpdate{
			Active:   float64(p.Producers[i].IsActive),
			Top:      isTop,
			Claim:    float64(time.Now().UTC().Unix() - lct.UTC().Unix()),
			Rank:     float64(i + 1),
			Votes:    float64(votes / 1_000_000_000),
			Producer: string(p.Producers[i].FioAddress),
			Endpoint: t.host,
			Url:      t.api.BaseURL,
		}
	}
	return update, nil
}

func (t *target) paused() (*paused, error) {
	if !t.hasProducer {
		return nil, NotMonitoredErr{Api: "producer"}
	}
	p, e := t.api.IsProducerPaused()
	if e != nil {
		return nil, e
	}
	if p {
		return nil, NullErr{}
	}
	return &paused{
		Active:   1,
		Endpoint: t.host,
		Url:      t.api.BaseURL,
	}, e
}

func (t *target) schedule() (*schedUpdate, error) {
	s, e := t.api.GetProducerSchedule()
	if e != nil {
		return nil, e
	}
	return &schedUpdate{
		Active:   float64(s.Active.Version),
		Pending:  float64(s.Pending.Version),
		Proposed: float64(s.Proposed.Version),
		Endpoint: t.host,
		Url:      t.api.BaseURL,
	}, e
}

func (t *target) db() (*dbUpdate, error) {
	if !t.hasDb {
		return nil, NotMonitoredErr{Api: "db"}
	}
	d, e := t.api.GetDBSize()
	if e != nil {
		return nil, e
	}
	return &dbUpdate{
		Free:     float64(d.FreeBytes),
		Used:     float64(d.UsedBytes),
		Endpoint: t.host,
		Url:      t.api.BaseURL,
	}, nil
}
