package river

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jpillora/backoff"
	"github.com/juju/errors"
	"github.com/thejerf/suture"

	"github.com/sandeepone/mysql-manticore/sphinx"
	"github.com/siddontang/go-mysql/canal"

	"gopkg.in/birkirb/loggers.v1"
	"gopkg.in/birkirb/loggers.v1/log"
)

// River is (actually, was) a pluggable service within Elasticsearch that pulls data from an external source.
// https://www.elastic.co/blog/the-river
// https://www.elastic.co/blog/deprecating-rivers
// We use this definition here for brevity, although this service obviously does not run within Elasticsearch.
type River struct {
	Log loggers.Contextual

	l loggers.Advanced

	c *Config

	canal *canal.Canal

	ctx    context.Context
	Cancel context.CancelFunc

	sph []*sphinx.SphConn

	sphinxService *SphinxService
	syncService   *SyncService

	StatService *stat

	master *masterState

	// protects isRunning flag
	m sync.Mutex

	isRunning bool

	syncC chan interface{}

	done chan struct{}

	FatalErrC chan error

	sup         *suture.Supervisor
	sphinxToken *suture.ServiceToken
	cronToken   *suture.ServiceToken
	syncToken   *suture.ServiceToken
	canalToken  *suture.ServiceToken

	syncM sync.Mutex
}

var errSphinxDisconnected = errors.New("manticore connections are already closed")
var errIndexesNotReady = errors.New("indexes not ready")
var errWaitForGTIDTimedOut = errors.New("waited for GTID sync for too long")

const canalServiceStopTimeout = 10 * time.Second
const cronServiceStopTimeout = 10 * time.Second
const sphinxServiceStopTimeout = 10 * time.Second
const syncServiceStopTimeout = 30 * time.Second
const switchBuildModeTimeout = 5 * time.Second

// NewRiver creates the River from config
func NewRiver(c *Config, log loggers.Contextual) (*River, error) {
	var err error
	r := new(River)

	r.Log = log

	r.l = log.WithFields("service", r.String())

	r.c = c

	r.done = make(chan struct{})

	r.syncC = make(chan interface{}, 40960)

	r.FatalErrC = make(chan error, 64)

	if err = r.newCanal(); err != nil {
		return nil, errors.Trace(err)
	}

	if err = r.prepareRule(); err != nil {
		return nil, errors.Trace(err)
	}

	r.master = newMasterState(r.c)

	r.StatService = &stat{r: r}

	r.sup = suture.New("river", suture.Spec{
		Timeout: 3 * time.Second,
		Log: func(msg string) {
			r.Log.WithFields("library", "suture").Info(msg)
		},
	})

	r.sphinxService = NewSphinxService(r)
	r.syncService = NewSyncService(r)

	if err = r.CheckBinlogRowImage(); err != nil {
		return nil, errors.Trace(err)
	}

	return r, nil
}

func (r *River) newCanal() error {
	cfg := canal.NewDefaultConfig()
	cfg.Addr = r.c.MyAddr
	cfg.User = r.c.MyUser
	cfg.Password = r.c.MyPassword
	cfg.Charset = r.c.MyCharset
	cfg.Flavor = r.c.Flavor
	cfg.HeartbeatPeriod = r.c.HeartbeatPeriod.Duration
	cfg.ParseTime = true

	cfg.IncludeTableRegex = []string{}
	for _, rule := range r.c.IngestRules {
		cfg.IncludeTableRegex = append(cfg.IncludeTableRegex, regexp.QuoteMeta(rule.TableName))
	}

	cfg.ServerID = r.c.ServerID
	cfg.Dump.ExecutionPath = r.c.DumpExec
	cfg.Dump.DiscardErr = false
	cfg.Dump.SkipMasterData = r.c.SkipMasterData

	var err error
	r.canal, err = canal.NewCanal(cfg)
	return errors.Trace(err)
}

// CheckBinlogRowImage row image must be FULL
func (r *River) CheckBinlogRowImage() error {
	res, err := r.canal.Execute(`SHOW GLOBAL VARIABLES LIKE "binlog_row_image"`)
	if err != nil {
		return errors.Trace(err)
	}

	rowImage, _ := res.GetString(0, 1)
	if !strings.EqualFold(rowImage, "FULL") {
		return errors.Errorf("MySQL uses '%s' binlog row image, but we want FULL", rowImage)
	}

	return nil
}

// Serve starts the River service
func (r *River) Serve() {
	r.m.Lock()
	r.isRunning = true
	r.m.Unlock()

	r.l.Infof("Starting")

	err := r.run()
	if err != nil {
		r.FatalErrC <- err
	}
	<-r.done
}

func (r *River) run() error {
	var err error

	r.ctx, r.Cancel = context.WithCancel(context.Background())

	r.sup.ServeBackground()

	r.sphinxService.RequestStartNotification()

	t := r.sup.Add(r.sphinxService)
	r.sphinxToken = &t

	r.sphinxService.WaitUntilStarted()

	b := &backoff.Backoff{
		Min:    1 * time.Second,
		Max:    20 * time.Minute,
		Factor: 2,
		Jitter: true,
	}
	defer b.Reset()

	// get master state - wait until get state or timeout
	for {
		time.Sleep(b.Duration())

		err = r.sphinxService.LoadSyncState(r.master.syncState())
		if err != nil {
			r.l.Errorf("one or more manticore backends are not up to date: %v", err)
		}

		if err == nil {
			b.Reset()
			r.l.Infof("Connected to manticore backend")
			break
		}
	}

	if err != nil {
		// r.l.Errorf("one or more manticore backends are not up to date: %v", err)
		return errors.Trace(err)
	}

	// check indexes are ready - wait until ready or timeout
	for {
		time.Sleep(b.Duration())

		err = r.checkAllIndexesReady()
		if err == nil {
			b.Reset()
			break
		}
	}

	if err != nil {
		return errors.Trace(err)
	}

	r.master.needPositionReset = false

	if r.cronToken == nil {
		t := r.sup.Add(NewCronService(r))
		r.cronToken = &t
	}

	r.startSyncRoutine()

	return nil
}

