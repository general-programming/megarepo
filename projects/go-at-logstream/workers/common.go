package workers

import (
	"github.com/general-programming/megarepo/projects/go-at-logstream/util"
)

var (
	PushRedis  bool = util.GetBoolEnvWithDefault("PUSH_REDIS", false)
	PushInflux bool = util.GetBoolEnvWithDefault("PUSH_INFLUX", false)
)