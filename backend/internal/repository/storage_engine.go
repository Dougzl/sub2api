package repository

import (
	"strings"
	"sync/atomic"
)

var runtimeStorageEngine atomic.Value // string

func setRuntimeStorageEngine(engine string) {
	runtimeStorageEngine.Store(strings.ToLower(strings.TrimSpace(engine)))
}

func isH2Storage() bool {
	v, _ := runtimeStorageEngine.Load().(string)
	return v == "h2" || v == "sqlite"
}

func isSQLiteStorage() bool {
	v, _ := runtimeStorageEngine.Load().(string)
	return v == "sqlite"
}
