package gocommon

import (
	"net/http"
	_ "net/http/pprof"
)

func StartDebugServer() {
	go func() {
		http.ListenAndServe(":6060", nil)
	}()
}
