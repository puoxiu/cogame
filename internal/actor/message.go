package actor

// Message 消息接口
type Message interface {
	GetType() string		// 返回消息类型, 用于路由
	GetData() []byte		// 返回消息的二进制数据, 需业务层自行序列化 / 反序列化
}

// 消息类型常量
const (
	MSG_TYPE_USER_LOGIN   = "user_login"
	MSG_TYPE_USER_LOGOUT  = "user_logout"
	MSG_TYPE_GAME_START   = "game_start"
	MSG_TYPE_GAME_END     = "game_end"
	MSG_TYPE_CHAT_MSG     = "chat_msg"
	MSG_TYPE_FRIEND_REQ   = "friend_req"
	MSG_TYPE_MAIL_SEND    = "mail_send"
	MSG_TYPE_GM_CMD       = "gm_cmd"
	MSG_TYPE_SYSTEM_CMD   = "system_cmd"
	MSG_TYPE_RPC_REQUEST  = "rpc_request"
	MSG_TYPE_RPC_RESPONSE = "rpc_response"
)