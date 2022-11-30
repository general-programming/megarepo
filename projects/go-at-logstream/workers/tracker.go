package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/bytedance/sonic"
	"github.com/general-programming/megarepo/projects/go-at-logstream/storage"
	"github.com/general-programming/megarepo/projects/go-at-logstream/util"
	socketio "github.com/googollee/go-socket.io"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"
)

type ATProjects struct {
	WarriorProject string      `json:"auto_project"`
	Projects       []ATProject `json:"projects"`
}

type ATProject struct {
	Description     string `json:"description"`
	ProjectName     string `json:"name"`
	Title           string `json:"title"`
	Repository      string `json:"repository"`
	LeaderboardLink string `json:"leaderboard"`
}

type ATStats struct {
	Values map[string]json.Number `json:"values"`
	Queues map[string]json.Number `json:"queues"`
}

type ATTrackerUpdate struct {
	Project    string `json:"project"`
	Downloader string `json:"downloader"`
	// Timestamp is a unix timestamp in seconds
	Timestamp json.Number `json:"ts"`

	Bytes       json.Number `json:"bytes"`
	Valid       bool        `json:"valid"`
	IsDuplicate bool        `json:"is_duplicate"`

	Items     []string `json:"items"`
	MoveItems []string `json:"move_items"`

	Item     string        `json:"item"`
	ItemRTTs []json.Number `json:"item_rtts"`

	WarriorUserAgent string `json:"user_agent"`
	WarriorVersion   string `json:"version"`

	SizeMB json.Number `json:"megabytes"`

	QueueStats map[string]json.Number `json:"queuestats"`
	Stats      ATStats                `json:"stats"`
	Counts     map[string]json.Number `json:"counts"`
}

func FetchProjects() *ATProjects {
	resp, err := http.Get("https://warriorhq.archiveteam.org/projects.json")
	if err != nil {
		panic("Failed to fetch projects")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		panic("Failed to read response body")
	}

	result := &ATProjects{}
	err = sonic.Unmarshal(body, result)
	if err != nil {
		panic(fmt.Sprintf("Failed to unmarshal response body: %v", err))
	}

	return result
}

func GetLogSocketNameFromLeaderboard(link string) string {
	// parses the final bit of the leaderboard link to get the project name
	// e.g. "https://tracker.archiveteam.org/grabtemp20221126" -> "grabtemp20221126"

	// this is done by getting the last part of the link, and removing the query parameters
	// (if there are any)
	split := strings.Split(strings.TrimRight(link, "/"), "/")
	last := split[len(split)-1]
	if strings.Contains(last, "?") {
		split = strings.Split(last, "?")
		return split[0]
	}

	return last
}

func OpenLogSocket(project string) {
	logger := util.CreateLogger(false)
	defer logger.Sync()

	logger.Info("opening log socket", zap.String("project", project))

	client, err := socketio.Dial("http://tracker.archiveteam.org:8080/" + project + "-log")

	if err != nil {
		logger.Error("Failed to connect to socket",
			zap.String("project", project),
			zap.Error(err),
		)
		return
	}

	client.On("connect", func(ns *socketio.NameSpace) {
		ctx := util.CreateContext()
		OnSocketConnect(ctx, ns)
	})

	client.On("log_message", func(ns *socketio.NameSpace, message string) {
		ctx := util.CreateContext()
		gopool.Go(func() {
			OnMessage(ctx, ns, message)
		})
	})

	client.Run()
}

func OnSocketConnect(ctx context.Context, ns *socketio.NameSpace) {
	util.LogWithCtx(ctx).Info("Connected to socket", zap.String("endpoint", ns.Endpoint()))
}

func OnMessage(ctx context.Context, ns *socketio.NameSpace, message string) {
	parsed := &ATTrackerUpdate{}
	err := sonic.UnmarshalString(message, parsed)
	if err != nil {
		util.LogWithCtx(ctx).Error("Failed to unmarshal message", zap.String("msg", message), zap.Error(err))
		return
	}

	// create item text
	var itemsText string
	if len(parsed.Items) > 1 {
		itemsText = fmt.Sprintf("%d items", len(parsed.Items))
	} else {
		itemsText = parsed.Item
	}

	// create size text
	size, _ := parsed.SizeMB.Float64()
	sizeText := fmt.Sprintf("%.2fMB", size)

	util.LogWithCtx(ctx).Debug("log_message",
		zap.String("project", parsed.Project),
		zap.String("downloader", parsed.Downloader),
		zap.String("items", itemsText),
		zap.String("size", sizeText),
	)

	// append to redis
	if err := storage.WrappedRedis.AppendLog(ctx, "archiveteam.tracker", map[string]string{
		"project": parsed.Project,
		"message": message,
	}); err != nil {
		util.LogWithCtx(ctx).Error("Failed to append to redis", zap.Error(err))
	}

	// send to influx
	timestampFloat, err := parsed.Timestamp.Float64()
	if err != nil {
		util.LogWithCtx(ctx).Error("Failed to parse timestamp", zap.Error(err), zap.String("timestamp", parsed.Timestamp.String()))
		return
	}
	timestamp := time.UnixMilli(int64(math.Round(timestampFloat * 1000)))

	tags := map[string]string{
		"project":         parsed.Project,
		"downloader":      parsed.Downloader,
		"warrior_version": parsed.WarriorVersion,
	}

	fields := map[string]interface{}{
		"size":  size,
		"items": len(parsed.Items),
	}

	point := write.NewPoint("archiveteam.tracker.event", tags, fields, timestamp)
	storage.WrappedInflux.Writer.WritePoint(point)
}

func TrackerMain() {
	// TODO(erin) actual app, please split this up.
	logger := util.CreateLogger(false)
	defer logger.Sync()

	var wg sync.WaitGroup
	projects := FetchProjects()
	logger.Info("got tracker projects", zap.Int("count", len(projects.Projects)))

	counter := 0
	for _, project := range projects.Projects {
		// special case to ignore urlteam
		if project.ProjectName == "urlteam2" {
			continue
		}

		wg.Add(1)
		go func(project ATProject) {
			defer wg.Done()
			socketName := GetLogSocketNameFromLeaderboard(project.LeaderboardLink)
			for {
				OpenLogSocket(socketName)
				logger.Warn("socket closed, reconnecting", zap.String("project", project.ProjectName))
				time.Sleep(1 * time.Second)
			}
		}(project)
		counter += 1
	}

	wg.Wait()
}
