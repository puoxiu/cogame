package network

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
	"github.com/puoxiu/cogame/internal/pool"
	"github.com/puoxiu/cogame/internal/security"
	"golang.org/x/time/rate"
)

const (
	DefaultBurstPerIP = 5 // 默认每个IP最大突发连接数
	DefaultRatePerIP = rate.Limit(5)  
)

// TCPServer TCP服务器核心类
type TCPServer struct {
	// 网络配置
	address      string
	port         int
	listener     net.Listener

	// 连接管理
	connections  sync.Map      // 存储所有活跃连接（key=connID uint64，value=*Connection）
	connCounter  uint64        // 连接ID计数器（原子递增，保证每个连接ID唯一）
	maxConns     int           // 最大连接数限制（防止连接过载）
	connPool     *pool.ConnectionPool  // 连接池（复用Connection对象，减少GC）

	// 状态与生命周期管理
	running      bool          // 服务器运行状态（true=运行中，false=已停止）
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup

	// 业务与超时配置
	handler      MessageHandler  // 消息处理器（业务层实现）
	readTimeout  time.Duration   // 读超时（防止连接长时间无数据，阻塞Read）
	writeTimeout time.Duration   // 写超时（防止写操作阻塞太久）
	// 限流
	limiter      *security.RateLimitManager    // 限流器
}

// NewTCPServer 创建TCP服务器实例
func NewTCPServer(address string, port int, handler MessageHandler, maxConns int, limiter *security.RateLimitManager) *TCPServer {
	ctx, cancel := context.WithCancel(context.Background())

	// 创建连接池：
	connPool := pool.NewConnectionPool(maxConns, func() interface{} {
		return &Connection{}  // 连接池存储的是空的Connection（后续复用前会Reset并绑定Conn）
	})

	return &TCPServer{
		address:      address,
		port:         port,
		handler:      handler,
		maxConns:     maxConns,
		readTimeout:  30 * time.Second,  // 默认读超时30秒
		writeTimeout: 30 * time.Second,  // 默认写超时30秒
		ctx:          ctx,
		cancel:       cancel,
		connPool:     connPool,
		limiter:      limiter,
		// running默认=false（未启动），connections默认初始化（sync.Map零值可用）
	}
}

// Start 启动TCP服务器
func (s *TCPServer) Start() error {
	logger.Debug(fmt.Sprintf("正在启动 TCP 服务器 %s:%d", s.address, s.port))
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.address, s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on %s:%d: %v", s.address, s.port, err)
	}

	// 2. 初始化服务器状态
	s.listener = listener
	s.running = true

	logger.Info(fmt.Sprintf("TCP server listening on %s:%d", s.address, s.port))

	s.wg.Add(2)
	go s.acceptLoop()     // 启动“接受连接”循环
	go s.heartbeatLoop()  // 启动“心跳检测”循环

	return nil
}

// Stop 停止TCP服务器（优雅关闭）
func (s *TCPServer) Stop() error {
	if !s.running {
		return nil
	}

	s.running = false
	s.cancel()

	if s.listener != nil {
		s.listener.Close()  // 关闭后，listener.Accept()会返回错误
	}

	s.connections.Range(func(key, value interface{}) bool {
		if conn, ok := value.(*Connection); ok {
			conn.Close()  // 关闭连接（会触发handleConnection的defer逻辑，清理连接）
		}
		return true
	})

	s.wg.Wait()
	logger.Info("TCP server stopped")

	return nil
}

