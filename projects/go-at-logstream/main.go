package main

import (
	"github.com/general-programming/megarepo/projects/go-at-logstream/storage"
	"github.com/general-programming/megarepo/projects/go-at-logstream/util"
	"github.com/general-programming/megarepo/projects/go-at-logstream/workers"
)

func main() {
	// do init
	util.StartDebugServer()
	storage.InitRedis()
	storage.InitInflux()

	workerType := util.GetEnvWithDefault("WORKER_TYPE", "tracker")
	if workerType == "tracker" {
		workers.TrackerMain()
	} else if workerType == "archivebot" {
		workers.ArchiveBotMain()
	} else {
		panic("invalid worker type: " + workerType)
	}
}
