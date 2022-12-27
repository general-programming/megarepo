package storage

var MetricsClient BaseMetricClient

func InitStorage() {
	var err error

	// setup metrics
	MetricsClient, err = NewInflux2Client()
	if err != nil {
		panic(err)
	}

	// setup db
	InitRedis()
}
