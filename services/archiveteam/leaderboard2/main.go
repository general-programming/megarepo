package main

import (
	"context"
	"log"

	"github.com/cloudwego/fastpb"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/general-programming/gocommon"
	"github.com/general-programming/megarepo/services/archiveteam/leaderboard2/fastpb_gen"
	"github.com/general-programming/megarepo/services/archiveteam/leaderboard2/model"
	"github.com/go-redis/redis/v9"
	"github.com/hertz-contrib/pprof"
	"github.com/hertz-contrib/websocket"
	"go.uber.org/ratelimit"
	"go.uber.org/zap"
)

var (
	RedisClient *redis.Client
	upgrader    = websocket.HertzUpgrader{
		CheckOrigin: func(r *app.RequestContext) bool {
			// TODO(erin): Check origin
			return true
		},
	}
)

func init() {
	RedisClient = gocommon.NewRedis()
}

func Marshal(msg fastpb.Writer) []byte {
	buf := make([]byte, msg.Size())
	msg.FastWrite(buf)
	return buf
}

type ConnectionHandler struct {
	Connection *websocket.Conn
	Projects   map[string]bool
}

func (c *ConnectionHandler) RedisConsumerWorker(ctx context.Context) (err error) {
	gocommon.LogWithCtx(ctx).Info("Starting up, got redis client")
	rl := ratelimit.New(5)
	last := "$"

	for {
		rl.Take()
		items, err := RedisClient.XRead(ctx, &redis.XReadArgs{
			Streams: []string{"archiveteam.tracker", last},
			Count:   512,
			Block:   5000,
		}).Result()

		if err != nil {
			gocommon.LogWithCtx(ctx).Error("Failed to read from redis", zap.Error(err))
			continue
		}

		for _, item := range items {
			for _, entry := range item.Messages {
				last = entry.ID

				projectString, ok := entry.Values["project"].(string)
				if !ok {
					gocommon.LogWithCtx(ctx).Error("Project is missing")
					continue
				}

				msgString, ok := entry.Values["message"].(string)
				if !ok {
					gocommon.LogWithCtx(ctx).Error(
						"Failed to get message bytes",
						zap.Any("entry", entry),
					)
				}

				msg := fastpb_gen.TrackerEvent{}
				err = gocommon.Unmarshal(msgString, &msg)
				if err != nil {
					gocommon.LogWithCtx(ctx).Error(
						"Failed to unmarshal message",
						zap.String("msg", msgString),
						zap.Error(err),
					)
					continue
				}

				if !c.Projects[projectString] && !c.Projects["all"] {
					continue
				}

				encoded := Marshal(&msg)
				encoded = gocommon.Compress(encoded)
				err = c.Connection.WriteMessage(websocket.BinaryMessage, encoded)

				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						gocommon.LogWithCtx(ctx).Error(
							"Got unexpected error writing",
							zap.Error(err),
						)
					}
					return err
				}
			}
		}
	}
}
func (c *ConnectionHandler) spin(ctx context.Context) {
	go c.RedisConsumerWorker(ctx)

	for {
		_, message, err := c.Connection.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				gocommon.LogWithCtx(ctx).Error(
					"Got unexpected error reading",
					zap.Error(err),
				)
			}
			break
		}

		// attempt to parse json
		c.HandleClientMessage(ctx, message)
	}
}

func (c *ConnectionHandler) HandleClientMessage(ctx context.Context, message []byte) {
	parsed := model.ClientMessage{}
	err := gocommon.Unmarshal(string(message), &parsed)
	if err != nil {
		gocommon.LogWithCtx(ctx).Error(
			"Failed to parse message",
			zap.ByteString("message", message),
			zap.Error(err),
		)
		return
	}

	// handle the message types
	gocommon.Unmarshal(string(message), &parsed.Args)

	switch parsed.Type {
	case "subscribe":
		project, ok := parsed.Args["project"]
		if !ok {
			gocommon.LogWithCtx(ctx).Error("Missing project in subscribe")
			return
		}

		projectString, ok := project.(string)
		if !ok {
			gocommon.LogWithCtx(ctx).Error("Project is not a string")
			return
		}

		gocommon.LogWithCtx(ctx).Debug("Subscribing to project", zap.String("project", projectString))

		c.Projects[projectString] = true
	}
}

func SocketHandler(ctx context.Context, c *app.RequestContext) {
	ctx = gocommon.CtxWithLogger(ctx)

	err := upgrader.Upgrade(c, func(conn *websocket.Conn) {
		connection := ConnectionHandler{
			Connection: conn,
			Projects:   make(map[string]bool),
		}
		connection.spin(ctx)
	})
	if err != nil {
		log.Print("upgrade:", err)
		return
	}
}

func main() {
	h := server.Default()
	pprof.Register(h)

	// https://github.com/cloudwego/hertz/issues/121
	h.NoHijackConnPool = true

	h.GET("/ping", func(c context.Context, ctx *app.RequestContext) {
		ctx.JSON(consts.StatusOK, utils.H{"message": "pong"})
	})

	h.GET("/ws", SocketHandler)

	h.Spin()
}
