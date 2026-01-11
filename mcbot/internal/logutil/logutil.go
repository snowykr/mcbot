package logutil

import (
	"log"
	"os"
	"strings"
	"sync/atomic"
)

var debugEnabled atomic.Bool

func init() {
	debugEnv := os.Getenv("MCBOT_DEBUG")
	debugEnabled.Store(strings.ToLower(debugEnv) == "true" || debugEnv == "1")
}

func Infof(format string, v ...interface{}) {
	log.Printf(format, v...)
}

func Debugf(format string, v ...interface{}) {
	if debugEnabled.Load() {
		log.Printf("[DEBUG] "+format, v...)
	}
}
