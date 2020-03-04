package river

import (
	"bytes"
	"context"
	"encoding/json"
	"expvar"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/errors"
	"github.com/sandeepone/mysql-manticore/util"
	"github.com/siddontang/go-mysql/mysql"
	"github.com/siddontang/go/sync2"
	"gopkg.in/birkirb/loggers.v1"
)

var (
	healthy int32
)

type stat struct {
	r *River

	l net.Listener

	m sync.Mutex

	srv http.Server

	log loggers.Advanced

	startedAt    time.Time
	lastRowEvent time.Time

	ProcessedRowEvents  sync2.AtomicUint64
	ProcessedDocEvents  sync2.AtomicUint64
	ProcessedBatches    sync2.AtomicUint64
	ReplaceAffectedRows sync2.AtomicUint64
	ReplaceQueries      sync2.AtomicUint64
	ReplaceQueryDocs    sync2.AtomicUint64
	UpdateAffectedRows  sync2.AtomicUint64
	UpdateQueries       sync2.AtomicUint64
	DeleteAffectedRows  sync2.AtomicUint64
	DeleteQueries       sync2.AtomicUint64
	DeleteQueryDocs     sync2.AtomicUint64
}

type statusInfo struct {
	Uptime               string
	UptimeNano           int64
	Lag                  string
	LagNano              int64
	UpstreamPosition     string
	UpstreamPositionInfo mysql.Position
	ReadPosition         string
	ReadPositionInfo     mysql.Position
	UpstreamGTID         string
	UpstreamGTIDInfo     mysql.GTIDSet
	ReadGTID             string
	ReadGTIDInfo         mysql.GTIDSet
	ProcessedRowEvents   uint64
	ProcessedDocEvents   uint64
	ProcessedBatches     uint64
	ReplaceAffectedRows  uint64
	ReplaceQueries       uint64
	ReplaceQueryDocs     uint64
	UpdateAffectedRows   uint64
	UpdateQueries        uint64
	DeleteAffectedRows   uint64
	DeleteQueries        uint64
	DeleteQueryDocs      uint64
	Running              bool
	Syncing              bool
}

var getStatusInfo func() interface{}

func init() {
	expvar.Publish("river", expvar.Func(func() interface{} {
		if getStatusInfo == nil {
			return nil
		}
		return getStatusInfo()
	}))
}

func (s *stat) newStatusInfo() (*statusInfo, error) {
	upstreamPos, err := s.r.canal.GetMasterPos()
	if err != nil {
		return nil, errors.Trace(err)
	}
	upstreamGTID, err := s.r.canal.GetMasterGTIDSet()
	if err != nil {
		return nil, errors.Trace(err)
	}

	now := time.Now()
	uptime := now.Sub(s.startedAt)
	lag := now.Sub(s.lastRowEvent)
	readPos := s.r.master.position()
	readGTID := s.r.master.gtidSet()

	return &statusInfo{
		Uptime:               uptime.String(),
		UptimeNano:           uptime.Nanoseconds(),
		Lag:                  lag.String(),
		LagNano:              lag.Nanoseconds(),
		UpstreamPosition:     fmt.Sprintf("%s", upstreamPos),
		UpstreamPositionInfo: upstreamPos,
		ReadPosition:         fmt.Sprintf("%s", readPos),
		ReadPositionInfo:     readPos,
		UpstreamGTID:         fmt.Sprintf("%s", upstreamGTID),
		UpstreamGTIDInfo:     upstreamGTID,
		ReadGTID:             fmt.Sprintf("%s", readGTID),
		ReadGTIDInfo:         readGTID,
		ProcessedRowEvents:   s.ProcessedRowEvents.Get(),
		ProcessedDocEvents:   s.ProcessedDocEvents.Get(),
		ProcessedBatches:     s.ProcessedBatches.Get(),
		ReplaceAffectedRows:  s.ReplaceAffectedRows.Get(),
		ReplaceQueries:       s.ReplaceQueries.Get(),
		ReplaceQueryDocs:     s.ReplaceQueryDocs.Get(),
		UpdateAffectedRows:   s.UpdateAffectedRows.Get(),
		UpdateQueries:        s.UpdateQueries.Get(),
		DeleteAffectedRows:   s.DeleteAffectedRows.Get(),
		DeleteQueries:        s.DeleteQueries.Get(),
		DeleteQueryDocs:      s.DeleteQueryDocs.Get(),
		// RebuildInProgress:    s.r.RebuildInProgress(),
		// RebuildLog:           s.RebuildLog,
		Running: s.r.IsRunning(),
		Syncing: s.r.IsSyncing(),
	}, nil
}

