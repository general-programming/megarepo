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

	workers.TrackerMain()
}
