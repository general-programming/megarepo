package storage

import (
	"context"
	"fmt"
	"os"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxapi "github.com/influxdata/influxdb-client-go/v2/api"

	"github.com/influxdata/influxdb-client-go/v2/api/write"
)

type Influx2Client struct {
	Client influxdb2.Client
	Writer influxapi.WriteAPI
}

func (i Influx2Client) Emit(ctx context.Context, metric Metric) (err error) {
	var timestamp time.Time

	if metric.Timestamp == nil {
		timestamp = time.Now()
	} else {
		timestamp = *metric.Timestamp
	}

	point := write.NewPoint(metric.Name, metric.Tags, metric.Values, timestamp)
	i.Writer.WritePoint(point)

	return
}

func NewInflux2Client() (client Influx2Client, err error) {
	token := os.Getenv("INFLUXDB_TOKEN")
	url := os.Getenv("INFLUXDB_HOST")

	client = Influx2Client{
		Client: influxdb2.NewClientWithOptions(url, token, influxdb2.DefaultOptions().SetUseGZip(true)),
	}

	// setup writer
	org := "genprog"
	bucket := "archiveteam-tracker"
	client.Writer = client.Client.WriteAPI(org, bucket)

	errorsCh := client.Writer.Errors()
	go func() {
		for err := range errorsCh {
			fmt.Printf("influx write error: %s\n", err.Error())
		}
	}()

	return
}
