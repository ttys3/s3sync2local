package main

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/ttys3/rotatefilehook"
)

var logger = logrus.New()

// LoggerInitError is a custom error representation
func LoggerInitError(err error) {
	logger.Error(err)
	osExit(3)
}

func initLogger(config *Config) {
	logLevels := map[string]logrus.Level{
		"trace": logrus.TraceLevel,
		"debug": logrus.DebugLevel,
		"info":  logrus.InfoLevel,
		"warn":  logrus.WarnLevel,
		"error": logrus.ErrorLevel,
		"fatal": logrus.FatalLevel,
		"panic": logrus.PanicLevel,
	}

	// set default loglevel
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}

	if LogLevel, ok := logLevels[config.LogLevel]; !ok {
		LoggerInitError(fmt.Errorf("log level definition not found for '%s'", config.LogLevel))
	} else {
		logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true})
		logger.SetOutput(os.Stdout)
		logger.SetLevel(LogLevel)

		if config.LogLevel == "trace" || config.LogLevel == "debug" {
			logger.SetReportCaller(true)
		}

		rotateFileHook, err := rotatefilehook.NewRotateFileHook(rotatefilehook.RotateFileConfig{
			Filename:   "s3-sync.log",
			MaxSize:    5, // the maximum size in megabytes
			MaxBackups: 7, // the maximum number of old log files to retain
			MaxAge:     7, // the maximum number of days to retain old log files
			LocalTime:  true,
			Level:      logrus.DebugLevel,
			Formatter:  &logrus.TextFormatter{FullTimestamp: true},
		})
		if err != nil {
			panic(err)
		}
		logger.AddHook(rotateFileHook)
	}
}