func (s *stat) getStatusInfo() interface{} {
	status, err := s.newStatusInfo()
	if err != nil {
		return map[string]string{}
	}
	return status
}

func (s *stat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)

	status, err := s.newStatusInfo()
	if err == nil {
		err = encoder.Encode(status)
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(errors.ErrorStack(err)))
		return
	}

	w.Header().Add("content-type", "application/json")
	w.Write(buf.Bytes())
}

func (s *stat) Serve() {
	if err := s.run(); err != nil && err != http.ErrServerClosed {
		s.r.FatalErrC <- err
	}
}

func (s *stat) run() (err error) {
	s.log = s.r.Log.WithFields("service", s.String())
	addr := s.r.c.StatAddr
	if len(addr) == 0 {
		return
	}
	s.l, err = net.Listen("tcp", addr)
	if err != nil {
		s.log.Errorf("error creating listener at addr %s: %v", addr, err)
		return
	}
	s.log.Infof("Started status http server at %s", addr)

	s.lastRowEvent = time.Now()
	s.startedAt = time.Now()

	mux := http.NewServeMux()

	// kubernetes probes
	mux.Handle("/healthz", handleHealthz(s.r))
	mux.Handle("/readyz", handleReadyz(s))

	// endpoints
	mux.Handle("/stats", s)
	mux.Handle("/maint", handleMaint(s.r))
	mux.Handle("/wait", handleWaitForGTID(s.r))

	// syncing - start/stop
	mux.Handle("/syncing/start", handleStartSync(s.r))
	mux.Handle("/syncing/stop", handleStopSync(s.r))

	// profiling
	mux.Handle("/debug/vars", expvar.Handler())
	mux.Handle("/debug/pprof/", http.HandlerFunc(pprof.Index))
	mux.Handle("/debug/pprof/cmdline", http.HandlerFunc(pprof.Cmdline))
	mux.Handle("/debug/pprof/profile", http.HandlerFunc(pprof.Profile))
	mux.Handle("/debug/pprof/symbol", http.HandlerFunc(pprof.Symbol))
	mux.Handle("/debug/pprof/trace", http.HandlerFunc(pprof.Trace))

	stdLogger, logWriter := util.NewStdLogger(s.r.Log)
	defer logWriter.Close()
	s.srv = http.Server{Handler: mux, ErrorLog: stdLogger}

	getStatusInfo = s.getStatusInfo

	// signal Kubernetes the server is healthy
	atomic.StoreInt32(&healthy, 1)

	return s.srv.Serve(s.l)
}

func (s *stat) Stop() {
	if s.l != nil {
		s.log.Infof("shutting down status http server")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s.srv.Shutdown(ctx)
		s.l.Close()
		cancel()
	}
}

func (s *stat) String() string {
	return "StatService"
}

// Kubernetes liveness probe - healthy
func handleHealthz(r *River) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if atomic.LoadInt32(&healthy) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
	})
}

// Kubernetes readiness probe - ready
func handleReadyz(s *stat) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d := time.Now().Sub(s.startedAt)

		// allows to take leadership or rolling update.
		// wait for 1 min and inform kubernetes if unsuccessful
		if !s.r.isRunning && d.Seconds() < 45 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if s.r.isRunning && atomic.LoadInt32(&healthy) == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusServiceUnavailable)
	})
}

