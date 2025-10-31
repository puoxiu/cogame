package network

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Connection TCP连接封装体
// 作用：将底层net.Conn与业务属性整合，提供统一操作接口，即每个对象是一个连接对象
type Connection struct {
	ID           uint64        // 连接唯一标识（全局递增，区分不同连接）
	Conn         net.Conn      // 底层TCP连接（Go标准库的net.Conn接口实例）
	UserID       uint64        // 绑定的用户ID（业务字段：如登录后关联用户）
	SessionID    string        // 会话ID（业务字段：如用户登录后的会话标识）
	LastActivity time.Time     // 最后活动时间（用于心跳检测：判断连接是否过期）
	closed       int32         // 连接关闭状态（原子变量：0=未关闭，1=已关闭，保证并发安全）
	writeMutex   sync.Mutex    // 写操作互斥锁（防止多个goroutine同时写数据，避免数据错乱）
	readBuffer   []byte        // 读缓冲区（预分配4096字节，减少内存分配）
	writeBuffer  []byte        // 写缓冲区（预分配4096字节，减少内存分配）
}

// MessageHandler 消息处理器接口
// 作用：定义消息处理的统一规范，具体逻辑由业务层实现（如解析消息、执行业务逻辑）
type MessageHandler interface {
	// HandleMessage 处理单个连接的消息
	// 参数：conn=当前连接，data=解析后的消息体（已去掉长度前缀）
	// 返回值：error=处理过程中的错误（如业务逻辑异常）
	HandleMessage(conn *Connection, data []byte) error
}


// NewConnection 创建新的TCP连接封装体
// 参数：id=连接唯一标识（由TCPServer分配），conn=底层net.Conn实例（Accept()返回的连接）
// 返回值：*Connection=初始化后的连接对象
func NewConnection(id uint64, conn net.Conn) *Connection {
	return &Connection{
		ID:           id,          // 传入全局唯一的连接ID
		Conn:         conn,        // 绑定底层TCP连接
		LastActivity: time.Now(),  // 初始活动时间=创建时间
		readBuffer:   make([]byte, 4096),  // 预分配4KB读缓冲区（减少动态扩容）
		writeBuffer:  make([]byte, 4096),  // 预分配4KB写缓冲区
		// writeMutex默认初始化（sync.Mutex零值可用）
	}
}

// Write 向连接写入数据（线程安全）
// 参数：data=要发送的二进制数据（已包含消息头/长度前缀的完整消息）
// 返回值：error=写入错误（如连接关闭、网络异常）
func (c *Connection) Write(data []byte) error {
	// 用原子操作检查连接是否已关闭（避免加锁开销，快速失败）
	if atomic.LoadInt32(&c.closed) == 1 {
		return fmt.Errorf("connection closed")
	}

	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()

	// 更新最后活动时间（心跳检测会用到，说明连接仍在使用）
	c.LastActivity = time.Now()

	_, err := c.Conn.Write(data)
	return err
}

// Read 从连接读取数据（线程安全）
// 参数：buf=接收数据的缓冲区（由调用者传入，避免内部缓冲区大小限制）
// 返回值：int=实际读取的字节数，error=读取错误（如连接关闭、超时）
func (c *Connection) Read(buf []byte) (int, error) {
	if atomic.LoadInt32(&c.closed) == 1 {
		return 0, fmt.Errorf("connection closed")
	}
	c.LastActivity = time.Now()

	// 调用底层net.Conn.Read()读取数据（阻塞直到有数据或出错）
	return c.Conn.Read(buf)
}

// Close 关闭连接（线程安全）
func (c *Connection) Close() error {
	if atomic.LoadInt32(&c.closed) == 1 {
		return nil
	}
	return c.Conn.Close()
}

// IsClosed 检查连接是否已关闭
func (c *Connection) IsClosed() bool {
	return atomic.LoadInt32(&c.closed) == 1
}

// Reset 重置连接状态（供连接池复用）作用：当Connection对象从连接池取出时，清空旧业务数据和状态，重新绑定新连接
func (c *Connection) Reset() {
	c.UserID = 0  
	c.SessionID = "" 
	c.LastActivity = time.Time{}
	atomic.StoreInt32(&c.closed, 0)
	// 注意：底层Conn字段需在取出连接池后重新绑定（Reset不处理Conn）
}