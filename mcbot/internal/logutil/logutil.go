package logutil

import (
	"log"
	"os"
	"strings"
)

var debugEnabled bool

func init() {
	debugEnv := os.Getenv("MCBOT_DEBUG")
	debugEnabled = strings.ToLower(debugEnv) == "true" || debugEnv == "1"
}

func Infof(format string, v ...interface{}) {
	log.Printf(format, v...)
}

func Debugf(format string, v ...interface{}) {
	if debugEnabled {
		log.Printf("[DEBUG] "+format, v...)
	}
}
