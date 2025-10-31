package pool


// ActorMessage Actor模型中的消息对象
type ActorMessage struct {
	ID       string            // 消息唯一标识
	Type     string            // 消息类型
	From     string            // 发送者
	To       string            // 接收者
	Data     []byte            // 消息数据
	Callback func(interface{}, error) // 回调函数（处理结果返回）
	buf      []byte            // 内部缓冲区（复用数据存储）
}

// Reset 重置Actor消息状态
func (am *ActorMessage) Reset() {
	am.ID = ""
	am.Type = ""
	am.From = ""
	am.To = ""
	am.Data = nil
	am.Callback = nil
	am.buf = am.buf[:0] // 清空缓冲区
}

// SetData 设置消息数据（复用内部缓冲区）
func (am *ActorMessage) SetData(data []byte) {
	if cap(am.buf) < len(data) {
		am.buf = make([]byte, len(data))
	}
	am.buf = am.buf[:len(data)]
	copy(am.buf, data)
	am.Data = am.buf
}

// ActorPool Actor消息对象池
type ActorPool struct {
	*GenericPool
}

// NewActorPool 创建Actor消息池
func NewActorPool(maxSize int) *ActorPool {
	return &ActorPool{
		GenericPool: NewGenericPool(
			maxSize,
			// 工厂函数：创建带512B缓冲区的ActorMessage
			func() interface{} {
				return &ActorMessage{
					buf: make([]byte, 0, 512),
				}
			},
			func(obj interface{}) {
				if msg, ok := obj.(*ActorMessage); ok {
					msg.Reset()
				}
			},
		),
	}
}

// GetActorMessage 获取Actor消息对象
func (p *ActorPool) GetActorMessage() *ActorMessage {
	return p.Get().(*ActorMessage)
}

// PutActorMessage 归还Actor消息对象
func (p *ActorPool) PutActorMessage(msg *ActorMessage) {
	p.Put(msg)
}