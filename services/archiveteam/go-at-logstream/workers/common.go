package workers

import (
	"github.com/general-programming/gocommon"
)

var (
	PushRedis  = gocommon.GetBoolEnvWithDefault("PUSH_REDIS", false)
	PushInflux = gocommon.GetBoolEnvWithDefault("PUSH_INFLUX", false)
)