func handleStartSync(r *River) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.m.Lock()
		defer r.m.Unlock()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("unexpected method\n"))
			return
		}

		if !r.isRunning {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("river not running\n"))
			return
		}

		if r.IsSyncing() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("sync already running\n"))
			return
		}

		// reconnect manticore client - always make new connection
		if r.sphinxToken == nil {
			r.sphinxService.RequestStartNotification()

			t := r.sup.Add(r.sphinxService)
			r.sphinxToken = &t

			r.sphinxService.WaitUntilStarted()
		}

		err := r.sphinxService.LoadSyncState(r.master.syncState())
		if err != nil {
			r.l.Errorf("Status: failed to reset GTID after successful restart: %s", errors.ErrorStack(err))
			w.Write([]byte("failed to reset GTID\n"))
			return
		}

		r.l.Infof("reset GTID after successful restart to: %s", r.master.gtidSet())
		r.startSyncRoutine()

		w.WriteHeader(http.StatusNoContent)
	})
}

func handleStopSync(r *River) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.m.Lock()
		defer r.m.Unlock()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("unexpected method\n"))
			return
		}

		if !r.isRunning {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("river not running\n"))
			return
		}

		if !r.IsSyncing() {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("sync not running\n"))
			return
		}

		r.stopSyncRoutine()

		// disconnect manticore client - probably host ip changes
		if r.sphinxToken != nil {
			err := r.sup.RemoveAndWait(*r.sphinxToken, sphinxServiceStopTimeout)
			if err != nil {
				r.l.Errorf("Status: SphinxService failed to stop after waiting for %s", sphinxServiceStopTimeout)
			}

			r.sphinxToken = nil
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

func handleMaint(r *River) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("unexpected method\n"))
			return
		}

		if !r.isRunning {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("river not running\n"))
			return
		}

		index := req.URL.Query().Get("index")
		if index == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("index not specified\n"))
			return
		}

		dataSource, exists := r.c.DataSource[index]
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(fmt.Sprintf("index %s not found\n", index)))
			return
		}

		err := r.sphinxService.CheckIndexForOptimize(index, dataSource.Parts)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(errors.ErrorStack(err) + "\n"))
			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
}

func handleWaitForGTID(r *River) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			w.Write([]byte("unexpected method\n"))
			return
		}

		if !r.isRunning {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("river not running\n"))
			return
		}

		req.ParseForm()
		gtidString := req.PostForm.Get("gtid")
		if gtidString == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("gtid not specified\n"))
			return
		}

		gtid, err := mysql.ParseGTIDSet(r.c.Flavor, gtidString)
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte("invalid gtid specified\n"))
			return
		}

		timeoutString := req.PostForm.Get("timeout")
		var timerC <-chan time.Time
		if timeoutString != "" {
			timeout, err := time.ParseDuration(timeoutString)
			if err != nil {
				w.WriteHeader(http.StatusUnprocessableEntity)
				w.Write([]byte("invalid timeout specified\n"))
				return
			}
			timer := time.NewTimer(timeout)
			timerC = timer.C
		}
		subscribe := gtidSubscriber{
			c:    make(chan mysql.GTIDSet, 1),
			gtid: gtid,
			peer: req.RemoteAddr,
		}
		unsubscribe := gtidCancelSubscriber{peer: req.RemoteAddr}
		r.syncC <- subscribe
		select {
		case <-req.Context().Done():
			unsubscribe.reason = "request cancelled"
			r.syncC <- unsubscribe
		case <-timerC:
			unsubscribe.reason = "timeout"
			r.syncC <- unsubscribe
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("timeout exceeded\n"))
		case newGTID := <-subscribe.c:
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(newGTID.String() + "\n"))
		}
	})
}
