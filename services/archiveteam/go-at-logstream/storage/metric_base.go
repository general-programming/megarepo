package storage

import (
	"context"
	"time"
)

type Metric struct {
	Name   string
	Values map[string]interface{}
	Tags   map[string]string

	Timestamp *time.Time
}

type BaseMetricClient interface {
	Emit(ctx context.Context, metric Metric) error
}
