package workers

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/bytedance/sonic"
	"github.com/general-programming/gocommon"
	"github.com/general-programming/megarepo/services/archiveteam/go-at-logstream/model"
	"github.com/general-programming/megarepo/services/archiveteam/go-at-logstream/storage"
	socketio "github.com/googollee/go-socket.io"
	"go.uber.org/zap"
)

func FetchProjects() (*model.ATTrackerProjects, error) {
	resp, err := http.Get("https://warriorhq.archiveteam.org/projects.json")
	if err != nil {
		return nil, errors.New("failed to fetch projects")
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			gocommon.CreateLogger().Error("Failed to close body", zap.Error(err))
		}
	}(resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("failed to read response body")
	}

	result := &model.ATTrackerProjects{}
	err = sonic.Unmarshal(body, result)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	return result, nil
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

type TrackerSocket struct {
	Worker  TrackerWorker
	Project string
}

func (sock *TrackerSocket) Connect() {
	logger := gocommon.CreateLogger()
	defer gocommon.RecoverFunction("TrackerSocket.Connect")

	logger.Info("opening log socket", zap.String("project", sock.Project))

	client, err := socketio.Dial(gocommon.GetEnvWithDefault("TRACKER_URL", "http://tracker.archiveteam.org:8080") + "/" + sock.Project + "-log")

	if err != nil {
		logger.Error("Failed to connect to socket",
			zap.String("project", sock.Project),
			zap.Error(err),
		)
		return
	}

	if !(sock.MustHook(client, "connect", sock.OnConnect) &&
		sock.MustHook(client, "log_message", sock.OnMessage)) {
		return
	}

	client.Run()
}

func (sock *TrackerSocket) MustHook(client *socketio.Client, name string, fn interface{}) bool {
	err := client.On(name, fn)
	logger := gocommon.CreateLogger()

	if err != nil {
		logger.Error("Failed to create socket hook.", zap.Error(err))
		return false
	}

	return true
}

func (sock *TrackerSocket) OnConnect(ns *socketio.NameSpace) {
	defer gocommon.RecoverFunction("TrackerSocket.OnConnect")
	ctx := gocommon.CreateContext()

	gocommon.LogWithCtx(ctx).Info("Connected to socket", zap.String("endpoint", ns.Endpoint()))
}

func (sock *TrackerSocket) OnMessage(_ *socketio.NameSpace, message string) {
	defer gocommon.RecoverFunction("TrackerSocket.OnMessage")
	ctx := gocommon.CreateContext()

	parsed := &model.ATTrackerUpdate{}
	err := sonic.UnmarshalString(message, parsed)
	if err != nil {
		gocommon.LogWithCtx(ctx).Error("Failed to unmarshal message", zap.String("msg", message), zap.Error(err))
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

	gocommon.LogWithCtx(ctx).Debug("log_message",
		zap.String("project", parsed.Project),
		zap.String("downloader", parsed.Downloader),
		zap.String("items", itemsText),
		zap.String("size", sizeText),
	)

	// append to redis
	_, _ = storage.WrappedRedis.Client.Publish(ctx, "tracker-log", message).Result()
	if PushRedis {
		if err := storage.WrappedRedis.AppendLog(ctx, "archiveteam.tracker", map[string]string{
			"project": parsed.Project,
			"message": message,
		}); err != nil {
			gocommon.LogWithCtx(ctx).Error("Failed to append to redis", zap.Error(err))
		}
	}

	// send to influx
	if PushInflux {
		timestampFloat, err := parsed.Timestamp.Float64()
		if err != nil {
			gocommon.LogWithCtx(ctx).Error("Failed to parse timestamp", zap.Error(err), zap.String("timestamp", parsed.Timestamp.String()))
			return
		}
		timestamp := time.UnixMilli(int64(math.Round(timestampFloat * 1000)))

		tags := map[string]string{
			"project":         parsed.Project,
			"downloader":      parsed.Downloader,
			"warrior_version": parsed.WarriorVersion,
			"valid":           strconv.FormatBool(parsed.Valid),
			"duplicate":       strconv.FormatBool(parsed.IsDuplicate),
		}

		fields := map[string]interface{}{
			"size":  size,
			"items": len(parsed.Items),
		}

		err = storage.MetricsClient.Emit(ctx, storage.Metric{
			Name:      "archiveteam.tracker.event",
			Tags:      tags,
			Values:    fields,
			Timestamp: &timestamp,
		})
		if err != nil {
			gocommon.LogWithCtx(ctx).Error("Failed to emit metric", zap.Error(err))
		}
	}
}

type TrackerWorker struct {
	wg      sync.WaitGroup
	sockets sync.Map
}

func (worker *TrackerWorker) LaunchSocket(project model.ATTrackerProject) {
	logger := gocommon.CreateLogger()

	// special case to ignore urlteam
	// TODO(erin) one day I'll make this work
	// 		for now it's just a waste of resources and it'd be silly to have the URLs
	if project.ProjectName == "urlteam2" {
		return
	}

	socketName := GetLogSocketNameFromLeaderboard(project.LeaderboardLink)
	newSocket := TrackerSocket{Project: socketName}
	worker.sockets.Store(project.ProjectName, newSocket)

	go func(project model.ATTrackerProject) {
		defer worker.wg.Done()

		for {
			newSocket.Connect()
			logger.Warn("socket closed, reconnecting", zap.String("project", project.ProjectName))
			time.Sleep(1 * time.Second)
		}
	}(project)
}

func (worker *TrackerWorker) Init() {
	// TODO(erin) actual app, please split this up.
	logger := gocommon.CreateLogger()
	defer func(logger *zap.Logger) {
		err := logger.Sync()
		if err != nil {
			fmt.Printf("Failed to sync logger: %v", err)
		}
	}(logger)

	worker.wg.Add(1)

	gopool.Go(func() {
		defer worker.wg.Done()

		for {
			// fetch the projects every 5 minutes
			projects, err := FetchProjects()
			if err != nil {
				logger.Error("Failed to fetch projects", zap.Error(err))
				time.Sleep(15 * time.Second)
				continue
			}

			logger.Info("got tracker projects", zap.Int("count", len(projects.Projects)))

			// create a new socket for each project we haven't already launched
			for _, project := range projects.Projects {
				if _, ok := worker.sockets.Load(project.ProjectName); !ok {
					worker.wg.Add(1)
					worker.LaunchSocket(project)
				}
			}

			time.Sleep(5 * time.Minute)
		}
	})

	worker.wg.Wait()
}

func TrackerMain() {
	worker := TrackerWorker{}
	worker.Init()
}
