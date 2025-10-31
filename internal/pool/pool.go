package pool

import "sync"

// GlobalPools 全局对象池管理器
// 作用：集中管理所有类型的对象池，提供单例访问
type GlobalPools struct {
	MessagePool    *MessagePool     // 消息对象池
	ConnectionPool *ConnectionPool  // 连接对象池
	ByteBufferPool *ByteBufferPool  // 字节缓冲区池
	ActorPool      *ActorPool       // Actor消息池
}

var (
	globalPools     *GlobalPools
	globalPoolsOnce sync.Once
)

// GetGlobalPools 获取全局对象池管理器（单例）
func GetGlobalPools() *GlobalPools {
	globalPoolsOnce.Do(func() {
		globalPools = &GlobalPools{
			// 初始化各对象池（大小可根据业务调整）
			MessagePool:    NewMessagePool(10000), // 最大10000个消息对象
			ConnectionPool: NewConnectionPool(1000, func() interface{} {
				// 连接池的工厂函数：返回一个实现Resettable的示例对象（实际使用时替换为真实连接类型）
				return &struct{ ID uint64; UserID uint64; SessionID string }{
					// 初始值为空，由Reset()重置
				}
			}),
			ByteBufferPool: NewByteBufferPool(),   // 字节缓冲区池（默认大小配置）
			ActorPool:      NewActorPool(5000),    // 最大5000个Actor消息对象
		}
	})
	return globalPools
}


// PoolStats 单个池的统计信息
type PoolStats struct {
	Name      string // 池名称（如"MessagePool"）
	Size      int    // 总对象数
	Available int    // 空闲对象数
	Created   int64  // 累计创建数
	Gotten    int64  // 累计获取数
	Put       int64  // 累计归还数
}

// GetStats 获取所有池的统计信息
func (gp *GlobalPools) GetStats() []PoolStats {
	var stats []PoolStats

	// 收集消息池统计
	created, gotten, put := gp.MessagePool.Stats()
	stats = append(stats, PoolStats{
		Name:      "MessagePool",
		Size:      gp.MessagePool.Size(),
		Available: gp.MessagePool.Available(),
		Created:   created,
		Gotten:    gotten,
		Put:       put,
	})

	// 收集连接池统计
	created, gotten, put = gp.ConnectionPool.Stats()
	stats = append(stats, PoolStats{
		Name:      "ConnectionPool",
		Size:      gp.ConnectionPool.Size(),
		Available: gp.ConnectionPool.Available(),
		Created:   created,
		Gotten:    gotten,
		Put:       put,
	})

	// 收集Actor池统计
	created, gotten, put = gp.ActorPool.Stats()
	stats = append(stats, PoolStats{
		Name:      "ActorPool",
		Size:      gp.ActorPool.Size(),
		Available: gp.ActorPool.Available(),
		Created:   created,
		Gotten:    gotten,
		Put:       put,
	})

	return stats
}