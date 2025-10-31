package mq

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/puoxiu/cogame/internal/logger"
)

// 消息类型常量
const (
	// 游戏事件
	MSG_GAME_ROOM_CREATED  = "game_room_created"
	MSG_GAME_ROOM_JOINED   = "game_room_joined"
	MSG_GAME_ROOM_LEFT     = "game_room_left"
	MSG_GAME_STARTED       = "game_started"
	MSG_GAME_ENDED         = "game_ended"
	MSG_PLAYER_ACTION      = "player_action"
	MSG_GAME_STATE_CHANGED = "game_state_changed"

	// 聊天频道
	CHAT_CHANNEL_WORLD  = 1 // 世界聊天
	CHAT_CHANNEL_ROOM   = 2 // 房间聊天
	CHAT_CHANNEL_FRIEND = 3 // 好友聊天
	CHAT_CHANNEL_GUILD  = 4 // 公会聊天

	// 系统命令
	SYS_CMD_RELOAD_CONFIG    = "reload_config"
	SYS_CMD_UPDATE_LOAD      = "update_load"
	SYS_CMD_SHUTDOWN         = "shutdown"
	SYS_CMD_HOT_UPDATE       = "hot_update"
	SYS_CMD_KICK_USER        = "kick_user"
	SYS_CMD_BROADCAST_NOTICE = "broadcast_notice"
)



// MessageBroker 消息代理
type MessageBroker struct {
	nsq    *NSQManager
	nodeID string
}

// NewMessageBroker 创建消息代理
func NewMessageBroker(nsq *NSQManager, nodeID string) *MessageBroker {
	return &MessageBroker{
		nsq:    nsq,
		nodeID: nodeID,
	}
}

// PublishGameMessage 发布游戏消息
func (mb *MessageBroker) PublishGameMessage(msgType string, roomID, userID uint64, data map[string]interface{}) error {
	msg := NewGameMessage(msgType, roomID, userID, data)
	return mb.nsq.PublishJSON("game_events", msg)
}

// PublishChatMessage 发布聊天消息
func (mb *MessageBroker) PublishChatMessage(fromUserID, toUserID uint64, channel int32, content string) error {
	msg := NewChatMessage(fromUserID, toUserID, channel, content)
	return mb.nsq.PublishJSON("chat_messages", msg)
}

// PublishSystemMessage 发布系统消息
func (mb *MessageBroker) PublishSystemMessage(msgType, target, command string, args map[string]interface{}) error {
	msg := NewSystemMessage(msgType, target, command, args)
	return mb.nsq.PublishJSON("system_messages", msg)
}

// BroadcastSystemMessage 广播系统消息
func (mb *MessageBroker) BroadcastSystemMessage(command string, args map[string]interface{}) error {
	return mb.PublishSystemMessage("broadcast", "", command, args)
}

// SendToNode 发送消息到指定节点
func (mb *MessageBroker) SendToNode(target, command string, args map[string]interface{}) error {
	return mb.PublishSystemMessage("unicast", target, command, args)
}

// SubscribeGameEvents 订阅游戏事件
func (mb *MessageBroker) SubscribeGameEvents(handler *GameMessageHandler) error {
	return mb.nsq.Subscribe("game_events", mb.nodeID, handler)
}

// SubscribeChatMessages 订阅聊天消息
func (mb *MessageBroker) SubscribeChatMessages(handler *ChatMessageHandler) error {
	return mb.nsq.Subscribe("chat_messages", mb.nodeID, handler)
}

// SubscribeSystemMessages 订阅系统消息
func (mb *MessageBroker) SubscribeSystemMessages(handler *SystemMessageHandler) error {
	return mb.nsq.Subscribe("system_messages", mb.nodeID, handler)
}


