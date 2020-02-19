package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juju/errors"
	"github.com/thejerf/suture"

	"github.com/gleez/pkg/httplib"

	"github.com/sandeepone/mysql-manticore/river"
	"github.com/sandeepone/mysql-manticore/util"

	"gopkg.in/birkirb/loggers.v1/log"
)

// kubernetes leader-elect sidecar
const LEADER_ELECT_ADDR string = "http://localhost:4040"

var (
	// LDFLAGS should overwrite these variables in build time.
	version  string
	revision string

	done chan struct{}
	mu   sync.Mutex // guards leader

	host string = hostname()

	rootSup    *suture.Supervisor
	r          *river.River
	riverToken suture.ServiceToken
)

type strList []string

type LeaderData struct {
	Name string `json:"name"`
}

func (s *strList) String() string {
	return strings.Join(*s, " ")
}

func (s *strList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func getVersion() string {
	return fmt.Sprintf("mysql-manticore %s (%s) ; go runtime %s", version, revision, runtime.Version())
}

func run() (err error) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	flags := flag.NewFlagSet("", flag.ContinueOnError)

	var configFile string
	var myAddr string
	var sphAddr strList
	var logLevel string
	var logFile string
	var rebuildAndExit bool
	var showVersion bool
	var k8Leader bool

	flags.StringVar(&configFile, "config", "../../temp/etc/river.toml", "config file")
	flags.StringVar(&myAddr, "my-addr", "", "MySQL replica address")
	flags.Var(&sphAddr, "sph-addr", "Manticore address")
	flags.StringVar(&logLevel, "log-level", "info", "log level")
	flags.StringVar(&logFile, "log-file", "", "log file; will log to stdout if empty")
	flags.BoolVar(&rebuildAndExit, "rebuild-and-exit", false, "rebuild all configured indexes and exit")
	flags.BoolVar(&showVersion, "version", false, "show program version and exit")
	flags.BoolVar(&k8Leader, "k8-leader", false, "Use kubernetes leader elector sidecar?")

	if err = flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	if showVersion {
		_, err = fmt.Printf("%s\n", getVersion())
		return err
	}

	if err = util.InitLogger(logLevel, logFile, getVersion()); err != nil {
		return err
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc,
		os.Kill,
		os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	cfg, err := river.NewConfigWithFile(configFile)
	if err != nil {
		return err
	}

	if myAddr != "" {
		cfg.MyAddr = myAddr
	}

	if len(sphAddr) > 0 {
		cfg.SphAddr = sphAddr
	}

	r, err = river.NewRiver(cfg, log.Logger, rebuildAndExit)
	if err != nil {
		return err
	}

	rootSup = suture.New("root", suture.Spec{
		FailureThreshold: -1,
		FailureBackoff:   10 * time.Second,
		Timeout:          time.Minute,
		Log: func(msg string) {
			log.WithFields("library", "suture").Info(msg)
		},
	})

	rootSup.Add(r.StatService)
	rootSup.ServeBackground()

	if k8Leader {
		log.Infof("Starting in kubernetes mode")

		// Start the goroutine that will check for master once per 30 seconds.
		go runMasterLoop()
	} else {
		log.Infof("Starting without cluster support")

		riverToken = rootSup.Add(r)
	}

	select {
	case n := <-sc:
		log.Infof("received signal %v, exiting", n)
	case err = <-r.FatalErrC:
		if errors.Cause(err) == river.ErrRebuildAndExitFlagSet {
			log.Info(err.Error())
			err = nil
		}
	}

	rootSup.Stop()
	return err
}

// k8s run master loop will check for master once per 30 seconds.
func runMasterLoop() {
	ticker := time.NewTicker(30 * time.Second)
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			fetchLeader()
		}
	}
}

func fetchLeader() {
	mu.Lock()
	defer mu.Unlock()

	var leader LeaderData
	if err := httplib.Get(LEADER_ELECT_ADDR).Setting(httplib.CustomSetting).ToJSON(&leader); err != nil {
		log.Errorf("Error fetchLeader %v", err)
		return
	}

	if len(leader.Name) > 3 {
		// this is leader and river not running - start river service
		if leader.Name == host && !r.IsRunning() {
			riverToken = rootSup.Add(r)
			log.Infof("New leader elected %s", leader.Name)
		}

		// this is not leader and river is running - stop river service
		if leader.Name != host && r.IsRunning() {
			log.Infof("Leadership changed leader %s", leader.Name)

			if err := rootSup.RemoveAndWait(riverToken, 20*time.Second); err != nil {
				log.Errorf("Error fetchLeader stop service [%s] %v", host, err)
			}
		}
	}
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	return name
}

func main() {
	err := run()
	if err != nil {
		// Fatalf also exits with exit-code 1
		log.Fatalf(errors.ErrorStack(err))
	}
}
