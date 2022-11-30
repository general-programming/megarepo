package storage

import (
	"os"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	influxapi "github.com/influxdata/influxdb-client-go/v2/api"
)

var (
	WrappedInflux InfluxClient
)

type InfluxClient struct {
	Client influxdb2.Client
	Writer influxapi.WriteAPI
}

func InitInflux() {
	token := os.Getenv("INFLUXDB_TOKEN")
	if token == "" {
		println("Influx token is missing.")
		// util.GetLogger()
		return
	}
	url := os.Getenv("INFLUXDB_HOST")
	WrappedInflux = InfluxClient{
		Client: influxdb2.NewClientWithOptions(url, token, influxdb2.DefaultOptions().SetUseGZip(true)),
	}

	// setup writer
	org := "genprog"
	bucket := "archiveteam-tracker"
	WrappedInflux.Writer = WrappedInflux.Client.WriteAPI(org, bucket)
}
