package tracecache

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"

	"github.com/ethereum/go-ethereum/kclients/util/env"
	"github.com/ethereum/go-ethereum/kclients/util/sig"
	"github.com/ethereum/go-ethereum/log"
)

type kv struct {
	blockNumber int64
	traceResult []byte
}

type redisCache struct {
	ctx      context.Context
	cancel   context.CancelFunc
	rdb      *redis.Client
	key      string // redis中的key
	size     int64  // redis缓存的大小
	endpoint string
	db       int

	ch chan kv
}

var rc *redisCache
var (
	untilStart chan struct{}
)

func (c *redisCache) send(v kv) error {
	c.rdb.HSet(
		context.TODO(),
		c.key,
		v.blockNumber,
		v.traceResult,
	)

	if v.blockNumber > c.size {
		c.rdb.HDel(
			context.TODO(),
			c.key,
			fmt.Sprint(v.blockNumber-c.size),
		)
	}
	return nil
}

func (c *redisCache) loop() {
	for {
		select {
		case <-c.ctx.Done():
			log.Info("### DEBUG ### rediscache loop exit", "remain", len(c.ch))
			return
		case v := <-c.ch:
			err := c.send(v)
			if err != nil {
				log.Error("### DEBUG ### [redisCache.loop]", "err", err)
			}
		}
	}
}

func Started() bool {
	return rc != nil
}

func Start(ctx context.Context) {
	if Started() {
		return
	}
	untilStart = make(chan struct{})
	cacheSize := env.LoadEnvInt64(env.EnvTraceCacheSize)
	chanSize := env.LoadEnvInt(env.EnvTraceCacheChanSize)
	endpoint := env.LoadEnvString(env.EnvTraceCacheEndpoint)
	db := env.LoadEnvInt(env.EnvTraceCacheDB)
	key := env.LoadEnvString(env.EnvTraceCacheKey)

	if endpoint == "" {
		return
	}
	if cacheSize == env.WrongInt {
		sig.Int(ErrInvalidRedisCacheSize.Error())
		return
	}
	if chanSize == env.WrongInt {
		sig.Int(ErrInvalidRedisCacheChanSize.Error())
		return
	}
	if db == env.WrongInt {
		sig.Int(ErrInvalidRedisDB.Error())
		return
	}
	if key == "" {
		sig.Int(ErrInvalidRedisCacheKey.Error())
		return
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: endpoint,
		DB:   db,
	})
	_ctx, cancel := context.WithCancel(ctx)
	c := &redisCache{
		ctx:      _ctx,
		cancel:   cancel,
		rdb:      rdb,
		key:      key,
		size:     cacheSize,
		endpoint: endpoint,
		db:       db,

		ch: make(chan kv, chanSize),
	}
	close(untilStart)
	go c.loop()
	rc = c
	log.Info("### DEBUG ### redis cache service started")
}

func Stop() {
	if !Started() {
		return
	}
	rc.cancel()
	flush()
}

// flush 终止前将channel中的数据发送完
func flush() {
	if !Started() {
		return
	}
	log.Info("### DEBUG ### [tracecache.Flush]", "remain", len(rc.ch))
	for len(rc.ch) > 0 {
		err := rc.send(<-rc.ch)
		if err != nil {
			log.Error("### DEBUG ### Stop rediscache loop", "err", err)
		}
	}
}

var (
	ErrInvalidRedisDB            = errors.New("invalid redis db")
	ErrInvalidRedisCacheSize     = errors.New("invalid redis cache size")
	ErrInvalidRedisCacheChanSize = errors.New("invalid redis cache channel size")
	ErrInvalidRedisCacheKey      = errors.New("invalid redis cache key")
)

// txTraceResult is the result of a single transaction trace.
type txTraceResult struct {
	Result interface{} `json:"result,omitempty"` // Trace results produced by the tracer
	Error  string      `json:"error,omitempty"`  // Trace failure produced by the tracer
}

func Write(blockNumber int64, traceResult []byte) {
	if !Started() {
		log.Info("### DEBUG ### [rediscache.Write] write before service start", "blockNumber", blockNumber)
		log.Info("### DEBUG ### [rediscache.Write] wait until start")
		Start(context.Background())
		<-untilStart
		log.Info("### DEBUG ### [rediscache.Write] started")
	}
	rc.ch <- kv{blockNumber, traceResult}
}
