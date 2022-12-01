package workers

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/general-programming/megarepo/projects/go-at-logstream/storage"
	"github.com/general-programming/megarepo/projects/go-at-logstream/util"
	"github.com/gorilla/websocket"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/bytedance/sonic"
)

/*
{
  "ts": 1669790842.318459,
  "url": "https://booth.pximg.net/d08187e0-0fda-43a7-8b19-40c83710d3a0/i/3157989/4de139b2-dd09-4ab9-a31d-7b8178fc775f.jpg",
  "response_code": 200,
  "wget_code": "OK",
  "is_error": false,
  "is_warning": false,
  "type": "download",
  "job_data": {
    "items_downloaded": "6547509",
    "concurrency": "5",
    "heartbeat": "1003121",
    "items_queued": "18071523",
    "no_offsite_links": "true",
    "last_trimmed_log_entry": "5110272.0",
    "queued_at": "1668578220",
    "url": "https://booth.pm/en",
    "r2xx": "5018724",
    "r5xx": "23",
    "settings_age": "18",
    "runk": "7811",
    "last_analyzed_log_entry": "5110281.0",
    "last_broadcasted_log_entry": "5110281.0",
    "user_agent": "",
    "r1xx": "0",
    "log_score": "5110282",
    "ignore_patterns_set_key": "12olda9c4lobzxfhv5ihdjvqf_ignores",
    "fetch_depth": "inf",
    "log_key": "12olda9c4lobzxfhv5ihdjvqf_log",
    "pipeline_id": "pipeline:e88002fe385f8be5bba14c1bafeb5a8f",
    "started_in": "#archivebot",
    "error_count": "7907",
    "ts": "1669790842.2456272",
    "last_acknowledged_heartbeat": "1003121",
    "started_by": "Fusl",
    "slug": "booth.pm-inf",
    "delay_min": "250",
    "bytes_downloaded": "1282744941202",
    "r4xx": "26030",
    "death_timer": "0",
    "started_at": "1668578220.789247",
    "note": "https://booth.pm/announcements/616; https://www.pixiv.net/info.php?id=8789",
    "delay_max": "375",
    "suppress_ignore_reports": "true",
    "r3xx": "50125",
    "ident": "12olda9c4lobzxfhv5ihdjvqf"
  }
}
*/

type ArchiveBotMessage struct {
	// Timestamp is a unix timestamp in seconds
	Timestamp json.Number `json:"ts"`

	Url          string            `json:"url"`
	ResponseCode int               `json:"response_code"`
	WgetCode     string            `json:"wget_code"`
	IsError      bool              `json:"is_error"`
	IsWarning    bool              `json:"is_warning"`
	Type         string            `json:"type"`
	JobInfo      ArchiveBotJobInfo `json:"job_data"`
}

type ArchiveBotJobInfo struct {
	ItemsDownloaded string `json:"items_downloaded"`
	Concurrency     string `json:"concurrency"`
	Heartbeat       string `json:"heartbeat"`
	ItemsQueued     string `json:"items_queued"`
	NoOffsiteLinks  string `json:"no_offsite_links"`
	LastTrimmedLog  string `json:"last_trimmed_log_entry"`
	QueuedAt        string `json:"queued_at"`
	Url             string `json:"url"`
	R2xx            string `json:"r2xx"`
	R5xx            string `json:"r5xx"`
	SettingsAge     string `json:"settings_age"`
	Runk            string `json:"runk"`
	LastAnalyzedLog string `json:"last_analyzed_log_entry"`
	LastBroadcasted string `json:"last_broadcasted_log_entry"`
	UserAgent       string `json:"user_agent"`
	R1xx            string `json:"r1xx"`
	LogScore        string `json:"log_score"`
	IgnorePatterns  string `json:"ignore_patterns_set_key"`
	LogKey          string `json:"log_key"`
	PipelineId      string `json:"pipeline_id"`
	StartedIn       string `json:"started_in"`
	ErrorCount      string `json:"error_count"`
	Ts              string `json:"ts"`
	LastHeartbeat   string `json:"last_acknowledged_heartbeat"`
	StartedBy       string `json:"started_by"`
	Slug            string `json:"slug"`
	DelayMin        string `json:"delay_min"`
	BytesDownloaded string `json:"bytes_downloaded"`
	R4xx            string `json:"r4xx"`
	DeathTimer      string `json:"death_timer"`
	StartedAt       string `json:"started_at"`
	Note            string `json:"note"`
	DelayMax        string `json:"delay_max"`
	SuppressIgnores string `json:"suppress_ignore_reports"`
	R3xx            string `json:"r3xx"`
	Ident           string `json:"ident"`
}

func HandleArchiveBotMessage(ctx context.Context, message []byte) {
	parsed := &ArchiveBotMessage{}
	err := sonic.Unmarshal(message, parsed)
	if err != nil {
		util.LogWithCtx(ctx).Error("Failed to unmarshal message", zap.String("msg", string(message)), zap.Error(err))
		return
	}

	// append to redis
	if PushRedis {
		if err := storage.WrappedRedis.AppendLog(ctx, "archiveteam.archivebot", map[string]string{
			"job":     parsed.JobInfo.Url,
			"message": string(message),
		}); err != nil {
			util.LogWithCtx(ctx).Error("Failed to append to redis", zap.Error(err))
		}
	}

	// send to influx
	if PushInflux {
		timestampFloat, err := parsed.Timestamp.Float64()
		if err != nil {
			util.LogWithCtx(ctx).Error("Failed to parse timestamp", zap.Error(err), zap.String("timestamp", parsed.Timestamp.String()))
			return
		}
		timestamp := time.UnixMilli(int64(math.Round(timestampFloat * 1000)))

		tags := map[string]interface{}{
			"count":         1,
			"job":           parsed.JobInfo.Url,
			"response_code": parsed.ResponseCode,
			"wget_code":     parsed.WgetCode,
		}

		fields := map[string]interface{}{
			"is_error":   parsed.IsError,
			"is_warning": parsed.IsWarning,
			"type":       parsed.Type,
		}

		point := write.NewPoint("archiveteam.tracker.event", tags, fields, timestamp)
		storage.WrappedInflux.Writer.WritePoint(point)
	}
}

func ArchiveBotMain() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	u := url.URL{Scheme: "ws", Host: "archivebot.com:4568", Path: "/stream"}
	log.Printf("connecting to %s", u.String())

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{})

	gopool.Go(func() {
		defer close(done)
		for {
			ctx := util.CreateContext()
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}

			HandleArchiveBotMessage(ctx, message)
		}
	})

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-interrupt:
			log.Println("interrupt")

			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}
