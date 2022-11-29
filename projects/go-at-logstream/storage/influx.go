package storage

import (
	"os"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	// "github.com/influxdata/influxdb-client-go/v2/api/write"
)

var (
	InfluxClient influxdb2.Client
)

func InitInflux() {
	token := os.Getenv("INFLUXDB_TOKEN")
	if token == "" {
		println("Influx token is missing.")
		// util.GetLogger()
		return
	}
	url := os.Getenv("INFLUXDB_HOST")
	InfluxClient = influxdb2.NewClient(url, token)
}
