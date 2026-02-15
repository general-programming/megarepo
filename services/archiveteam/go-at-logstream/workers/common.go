package workers

import (
	"github.com/general-programming/megarepo/go/common"
)

var (
	PushRedis  = common.GetBoolEnvWithDefault("PUSH_REDIS", false)
	PushInflux = common.GetBoolEnvWithDefault("PUSH_INFLUX", false)
)