// SystemMessage 系统消息
type SystemMessage struct {
	Type      string                 `json:"type"`
	Target    string                 `json:"target,omitempty"` // 目标节点ID，空表示广播
	Command   string                 `json:"command"`
	Args      map[string]interface{} `json:"args,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// NewSystemMessage 创建系统消息
func NewSystemMessage(msgType, target, command string, args map[string]interface{}) *SystemMessage {
	return &SystemMessage{
		Type:      msgType,
		Target:    target,
		Command:   command,
		Args:      args,
		Timestamp: time.Now().Unix(),
	}
}

// SystemMessageHandler 系统消息处理器
type SystemMessageHandler struct {
	nodeID   string
	handlers map[string]func(*SystemMessage) error
	mutex    sync.RWMutex
}

// NewSystemMessageHandler 创建系统消息处理器
func NewSystemMessageHandler(nodeID string) *SystemMessageHandler {
	return &SystemMessageHandler{
		nodeID:   nodeID,
		handlers: make(map[string]func(*SystemMessage) error),
	}
}

// RegisterHandler 注册命令处理器
func (smh *SystemMessageHandler) RegisterHandler(command string, handler func(*SystemMessage) error) {
	smh.mutex.Lock()
	defer smh.mutex.Unlock()

	smh.handlers[command] = handler
	logger.Debug(fmt.Sprintf("Registered system command handler: %s", command))
}

// HandleMessage 处理消息
func (smh *SystemMessageHandler) HandleMessage(topic, channel string, data []byte) error {
	var sysMsg SystemMessage
	if err := json.Unmarshal(data, &sysMsg); err != nil {
		return fmt.Errorf("failed to unmarshal system message: %v", err)
	}

	// 检查消息是否针对当前节点
	if sysMsg.Target != "" && sysMsg.Target != smh.nodeID {
		return nil // 不是发给当前节点的消息
	}

	smh.mutex.RLock()
	handler, exists := smh.handlers[sysMsg.Command]
	smh.mutex.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("No handler for system command: %s", sysMsg.Command))
		return nil
	}

	return handler(&sysMsg)
}

// GameMessage 游戏消息
type GameMessage struct {
	Type      string                 `json:"type"`
	RoomID    uint64                 `json:"room_id,omitempty"`
	UserID    uint64                 `json:"user_id,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp int64                  `json:"timestamp"`
}

// NewGameMessage 创建游戏消息
func NewGameMessage(msgType string, roomID, userID uint64, data map[string]interface{}) *GameMessage {
	return &GameMessage{
		Type:      msgType,
		RoomID:    roomID,
		UserID:    userID,
		Data:      data,
		Timestamp: time.Now().Unix(),
	}
}

// GameMessageHandler 游戏消息处理器
type GameMessageHandler struct {
	handlers map[string]func(*GameMessage) error
	mutex    sync.RWMutex
}

// NewGameMessageHandler 创建游戏消息处理器
func NewGameMessageHandler() *GameMessageHandler {
	return &GameMessageHandler{
		handlers: make(map[string]func(*GameMessage) error),
	}
}

// RegisterHandler 注册消息类型处理器
func (gmh *GameMessageHandler) RegisterHandler(msgType string, handler func(*GameMessage) error) {
	gmh.mutex.Lock()
	defer gmh.mutex.Unlock()

	gmh.handlers[msgType] = handler
	logger.Debug(fmt.Sprintf("Registered handler for message type: %s", msgType))
}

// HandleMessage 处理消息
func (gmh *GameMessageHandler) HandleMessage(topic, channel string, data []byte) error {
	var gameMsg GameMessage
	if err := json.Unmarshal(data, &gameMsg); err != nil {
		return fmt.Errorf("failed to unmarshal game message: %v", err)
	}

	gmh.mutex.RLock()
	handler, exists := gmh.handlers[gameMsg.Type]
	gmh.mutex.RUnlock()

	if !exists {
		logger.Warn(fmt.Sprintf("No handler for message type: %s", gameMsg.Type))
		return nil
	}

	return handler(&gameMsg)
}

// ChatMessage 聊天消息
type ChatMessage struct {
	FromUserID uint64 `json:"from_user_id"`
	ToUserID   uint64 `json:"to_user_id"` // 0表示全服聊天
	Channel    int32  `json:"channel"`    // 聊天频道
	Content    string `json:"content"`
	Timestamp  int64  `json:"timestamp"`
}

// NewChatMessage 创建聊天消息
func NewChatMessage(fromUserID, toUserID uint64, channel int32, content string) *ChatMessage {
	return &ChatMessage{
		FromUserID: fromUserID,
		ToUserID:   toUserID,
		Channel:    channel,
		Content:    content,
		Timestamp:  time.Now().Unix(),
	}
}

// ChatMessageHandler 聊天消息处理器
type ChatMessageHandler struct {
	onMessage func(*ChatMessage) error
}

// NewChatMessageHandler 创建聊天消息处理器
func NewChatMessageHandler(onMessage func(*ChatMessage) error) *ChatMessageHandler {
	return &ChatMessageHandler{
		onMessage: onMessage,
	}
}

// HandleMessage 处理消息
func (cmh *ChatMessageHandler) HandleMessage(topic, channel string, data []byte) error {
	var chatMsg ChatMessage
	if err := json.Unmarshal(data, &chatMsg); err != nil {
		return fmt.Errorf("failed to unmarshal chat message: %v", err)
	}

	if cmh.onMessage != nil {
		return cmh.onMessage(&chatMsg)
	}

	return nil
}
