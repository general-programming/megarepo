package main

import (
	"github.com/general-programming/gocommon"
	"github.com/general-programming/megarepo/services/archiveteam/go-at-logstream/storage"
	"github.com/general-programming/megarepo/services/archiveteam/go-at-logstream/workers"
)

func main() {
	// do init
	gocommon.StartDebugServer()
	storage.InitStorage()

	workerType := gocommon.GetEnvWithDefault("WORKER_TYPE", "tracker")
	if workerType == "tracker" {
		workers.TrackerMain()
	} else if workerType == "archivebot" {
		workers.ArchiveBotMain()
	} else {
		panic("invalid worker type: " + workerType)
	}
}
