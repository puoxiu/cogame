package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"

	login_proto "github.com/puoxiu/cogame/pkg/login_proto/proto"
	"github.com/gogo/protobuf/proto"
)

// LoginClient 精简的登录测试客户端
type LoginClient struct {
	conn      net.Conn
	connected bool
}

// NewLoginClient 创建登录测试客户端
func NewLoginClient() *LoginClient {
	return &LoginClient{
		connected: false,
	}
}

// Connect 连接到登录服务器
func (lc *LoginClient) Connect(serverAddr string) error {
	log.Printf("Connecting to server: %s", serverAddr)
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return fmt.Errorf("连接服务器失败: %v", err)
	}

	lc.conn = conn
	lc.connected = true
	log.Printf("Connected to server: %s", serverAddr)
	return nil
}

// Disconnect 断开连接
func (lc *LoginClient) Disconnect() {
	if lc.connected && lc.conn != nil {
		lc.conn.Close()
		lc.connected = false
		log.Println("Disconnected from server")
	}
}

// sendLoginRequest 发送登录请求
func (lc *LoginClient) sendLoginRequest(username, password string) error {
	if !lc.connected {
		return fmt.Errorf("not connected to server")
	}

	// 构建登录请求数据
	loginReq := &login_proto.LoginRequest{
		Username: username,
		Password: password,
		Platform: "test",
		Version:  "1.0.0",
	}
	reqData, err := proto.Marshal(loginReq)
	if err != nil {
		return fmt.Errorf("marshal login request failed: %v", err)
	}

	// 构建基础请求结构（消息ID=1001对应登录）
	baseReq := &login_proto.BaseRequest{
		Header: &login_proto.MessageHeader{
			MsgId:     1001,
			Seq:       1,
			Timestamp: uint32(time.Now().Unix()),
		},
		Data: reqData,
	}
	baseReqData, err := proto.Marshal(baseReq)
	if err != nil {
		return fmt.Errorf("marshal base request failed: %v", err)
	}

	// 发送消息（格式：4字节长度 + 4字节消息ID + 消息内容）
	fullMsg := make([]byte, 4+len(baseReqData))
	binary.BigEndian.PutUint32(fullMsg[:4], uint32(len(baseReqData)))
	copy(fullMsg[4:], baseReqData)

	if _, err := lc.conn.Write(fullMsg); err != nil {
		return fmt.Errorf("send login request failed: %v", err)
	}
	return nil
}

// receiveLoginResponse 接收并解析登录响应
func (lc *LoginClient) receiveLoginResponse() (*login_proto.LoginResponse, error) {
	if !lc.connected {
		return nil, fmt.Errorf("not connected to server")
	}

	// 读取响应长度
	lenBuf := make([]byte, 4)
	if _, err := lc.conn.Read(lenBuf); err != nil {
		return nil, fmt.Errorf("read response length failed: %v", err)
	}
	respLen := binary.BigEndian.Uint32(lenBuf)

	// 读取响应内容
	respData := make([]byte, respLen)
	if _, err := lc.conn.Read(respData); err != nil {
		return nil, fmt.Errorf("read response content failed: %v", err)
	}

	// 解析基础响应
	var baseResp login_proto.BaseResponse
	if err := proto.Unmarshal(respData, &baseResp); err != nil {
		return nil, fmt.Errorf("marshal base response failed: %v", err)
	}

	// 检查业务错误
	if baseResp.Code != 0 {
		return nil, fmt.Errorf("login failed [error code: %d]: %s", baseResp.Code, baseResp.Msg)
	}

	// 解析登录响应数据
	var loginResp login_proto.LoginResponse
	if err := proto.Unmarshal(baseResp.Data, &loginResp); err != nil {
		return nil, fmt.Errorf("marshal login response failed: %v", err)
	}

	return &loginResp, nil
}

// TestLogin 执行登录测试
func (lc *LoginClient) TestLogin(username, password string) (*login_proto.LoginResponse, error) {
	log.Printf("start test login: username=%s, password=%s", username, password)

	// 发送登录请求
	if err := lc.sendLoginRequest(username, password); err != nil {
		return nil, fmt.Errorf("send login request failed: %v", err)
	}

	// 接收登录响应
	resp, err := lc.receiveLoginResponse()
	if err != nil {
		return nil, fmt.Errorf("receive login response failed: %v", err)
	}

	log.Printf("登录成功! 用户ID: %d, 令牌: %s", resp.UserId, resp.Token)
	log.Printf("用户信息: 昵称=%s, 等级=%d, 金币=%d, 钻石=%d", resp.Nickname, resp.Level, resp.Gold, resp.Diamond)
	return resp, nil
}

func main() {
	// 配置测试参数
	serverAddr := "127.0.0.1:8001"
	username := "testuser"
	password := "123456789"

	// 创建客户端并执行登录测试
	client := NewLoginClient()
	defer client.Disconnect()

	// 连接服务器
	if err := client.Connect(serverAddr); err != nil {
		log.Fatalf("connect server failed: %v", err)
	}

	// 执行登录测试
	if _, err := client.TestLogin(username, password); err != nil {
		log.Fatalf("test login failed: %v", err)
	}

	log.Println("login test completed, success!")
}