// Stop stops the River service
func (r *River) Stop() {
	r.m.Lock()
	defer r.m.Unlock()
	if !r.isRunning {
		return
	}

	r.l.Infof("stopping river")

	r.stopSyncRoutine()

	if r.cronToken != nil {
		err := r.sup.RemoveAndWait(*r.cronToken, cronServiceStopTimeout)
		if err != nil {
			r.l.Errorf("CronService failed to stop after waiting for %s", cronServiceStopTimeout)
		}
		r.cronToken = nil
	}

	r.Cancel()

	if r.sphinxToken != nil {
		err := r.sup.RemoveAndWait(*r.sphinxToken, sphinxServiceStopTimeout)
		if err != nil {
			r.l.Errorf("SphinxService failed to stop after waiting for %s", sphinxServiceStopTimeout)
		}
		r.sphinxToken = nil
	}

	r.sup.Stop()

	r.done <- struct{}{}
	r.isRunning = false
}

func (r *River) String() string {
	return "MainRiverService"
}

func (r *River) IsRunning() bool {
	return r.isRunning
}

func (r *River) IsSyncing() bool {
	if !r.isRunning {
		return false
	}

	if r.syncToken != nil && r.canalToken != nil {
		return true
	}

	return false
}

// SaveState saves current state to file and to sphinx backends
func (r *River) SaveState() {
	err := r.sphinxService.SaveSyncState()
	if err != nil {
		r.l.Errorf("could not save synchronization state: %s", errors.ErrorStack(err))
	}
}

func (r *River) startSyncRoutine() {
	r.syncM.Lock()
	defer r.syncM.Unlock()

	if r.syncToken == nil {
		t := r.sup.Add(r.syncService)
		r.syncToken = &t
	}

	if r.canalToken == nil {
		t := r.sup.Add(NewCanalService(r))
		r.canalToken = &t
	}
}

func (r *River) stopSyncRoutine() {
	r.syncM.Lock()
	defer r.syncM.Unlock()

	if r.canalToken != nil {
		err := r.sup.RemoveAndWait(*r.canalToken, canalServiceStopTimeout)
		if err != nil {
			r.l.Errorf("CanalService failed to stop after waiting for %s", canalServiceStopTimeout)
		}
		r.canalToken = nil
	}

	if r.syncToken != nil {
		err := r.sup.RemoveAndWait(*r.syncToken, syncServiceStopTimeout)
		if err != nil {
			r.l.Errorf("SyncService failed to stop after waiting for %s", syncServiceStopTimeout)
		}
		r.syncToken = nil
	}
}

func (r *River) checkAllIndexesForOptimize() {
	for index, cfg := range r.c.DataSource {
		err := r.sphinxService.CheckIndexForOptimize(index, cfg.Parts)
		if err != nil {
			log.Warnf("periodic optimize error: %s", errors.ErrorStack(err))
		}
	}
}

func (r *River) checkAllIndexesReady() error {
	if r.c.SkipSphIndexCheck {
		return nil
	}

	indexes := []string{}
	for index, cfg := range r.c.DataSource {
		ok, err := r.sphinxService.IndexIsReady(index, cfg.Parts)
		if err != nil {
			r.l.Errorf("Index got error for [%s] waiting %v", index, err)
			return errors.Trace(err)
		}

		if !ok {
			indexes = append(indexes, index)
		}
	}

	if len(indexes) == 0 {
		r.l.Infof("All indexes are ready to listen for events")
		return nil
	}

	if len(indexes) == len(r.c.DataSource) {
		r.l.Errorf("All indexes not ready")
		return errors.Trace(errIndexesNotReady)
	}

	if len(indexes) > 0 {
		r.l.Errorf("%d index(s) not ready", len(indexes))
		return errors.Trace(errIndexesNotReady)
	}

	return nil
}

func (r *River) prepareRule() error {
	if r.c.IngestRules != nil {
		for key, rule := range r.c.IngestRules {

			if strings.Contains(rule.TableName, ".") {
				s := strings.Split(rule.TableName, ".")

				if err := r.updateRule(key, s[0], s[1]); err != nil {
					return errors.Trace(err)
				}
			}
		}
	}

	return nil
}

func (r *River) updateRule(key int, schema, table string) error {
	tableInfo, err := r.canal.GetTable(schema, table)
	if err != nil {
		return errors.Trace(err)
	}

	r.c.IngestRules[key].TableInfo = tableInfo

	return nil
}

func rowCount(e *canal.RowsEvent) int {
	rows := len(e.Rows)
	if e.Action == canal.UpdateAction {
		rows = rows / 2
	}
	return rows
}

func executeMysqlQuery(canal *canal.Canal, query string) error {
	log.Infof("[mysql] %s", query)
	_, err := canal.Execute(query)
	if err != nil {
		return errors.Trace(err)
	}
	return nil
}
