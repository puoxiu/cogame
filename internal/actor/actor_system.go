package actor

import (
	"context"
	"fmt"
	"sync"

	"github.com/puoxiu/cogame/internal/logger"
)

// ActorSystem Actor系统
type ActorSystem struct {
	actors map[string]Actor
	mutex  sync.RWMutex
	name   string
	ctx    context.Context
	cancel context.CancelFunc
}

// NewActorSystem 创建Actor系统
func NewActorSystem(name string) *ActorSystem {
	ctx, cancel := context.WithCancel(context.Background())
	return &ActorSystem{
		actors: make(map[string]Actor),
		name:   name,
		ctx:    ctx,
		cancel: cancel,
	}
}

// SpawnActor 创建Actor
func (sys *ActorSystem) SpawnActor(actor Actor) error {
	sys.mutex.Lock()
	defer sys.mutex.Unlock()

	id := actor.GetID()
	if _, exists := sys.actors[id]; exists {
		return fmt.Errorf("actor %s already exists", id)
	}

	// 启动Actor
	if err := actor.OnStart(sys.ctx); err != nil {
		return err
	}

	sys.actors[id] = actor
	logger.Info(fmt.Sprintf("Actor %s spawned", id))

	return nil
}

// GetActor 获取Actor
func (sys *ActorSystem) GetActor(id string) (Actor, bool) {
	sys.mutex.RLock()
	defer sys.mutex.RUnlock()

	actor, exists := sys.actors[id]
	return actor, exists
}

// Tell 向Actor发送消息
func (sys *ActorSystem) Tell(actorID string, msg Message) error {
	actor, exists := sys.GetActor(actorID)
	if !exists {
		return fmt.Errorf("actor %s not found", actorID)
	}

	return actor.OnReceive(sys.ctx, msg)
}

// Shutdown 关闭Actor系统
func (sys *ActorSystem) Shutdown() error {
	sys.mutex.Lock()
	defer sys.mutex.Unlock()

	logger.Info("Shutting down actor system")

	// 停止所有Actor
	for id, actor := range sys.actors {
		if err := actor.OnStop(sys.ctx); err != nil {
			logger.Error(fmt.Sprintf("Error stopping actor %s: %v", id, err))
		}
	}

	sys.cancel()
	sys.actors = make(map[string]Actor)

	return nil
}

// GetActorCount 获取Actor数量
func (sys *ActorSystem) GetActorCount() int {
	sys.mutex.RLock()
	defer sys.mutex.RUnlock()

	return len(sys.actors)
}

// ListActors 列出所有Actor
func (sys *ActorSystem) ListActors() []string {
	sys.mutex.RLock()
	defer sys.mutex.RUnlock()

	var actors []string
	for id := range sys.actors {
		actors = append(actors, id)
	}

	return actors
}
