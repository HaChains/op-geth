package pause

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/ethereum/go-ethereum/kclients/util/env"
	"github.com/ethereum/go-ethereum/kclients/util/sig"
	"github.com/ethereum/go-ethereum/log"
)

type Result struct {
	BlockNumber int64 `json:"blockNumber"`
}

var rdb *redis.Client

var (
	addrs      = []string{}
	masterName string
	password   string
	db         int

	chainName string
)

func redisBlockNumber() int64 {
	if pc.testRedisHeight != 0 {
		return pc.testRedisHeight
	}

	if rdb == nil {
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    masterName,
			SentinelAddrs: addrs,
			Password:      password,
			DB:            db,
		})
	}
	ctx := context.Background()
	str, err := rdb.HGet(ctx, "chain_latest:timeline", chainName).Result()
	if err != nil {
		log.Error("### DEBUG ### redis HGet err", "err", err)
		return -1
	}
	var r Result
	err = json.Unmarshal([]byte(str), &r)
	if err != nil {
		log.Error("### DEBUG ### json unmarshall err", "err", err)
		return -1
	}
	return r.BlockNumber - pc.testOffset
}

var pc pauseControl

type pauseControl struct {
	ctx    context.Context
	cancel context.CancelFunc

	started         bool
	exiting         bool
	allowOffset     int64
	testOffset      int64
	testRedisHeight int64
	nextHeight      int64
	redisHeight     int64
	lock            sync.RWMutex
}

func stopBySig() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGKILL, syscall.SIGSTOP)
	<-sigChan
	Stop()
}

func Start() {
	if Started() {
		return
	}
	addrs = env.LoadEnvStrings(env.EnvETLAddrs)
	if len(addrs) == 0 {
		sig.Int("empty etl redis endpoint list")
		return
	}
	masterName = env.LoadEnvString(env.EnvETLMasterName)
	if masterName == "" {
		sig.Int("invalid etl redis master name")
		return
	}
	password = env.LoadEnvStringMute(env.EnvETLPassword)
	if password == "" {
		sig.Int("invalid etl redis password")
		return
	}
	chainName = env.LoadEnvString(env.EnvETLChainName)
	if chainName == "" {
		sig.Int("invalid etl chain name")
		return
	}
	db = env.LoadEnvInt(env.EnvETLDB)
	if db == env.WrongInt {
		return
	}
	pc.allowOffset = env.LoadEnvInt64(env.EnvETLAllowBehind)
	if pc.allowOffset == env.WrongInt {
		return
	}
	pc.testOffset = env.LoadEnvInt64(env.EnvTestOffset)
	if pc.testOffset == env.WrongInt {
		return
	}
	pc.testRedisHeight = env.LoadEnvInt64(env.EnvTestRedisHeight)
	if pc.testRedisHeight == env.WrongInt {
		return
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	pc.ctx = ctx
	pc.cancel = cancelFunc

	go pc.updateLoop()
	log.Info("### DEBUG ### pause control service started")
	pc.started = true
	go stopBySig()
}

func Stop() {
	if !Started() {
		return
	}
	if pc.exiting {
		log.Info("### DEBUG ### exiting")
		return
	}
	pc.exiting = true
	log.Info("### DEBUG ### stopping pause control service")
	pc.cancel()
}

func (c *pauseControl) updateLoop() {
	c.updateBlockHeight()
	for {
		select {
		case <-time.After(time.Second):
			c.updateBlockHeight()
		case <-c.ctx.Done():
			log.Info("### DEBUG ### pauseControl updateLoop stopped")
			return
		}
	}
}

func (c *pauseControl) updateBlockHeight() {
	redisHeight := redisBlockNumber()

	c.lock.Lock()
	if redisHeight != -1 {
		c.redisHeight = redisHeight
	}
	c.lock.Unlock()
}

func Started() bool {
	return pc.started
}

func RedisBehind(l2Num int64) bool {
	return pc.redisBehind(l2Num)
}

func PauseIfBehind(tag string) (shutdown bool) {
	return pc.pauseIfBehind(tag)
}
func (c *pauseControl) redisBehind(l2Num int64) bool {
	if !Started() {
		fmt.Println("### DEBUG ### [pauseControl.redisBehind] service is not started yet")
		Start()
		time.Sleep(5 * time.Second)
	}
	c.lock.RLock()
	if l2Num == 0 {
		l2Num = c.nextHeight
	} else {
		c.nextHeight = l2Num
	}
	var pause = l2Num-c.redisHeight >= c.allowOffset
	c.lock.RUnlock()
	log.Info(fmt.Sprintf("### DEBUG ### l2Height(%d)-redisHeight(%d) = %d >= allowOffset(%d): %v",
		l2Num, c.redisHeight, l2Num-c.redisHeight, c.allowOffset, pause))
	//log.Info("### DEBUG ###", "l2 height from rpc", c.l2Height)
	return pause
}

func (c *pauseControl) pauseIfBehind(tag string) (shutdown bool) {
	stopCh := make(chan struct{})
	for {
		select {
		case <-time.After(1 * time.Second):
			//log.Debug(fmt.Sprintf("### DEBUG ### check redis block height [%s]", tag))
			if !c.redisBehind(0) {
				close(stopCh)
			}
		case <-stopCh:
			log.Info("### DEBUG ### stop pause", "tag", tag)
			return false
		case <-c.ctx.Done():
			if rdb != nil {
				rdb.Close()
			}
			log.Info("### DEBUG ### Pause Control exit", "tag", tag)
			return true
		}
	}
}