// acceptLoop 接受新连接的循环, 不断调用listener.Accept()获取新连接，初始化后交给handleConnection处理
func (s *TCPServer) acceptLoop() {
	defer s.wg.Done()

	for s.running {
		conn, err := s.listener.Accept()
		remoteAddr := conn.RemoteAddr().String()
		logger.Debug(fmt.Sprintf("新的连接 %s", remoteAddr))
		if clientIP, _, err := net.SplitHostPort(remoteAddr); err == nil {
			// 检查是否超出限流阈值, 限制单IP最大连接数
			if !s.limiter.CheckLimit("conn"+clientIP, DefaultRatePerIP, DefaultBurstPerIP) {
				logger.Warn(fmt.Sprintf("IP %s exceeds max connections (%d), closing new connection", clientIP, DefaultBurstPerIP))
            	conn.Close()
				continue
			}
		}

		if err != nil {
			if s.running {
				logger.Error(fmt.Sprintf("Accept error: %v", err))
			}
			continue
		}

		if s.GetConnectionCount() >= s.maxConns {
			logger.Warn("Max connections reached, closing new connection")
			conn.Close()  // 拒绝新连接，直接关闭
			continue
		}

		connID := atomic.AddUint64(&s.connCounter, 1)

		poolObj := s.connPool.Get()
		connection, ok := poolObj.(*Connection)
		if !ok {
			// 连接池对象类型错误，降级为直接创建新对象（容错处理）
			logger.Error("Failed to get Connection from pool, creating new one")
			connection = NewConnection(connID, conn)
		} else {
			// 重置连接状态（清空旧数据），并绑定新的底层Conn
			connection.Reset()
			connection.ID = connID
			connection.Conn = conn
			connection.LastActivity = time.Now()
		}

		s.connections.Store(connID, connection)
		logger.Debug(fmt.Sprintf("新的连接2 %d %s", connID, remoteAddr))

		s.wg.Add(1)
		go s.handleConnection(connection)
	}
}

// handleConnection 处理单个连接的消息（独立goroutine运行）
func (s *TCPServer) handleConnection(conn *Connection) {
	defer func() {
		conn.Close()                          // 1. 关闭底层连接
		s.connections.Delete(conn.ID)         // 2. 从活跃连接列表中移除
		s.connPool.Put(conn)                  // 3. 将Connection对象归还连接池（供复用）
		logger.Debug(fmt.Sprintf("连接 %d 已关闭", conn.ID))  // 4. 输出关闭日志
		s.wg.Done()                           // 5. 通知WaitGroup：该连接的goroutine已完成
	}()

	conn.Conn.SetReadDeadline(time.Now().Add(s.readTimeout))
	conn.Conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))

	logger.Debug(fmt.Sprintf("开始处理连接 %d", conn.ID))
	for !conn.IsClosed() && s.running {
		// 读取消息长度前缀（4字节，大端序：表示后续消息体的字节数）-- 防止粘包
		lengthBuf := make([]byte, 4)  // 4字节足够存储最大1MB的消息（1MB=1024*1024=1048576，4字节最大可表示4GB）
		if _, err := conn.Read(lengthBuf); err != nil {
			// 只有连接未关闭时，才输出错误（正常关闭的错误无需关注）
			if !conn.IsClosed() {
				logger.Debug(fmt.Sprintf("连接 %d 读取长度失败: %v", conn.ID, err))
			}
			break  // 读取长度失败，退出循环（触发defer清理）
		}

		// 解析消息长度（大端序转uint32：字节序统一，避免跨平台问题）
		// 原理：lengthBuf[0]是最高位（2^24），lengthBuf[3]是最低位（2^0）
		msgLen := uint32(lengthBuf[0])<<24 | uint32(lengthBuf[1])<<16 | uint32(lengthBuf[2])<<8 | uint32(lengthBuf[3])

		logger.Debug(fmt.Sprintf("连接 %d 读取到消息长度 %d", conn.ID, msgLen))
		
		// 4. 检查消息长度合法性（防止恶意攻击：如超大消息耗尽内存）
		if msgLen == 0 || msgLen > 1024*1024 {  // 最小0字节（非法），最大1MB
			logger.Warn(fmt.Sprintf("Invalid message length %d for connection %d", msgLen, conn.ID))
			break  // 消息非法，关闭连接
		}

		// 5. 读取消息体（按解析出的长度读取，确保完整读取一条消息）
		msgBuf := make([]byte, msgLen)  // 创建对应长度的缓冲区
		if _, err := conn.Read(msgBuf); err != nil {
			logger.Debug(fmt.Sprintf("Read message error for connection %d: %v", conn.ID, err))
			break
		}

		// 调用业务处理器处理消息（解耦：服务器不关心具体业务逻辑）
		if err := s.handler.HandleMessage(conn, msgBuf); err != nil {
			logger.Error(fmt.Sprintf("Handle message error for connection %d: %v", conn.ID, err))
			// 业务错误是否关闭连接？这里不关闭，由业务处理器决定（如登录失败不关闭，权限错误关闭）
		}

		// 7. 更新读写超时（每次处理完消息后，延长超时时间，避免正常连接被误判为超时）
		conn.Conn.SetReadDeadline(time.Now().Add(s.readTimeout))
		conn.Conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	}
}


