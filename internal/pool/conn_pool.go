package pool

// Resettable 可重置接口（所有需要放入ConnectionPool的对象必须实现）
// 作用：统一重置方法，确保连接对象归还时状态干净
type Resettable interface {
	Reset() // 重置对象状态（如清空用户ID、会话ID等）
}

// ConnectionPool 连接对象池（基于GenericPool）
type ConnectionPool struct {
	*GenericPool
}

// NewConnectionPool 创建连接池
func NewConnectionPool(maxSize int, factory func() interface{}) *ConnectionPool {
	return &ConnectionPool{
		GenericPool: NewGenericPool(
			maxSize,
			factory,
			// 重置函数：调用对象的Reset()方法（前提是对象实现了Resettable）
			func(obj interface{}) {
				if resettable, ok := obj.(Resettable); ok {
					resettable.Reset()
				}
			},
		),
	}
}

// GetConnection 获取连接对象（类型安全封装）
func (p *ConnectionPool) GetConnection() interface{} {
	return p.Get()
}

// PutConnection 归还连接对象（类型安全封装）
func (p *ConnectionPool) PutConnection(conn interface{}) {
	p.Put(conn)
}