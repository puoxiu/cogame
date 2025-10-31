package cache

import (
	"fmt"
	"time"
)

// UserCache 用户缓存
type UserCache struct {
	redis  *RedisManager
	prefix string
	expiry time.Duration
}



// NewUserCache 创建用户缓存
func NewUserCache(redis *RedisManager) *UserCache {
	return &UserCache{
		redis:  redis,
		prefix: "user:",
		expiry: 24 * time.Hour,
	}
}

// SetUserInfo 设置用户信息
func (uc *UserCache) SetUserInfo(userID uint64, info interface{}) error {
	key := fmt.Sprintf("%s%d", uc.prefix, userID)
	return uc.redis.Set(key, info, uc.expiry)
}

// GetUserInfo 获取用户信息
func (uc *UserCache) GetUserInfo(userID uint64, dest interface{}) error {
	key := fmt.Sprintf("%s%d", uc.prefix, userID)
	return uc.redis.GetObject(key, dest)
}

// DeleteUserInfo 删除用户信息
func (uc *UserCache) DeleteUserInfo(userID uint64) error {
	key := fmt.Sprintf("%s%d", uc.prefix, userID)
	return uc.redis.Delete(key)
}

// SetUserOnline 设置用户在线状态
func (uc *UserCache) SetUserOnline(userID uint64, nodeID string) error {
	key := fmt.Sprintf("online:%d", userID)
	return uc.redis.Set(key, nodeID, 30*time.Minute)
}

// GetUserOnline 获取用户在线节点
func (uc *UserCache) GetUserOnline(userID uint64) (string, error) {
	key := fmt.Sprintf("online:%d", userID)
	return uc.redis.GetString(key)
}

// SetUserOffline 设置用户离线
func (uc *UserCache) SetUserOffline(userID uint64) error {
	key := fmt.Sprintf("online:%d", userID)
	return uc.redis.Delete(key)
}
