package pool



// Message 可重用的消息对象
type Message struct {
	Type string  // 消息类型（如"login"/"chat"）
	Data []byte  // 消息数据（对外暴露的字段）
	buf  []byte  // 内部缓冲区（用于存储原始数据，避免Data切片扩容）
}

// Reset 重置消息对象状态（供归还池时调用）
func (m *Message) Reset() {
	m.Type = ""                  // 清空类型
	m.Data = m.Data[:0]          // 清空数据引用（保留底层数组，供下次复用）
	// 若内部缓冲区过大（>4096字节），重新分配小缓冲区（避免内存浪费）
	if len(m.buf) > 4096 {
		m.buf = make([]byte, 0, 1024) // 重置为1KB容量
	} else {
		m.buf = m.buf[:0] // 清空缓冲区，保留容量
	}
}

// SetData 设置消息数据（复用内部缓冲区，减少内存分配）
func (m *Message) SetData(data []byte) {
	// 若缓冲区容量不足，重新分配（确保能容纳data）
	if cap(m.buf) < len(data) {
		m.buf = make([]byte, len(data))
	}
	m.buf = m.buf[:len(data)] // 调整缓冲区长度
	copy(m.buf, data)         // 复制数据到缓冲区
	m.Data = m.buf            // Data指向缓冲区
}

// MessagePool 消息对象池（基于GenericPool）
// 作用：专门管理Message对象的复用
type MessagePool struct {
	*GenericPool // 嵌入GenericPool，复用其Get/Put等方法
}


// NewMessagePool 创建消息池
func NewMessagePool(maxSize int) *MessagePool {
	return &MessagePool{
		GenericPool: NewGenericPool(
			maxSize,
			// 工厂函数：创建新的Message对象（初始化内部缓冲区为1KB）
			func() interface{} {
				return &Message{
					buf: make([]byte, 0, 1024),
				}
			},
			// 重置函数：归还时调用Message.Reset()清空状态
			func(obj interface{}) {
				if msg, ok := obj.(*Message); ok {
					msg.Reset()
				}
			},
		),
	}
}

// GetMessage 获取消息对象（类型安全的封装，避免外部类型断言）
func (p *MessagePool) GetMessage() *Message {
	return p.Get().(*Message) // 调用GenericPool.Get()，并转为*Message
}

// PutMessage 归还消息对象（类型安全的封装）
func (p *MessagePool) PutMessage(msg *Message) {
	p.Put(msg) // 调用GenericPool.Put()
}
