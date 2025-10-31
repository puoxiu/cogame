package cache

import (
	"encoding/json"
	"fmt"
	"time"

	"context"
	"sync"

	"github.com/go-redis/redis/v8"
	"github.com/puoxiu/cogame/internal/logger"
)

type RedisConfig struct {
	// 通用配置
	PoolSize     int           `mapstructure:"pool_size"`
	MaxRetries   int           `mapstructure:"max_retries"`
	DialTimeout  time.Duration `mapstructure:"dial_timeout"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout"`

	// 单机模式配置
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`

	// 集群模式配置
	ClusterMode  bool     `mapstructure:"cluster_mode"`
	ClusterAddrs []string `mapstructure:"cluster_addrs"`

	// 哨兵模式配置
	SentinelMode   bool     `mapstructure:"sentinel_mode"`
	SentinelAddrs  []string `mapstructure:"sentinel_addrs"`
	SentinelMaster string   `mapstructure:"sentinel_master"`

	// 集群特有配置
	MaxRedirects   int  `mapstructure:"max_redirects"`
	ReadOnly       bool `mapstructure:"read_only"`
	RouteByLatency bool `mapstructure:"route_by_latency"`
	RouteRandomly  bool `mapstructure:"route_randomly"`
}

// RedisManager Redis管理器
type RedisManager struct {
	client         redis.Cmdable 			// 可以是Client、ClusterClient或SentinelClient
	clusterClient  *redis.ClusterClient
	sentinelClient *redis.Client
	config         *RedisConfig
	ctx            context.Context
	mutex          sync.RWMutex
	mode           string 					// "single", "cluster", "sentinel"
}


// NewRedisManager 创建Redis管理器
func NewRedisManager(config *RedisConfig) (*RedisManager, error) {
	ctx := context.Background()

	manager := &RedisManager{
		config: config,
		ctx:    ctx,
	}

	var err error

	// 根据配置选择Redis模式
	if config.ClusterMode {
		manager.mode = "cluster"
		err = manager.initClusterMode()
	} else if config.SentinelMode {
		manager.mode = "sentinel"
		err = manager.initSentinelMode()
	} else {
		manager.mode = "single"
		err = manager.initSingleMode()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize redis: %v", err)
	}

	// 测试连接
	if err := manager.client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %v", err)
	}

	logger.Infof("Redis connected in %s mode", manager.mode)
	return manager, nil
}

// initSingleMode 初始化单机模式
func (rm *RedisManager) initSingleMode() error {
	client := redis.NewClient(&redis.Options{
		Addr:         rm.config.Addr,
		Password:     rm.config.Password,
		DB:           rm.config.DB,
		PoolSize:     rm.config.PoolSize,
		MaxRetries:   rm.config.MaxRetries,
		DialTimeout:  rm.config.DialTimeout,
		ReadTimeout:  rm.config.ReadTimeout,
		WriteTimeout: rm.config.WriteTimeout,
		IdleTimeout:  rm.config.IdleTimeout,
	})

	rm.client = client
	logger.Infof("Redis single mode initialized: %s", rm.config.Addr)
	return nil
}

// initClusterMode 初始化集群模式--todo
func (rm *RedisManager) initClusterMode() error {
	return nil
}

// initSentinelMode 初始化哨兵模式--todo
func (rm *RedisManager) initSentinelMode() error {
	return nil
}

// GetClient 获取Redis客户端
func (rm *RedisManager) GetClient() redis.Cmdable {
	return rm.client
}

// GetClusterClient 获取集群客户端 -- todo
// func (rm *RedisManager) GetClusterClient() *redis.ClusterClient {
// 	return rm.clusterClient
// }

// GetMode 获取Redis模式
func (rm *RedisManager) GetMode() string {
	return rm.mode
}


// Close 关闭Redis连接
func (rm *RedisManager) Close() error {
	switch rm.mode {
	case "cluster":
		if rm.clusterClient != nil {
			return rm.clusterClient.Close()
		}
	case "sentinel":
		if rm.sentinelClient != nil {
			return rm.sentinelClient.Close()
		}
	default:
		if client, ok := rm.client.(*redis.Client); ok {
			return client.Close()
		}
	}
	return nil
}

// Delete 删除键, 命令 del key
func (rm *RedisManager) Delete(keys ...string) error {
	return rm.client.Del(rm.ctx, keys...).Err()
}

// Exists 检查键是否存在, 命令 exists key
func (rm *RedisManager) Exists(key string) (bool, error) {
	count, err := rm.client.Exists(rm.ctx, key).Result()
	return count > 0, err
}

// Expire 设置过期时间, 命令 expire key expiration
func (rm *RedisManager) Expire(key string, expiration time.Duration) error {
	return rm.client.Expire(rm.ctx, key, expiration).Err()
}

// TTL 获取TTL, 命令 ttl key
func (rm *RedisManager) TTL(key string) (time.Duration, error) {
	return rm.client.TTL(rm.ctx, key).Result()
}

// -----------------//
// string 操作
// -----------------//

// Set 设置键值对, 命令 set key value ex expiration
func (rm *RedisManager) Set(key string, value interface{}, expiration time.Duration) error {
	var data []byte
	var err error

	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		data, err = json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal value: %v", err)
		}
	}

	return rm.client.Set(rm.ctx, key, data, expiration).Err()
}

// Get 获取值, 命令 get key
func (rm *RedisManager) Get(key string) ([]byte, error) {
	result, err := rm.client.Get(rm.ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("key not found")
		}
		return nil, err
	}
	return []byte(result), nil
}

// GetString 获取字符串值
func (rm *RedisManager) GetString(key string) (string, error) {
	return rm.client.Get(rm.ctx, key).Result()
}

// GetObject 获取对象
func (rm *RedisManager) GetObject(key string, dest interface{}) error {
	data, err := rm.Get(key)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// Incr 递增, 命令 incr key
func (rm *RedisManager) Incr(key string) (int64, error) {
	return rm.client.Incr(rm.ctx, key).Result()
}

// IncrBy 递增指定值, 命令 incrby key value
func (rm *RedisManager) IncrBy(key string, value int64) (int64, error) {
	return rm.client.IncrBy(rm.ctx, key, value).Result()
}

// -----------------//
// hash 操作
// -----------------//

// HSet 设置哈希字段值, 命令 hset key field value
func (rm *RedisManager) HSet(key, field string, value interface{}) error {
	return rm.client.HSet(rm.ctx, key, field, value).Err()
}

// HGet 获取哈希字段值, 命令 hget key field
func (rm *RedisManager) HGet(key, field string) (string, error) {
	return rm.client.HGet(rm.ctx, key, field).Result()
}

// HGetAll 获取哈希所有字段值, 命令 hgetall key
func (rm *RedisManager) HGetAll(key string) (map[string]string, error) {
	return rm.client.HGetAll(rm.ctx, key).Result()
}

// HDel 删除哈希字段, 命令 hdel key field
func (rm *RedisManager) HDel(key string, fields ...string) error {
	return rm.client.HDel(rm.ctx, key, fields...).Err()
}

// HExists 检查哈希字段是否存在, 命令 hexists key field
func (rm *RedisManager) HExists(key, field string) (bool, error) {
	return rm.client.HExists(rm.ctx, key, field).Result()
}

// -----------------//
// list 操作
// -----------------//

// LPush 从左侧插入值, 命令 lpush key value
func (rm *RedisManager) LPush(key string, values ...interface{}) error {
	return rm.client.LPush(rm.ctx, key, values...).Err()
}

// RPush 从右侧插入值, 命令 rpush key value
func (rm *RedisManager) RPush(key string, values ...interface{}) error {
	return rm.client.RPush(rm.ctx, key, values...).Err()
}

// LPop 从左侧弹出值, 命令 lpop key
func (rm *RedisManager) LPop(key string) (string, error) {
	return rm.client.LPop(rm.ctx, key).Result()
}

// RPop 从右侧弹出值, 命令 rpop key
func (rm *RedisManager) RPop(key string) (string, error) {
	return rm.client.RPop(rm.ctx, key).Result()
}

// LLen 获取列表长度, 命令 llen key
func (rm *RedisManager) LLen(key string) (int64, error) {
	return rm.client.LLen(rm.ctx, key).Result()
}

// LRange 获取列表范围, 命令 lrange key start stop
func (rm *RedisManager) LRange(key string, start, stop int64) ([]string, error) {
	return rm.client.LRange(rm.ctx, key, start, stop).Result()
}

// -----------------//
// set 操作
// -----------------//

// SAdd 添加成员, 命令 sadd key member
func (rm *RedisManager) SAdd(key string, members ...interface{}) error {
	return rm.client.SAdd(rm.ctx, key, members...).Err()
}

// SRem 删除成员, 命令 srem key member
func (rm *RedisManager) SRem(key string, members ...interface{}) error {
	return rm.client.SRem(rm.ctx, key, members...).Err()
}

// SMembers 获取所有成员, 命令 smembers key
func (rm *RedisManager) SMembers(key string) ([]string, error) {
	return rm.client.SMembers(rm.ctx, key).Result()
}

// SIsMember 检查成员是否存在, 命令 sismember key member
func (rm *RedisManager) SIsMember(key string, member interface{}) (bool, error) {
	return rm.client.SIsMember(rm.ctx, key, member).Result()
}

// SCard 获取集合大小, 命令 scard key
func (rm *RedisManager) SCard(key string) (int64, error) {
	return rm.client.SCard(rm.ctx, key).Result()
}


// ZSet操作-----------------//
// zset 操作
// -----------------//

// ZAdd 添加成员, 命令 zadd key score member
func (rm *RedisManager) ZAdd(key string, members ...*redis.Z) error {
	return rm.client.ZAdd(rm.ctx, key, members...).Err()
}

// ZRem 删除成员, 命令 zrem key member
func (rm *RedisManager) ZRem(key string, members ...interface{}) error {
	return rm.client.ZRem(rm.ctx, key, members...).Err()
}

// ZRange 获取有序集合范围, 命令 zrange key start stop
func (rm *RedisManager) ZRange(key string, start, stop int64) ([]string, error) {
	return rm.client.ZRange(rm.ctx, key, start, stop).Result()
}

// ZRangeWithScores 获取有序集合范围及分数, 命令 zrange key start stop withscores
func (rm *RedisManager) ZRangeWithScores(key string, start, stop int64) ([]redis.Z, error) {
	return rm.client.ZRangeWithScores(rm.ctx, key, start, stop).Result()
}

// ZRevRange 获取有序集合范围（逆序）, 命令 zrevrange key start stop
func (rm *RedisManager) ZRevRange(key string, start, stop int64) ([]string, error) {
	return rm.client.ZRevRange(rm.ctx, key, start, stop).Result()
}

// ZScore 获取成员分数, 命令 zscore key member
func (rm *RedisManager) ZScore(key, member string) (float64, error) {
	return rm.client.ZScore(rm.ctx, key, member).Result()
}

// ZCard 获取有序集合大小, 命令 zcard key
func (rm *RedisManager) ZCard(key string) (int64, error) {
	return rm.client.ZCard(rm.ctx, key).Result()
}

// Pipeline操作-----------------//
// Pipeline 可以批量执行多个命令, 命令 pipelined
func (rm *RedisManager) Pipeline() redis.Pipeliner {
	return rm.client.Pipeline()
}

// Transaction操作-----------------//
// TxPipeline 开启事务, 命令 multi
func (rm *RedisManager) TxPipeline() redis.Pipeliner {
	return rm.client.TxPipeline()
}

// todo 分布式锁的实现 用lua + uuid
// Lock 分布式锁（底层命令：SET key value NX EX expiration）
// 若 key 不存在则加锁并设置过期时间；存在则返回 false。
func (rm *RedisManager) Lock(key string, expiration time.Duration) (bool, error) {
	lockKey := fmt.Sprintf("lock:%s", key)
	result := rm.client.SetNX(rm.ctx, lockKey, "1", expiration)
	return result.Result()
}

// Unlock 释放分布式锁, 命令 del key
func (rm *RedisManager) Unlock(key string) error {
	lockKey := fmt.Sprintf("lock:%s", key)
	return rm.client.Del(rm.ctx, lockKey).Err()
}

//--------------------//
// 基于redis实现广播 / 发布订阅
//-------------------//

// Publish 发布消息到指定频道, 命令 publish channel message
func (rm *RedisManager) Publish(channel string, message interface{}) error {
	return rm.client.Publish(rm.ctx, channel, message).Err()
}

// Subscribe 订阅指定频道, 命令 subscribe channel
func (rm *RedisManager) Subscribe(channels ...string) *redis.PubSub {
	switch rm.mode {
	case "cluster":
		if rm.clusterClient != nil {
			return rm.clusterClient.Subscribe(rm.ctx, channels...)
		}
	case "sentinel":
		if rm.sentinelClient != nil {
			return rm.sentinelClient.Subscribe(rm.ctx, channels...)
		}
	default:
		if client, ok := rm.client.(*redis.Client); ok {
			return client.Subscribe(rm.ctx, channels...)
		}
	}
	return nil
}

// PSubscribe 订阅匹配模式（通配符）的频道, 命令 psubscribe pattern
func (rm *RedisManager) PSubscribe(patterns ...string) *redis.PubSub {
	switch rm.mode {
	case "cluster":
		if rm.clusterClient != nil {
			return rm.clusterClient.PSubscribe(rm.ctx, patterns...)
		}
	case "sentinel":
		if rm.sentinelClient != nil {
			return rm.sentinelClient.PSubscribe(rm.ctx, patterns...)
		}
	default:
		if client, ok := rm.client.(*redis.Client); ok {
			return client.PSubscribe(rm.ctx, patterns...)
		}
	}
	return nil
}
