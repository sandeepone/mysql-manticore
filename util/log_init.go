package util

import (
	"os"
	"time"

	mapper "github.com/birkirb/loggers-mapper-logrus"
	"github.com/juju/errors"
	"github.com/sirupsen/logrus"
	"gopkg.in/birkirb/loggers.v1/log"
)

func InitLogger(logLevel string, logFile string, version string) error {
	l := logrus.New()
	if logFile == "" {
		l.Out = os.Stdout
	} else {
		f, err := os.OpenFile(logFile, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			return errors.Trace(err)
		}
		l.Out = f
	}
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	l.Level = level
	l.Formatter = &logrus.TextFormatter{TimestampFormat: time.RFC3339Nano}
	log.Logger = mapper.NewLogger(l)

	SetupLogAdapter(log.Logger, logLevel, "library", "go-mysql")
	log.Infof("starting %s", version)
	return nil
}