// heartbeatLoop 心跳检测循环：定期清理过期连接（长时间无活动的连接），释放资源
func (s *TCPServer) heartbeatLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:  // 定时器触发，执行心跳检测
			now := time.Now()
			var expiredConns []uint64

			// 1. 遍历所有活跃连接，检查是否过期
			s.connections.Range(func(key, value interface{}) bool {
				connID := key.(uint64)
				conn, ok := value.(*Connection)
				if !ok {
					logger.Error(fmt.Sprintf("Invalid connection type for connID %d", connID))
					return true
				}

				// 检查最后活动时间：当前时间 - 最后活动时间 > 60秒 → 过期
				if now.Sub(conn.LastActivity) > 60*time.Second {
					expiredConns = append(expiredConns, connID)
				}
				return true  // 继续遍历下一个连接
			})

			// 2. 关闭所有过期连接（遍历过期列表，调用Close()）
			for _, connID := range expiredConns {
				if value, ok := s.connections.Load(connID); ok {
					if conn, ok := value.(*Connection); ok {
						logger.Debug(fmt.Sprintf("Closing expired connection %d", connID))
						conn.Close()  // 关闭连接（触发handleConnection的defer清理）
					}
				}
			}

		case <-s.ctx.Done():  // 服务器停止（cancel()被调用），退出循环
			return
		}
	}
}

// GetConnection 根据连接ID获取连接
func (s *TCPServer) GetConnection(connID uint64) (*Connection, bool) {
	value, ok := s.connections.Load(connID)
	if !ok {
		return nil, false
	}
	// 类型断言：将value转为*Connection（因为sync.Map的值是interface{}）
	return value.(*Connection), true
}

// GetConnectionByUserID 根据用户ID获取连接（假设一个用户只绑定一个连接）
func (s *TCPServer) GetConnectionByUserID(userID uint64) (*Connection, bool) {
	var result *Connection

	s.connections.Range(func(key, value interface{}) bool {
		conn, ok := value.(*Connection)
		if ok && conn.UserID == userID {
			result = conn
			return false
		}
		return true  // 未找到，继续遍历
	})

	return result, result != nil
}


// GetConnectionCount 获取当前活跃连接数
func (s *TCPServer) GetConnectionCount() int {
	count := 0
	s.connections.Range(func(key, value interface{}) bool {
		count++
		return true 
	})
	return count
}


// Broadcast 向所有活跃连接广播消息
// 参数：data=要广播的二进制消息（已包含长度前缀的完整消息）
// 注意：不阻塞，每个连接的Write()独立，失败不影响其他连接
func (s *TCPServer) Broadcast(data []byte) {
	s.connections.Range(func(key, value interface{}) bool {
		conn, ok := value.(*Connection)
		if ok && !conn.IsClosed() {
			go func(c *Connection) {
				if err := c.Write(data); err != nil {
					logger.Warn(fmt.Sprintf("Broadcast to conn %d failed: %v", c.ID, err))
				}
			}(conn)
		}
		return true  // 继续遍历所有连接
	})
}

// SendToUser 向特定用户发送消息
// 参数：userID=目标用户ID，data=要发送的二进制消息（已包含长度前缀）
func (s *TCPServer) SendToUser(userID uint64, data []byte) error {
	conn, ok := s.GetConnectionByUserID(userID)
	if !ok {
		return fmt.Errorf("user %d not connected", userID)
	}

	return conn.Write(data)
}



