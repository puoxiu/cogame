package actor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
)


// Actor 接口定义
type Actor interface {
	GetID() string										// 返回Actor的唯一ID, 用于ActorSystem 定位
	GetType() string									// 返回Actor的类型, 用于路由区分业务模块
	GetMailboxSize() int								// 返回消息队列（邮箱）的当前消息数，用于监控
	OnReceive(ctx context.Context, msg Message) error	// 消息收发处理核心
	OnStart(ctx context.Context) error					// 	Actor 启动初始化
	OnStop(ctx context.Context) error					// Actor 优雅停止
}

// BaseActor Actor基础实现--注意BaseActor并没有实现Actor接口的所有方法，由具体actor实现
// 1. OnReceive 方法没有实现，因为不同的业务模块有不同的消息处理逻辑
// 2. OnStart 和 OnStop 方法没有实现，因为不同的业务模块有不同的初始化和停止逻辑
type BaseActor struct {
	id        string
	actorType string
	mailbox   chan Message
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	running   bool
	mutex     sync.RWMutex
}

// NewBaseActor 创建基础Actor
func NewBaseActor(id, actorType string, mailboxSize int) *BaseActor {
	ctx, cancel := context.WithCancel(context.Background())
	return &BaseActor{
		id:        id,
		actorType: actorType,
		mailbox:   make(chan Message, mailboxSize),
		ctx:       ctx,
		cancel:    cancel,
		running:   false,
	}
}

func (a *BaseActor) GetID() string {
	return a.id
}

func (a *BaseActor) GetType() string {
	return a.actorType
}

func (a *BaseActor) GetMailboxSize() int {
	return len(a.mailbox)
}

// Start 启动Actor
func (a *BaseActor) Start(actor Actor) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if a.running {
		return fmt.Errorf("actor %s already running", a.id)
	}

	a.running = true
	a.wg.Add(1)

	go a.run(actor)

	return actor.OnStart(a.ctx)
}

// run Actor运行循环
func (a *BaseActor) run(actor Actor) {
	defer a.wg.Done()

	logger.Info(fmt.Sprintf("Actor %s started", a.id))

	for {
		select {
		case msg := <-a.mailbox:
			if err := actor.OnReceive(a.ctx, msg); err != nil {
				logger.Error(fmt.Sprintf("Actor %s handle message error: %v", a.id, err))
			}

		case <-a.ctx.Done():
			logger.Info(fmt.Sprintf("Actor %s stopped", a.id))
			return
		}
	}
}

// Stop 停止Actor
func (a *BaseActor) Stop(actor Actor) error {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if !a.running {
		return nil
	}

	a.running = false
	a.cancel()

	// 等待goroutine结束
	a.wg.Wait()

	return actor.OnStop(a.ctx)
}

// Tell 发送消息到Actor
func (a *BaseActor) Tell(msg Message) error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()

	if !a.running {
		return fmt.Errorf("actor %s is not running", a.id)
	}

	select {
	case a.mailbox <- msg:
		return nil
	case <-time.After(time.Second * 5):
		return fmt.Errorf("mailbox full for actor %s", a.id)
	}
}